package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/XferOps/hizal/internal/audit"
	"github.com/XferOps/hizal/internal/email"
	"github.com/XferOps/hizal/internal/embeddings"
	"github.com/XferOps/hizal/internal/mcp"
	"github.com/XferOps/hizal/internal/usage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

const version = "0.2.1"

const (
	defaultBodyLimitBytes    = int64(1 << 20)
	chunkWriteBodyLimitBytes = int64(256 << 10)
	mcpBodyLimitBytes        = int64(512 << 10)
	authBodyLimitBytes       = int64(16 << 10)
)

func NewRouter(pool *pgxpool.Pool, embed *embeddings.Client) http.Handler {
	requireJWTSecretForStartup()

	initLimiters()
	startLimiterCleanup(5*time.Minute, 10*time.Minute)

	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(corsMiddleware)
	r.Use(BodyLimit(defaultBodyLimitBytes))

	r.Get("/health", healthHandler)

	var h *Handlers
	var mcpServer *mcp.Server
	var tracker *usage.Tracker
	var auditLogger *audit.AuditLogger
	var emailClient *email.Client
	if pool != nil {
		mcpServer = mcp.NewServer(pool, embed)
		h = NewHandlers(mcpServer.Tools(), pool)
		tracker = usage.New(pool)
		auditLogger = audit.New(pool)
		var emailErr error
		emailClient, emailErr = email.New(context.Background())
		if emailErr != nil {
			log.Printf("WARNING: email client failed to initialize: %v", emailErr)
		}
	}

	authH := NewAuthHandlers(pool, auditLogger, emailClient)
	inviteH, _ := NewInviteHandlers(context.Background(), pool)
	orgH := NewOrgHandlers(pool, auditLogger)
	projH := NewProjectHandlers(pool)
	projMemberH := NewProjectMembershipHandlers(pool)
	agentH := NewAgentHandlers(pool, auditLogger)
	agentKeyH := NewAgentKeyHandlers(pool)
	agentOnboardingH := NewAgentOnboardingHandlers(pool)
	skillH := NewSkillHandlers(pool)
	keyH := NewKeyHandlers(pool, auditLogger)
	var seedH *SeedHandlers
	if pool != nil && h != nil {
		seedH = NewSeedHandlers(pool, mcpServer.Tools())
	}
	billingH := NewBillingHandlers(pool)
	var sessionH *SessionHandlers
	if pool != nil && h != nil {
		sessionH = NewSessionHandlers(mcpServer.Tools(), pool)
	}
	agentTypeH := NewAgentTypeHandlers(pool)
	chunkTypeH := NewChunkTypeHandlers(pool)
	reviewH := NewReviewHandlers(pool)
	adminH := NewAdminHandlers(pool)

	pubH := NewPublicHandlers(pool, embed)

	// ── Public routes (no auth required) ───────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(RateLimit(30, 60))
		r.Get("/api/v1/public/chunks/search", pubH.SearchPublicChunks)
		r.Get("/api/v1/public/chunks", pubH.ListPublicChunks)
		r.Get("/api/v1/public/chunks/{chunkID}", pubH.GetPublicChunk)
	})

	// ── Public hub routes (JWT auth required) ──────────────────────────────
	// Rate: 20 adds per user per hour (20/3600 = 0.0056 per second)
	r.Group(func(r chi.Router) {
		r.Use(JWTAuth())
		r.Use(UserRateLimit(20.0/3600.0, 20))
		r.With(BodyLimit(chunkWriteBodyLimitBytes)).Post("/api/v1/public/chunks/{chunkID}/add", pubH.AddPublicChunk)
	})

	// Stripe webhook — no JWT auth, verified by Stripe-Signature header
	r.Post("/v1/webhooks/stripe", billingH.HandleWebhook)

	// ── Auth routes (no auth required for register/login) ──────────────────
	// Strict IP-based limits per endpoint: register 5/min, login 10/min, accept-invite 3/hour
	// These stack on top of the global 60/120 IP limiter applied at r.Group level.
	r.Route("/v1/auth", func(r chi.Router) {
		r.With(StrictIPRateLimit(5.0/60.0, 5), BodyLimit(authBodyLimitBytes)).Post("/register", authH.Register)
		r.With(StrictIPRateLimit(10.0/60.0, 10), BodyLimit(authBodyLimitBytes)).Post("/login", authH.Login)
		r.With(StrictIPRateLimit(10.0/60.0, 10), BodyLimit(authBodyLimitBytes)).Post("/refresh", authH.Refresh)
		r.With(JWTAuth()).Get("/me", authH.Me)
		r.With(JWTAuth(), BodyLimit(authBodyLimitBytes)).Patch("/me", authH.UpdateUser)
		r.With(StrictIPRateLimit(3.0/3600.0, 3), BodyLimit(authBodyLimitBytes)).Post("/accept-invite", inviteH.AcceptInvite)
		r.Post("/verify-email", authH.VerifyEmail)
		r.With(JWTAuth(), StrictIPRateLimit(3.0/3600.0, 3), BodyLimit(authBodyLimitBytes)).Post("/resend-verification", authH.ResendVerification)
	})

	// ── Bootstrap key creation (kept for backward compat, no auth required) ──
	r.Post("/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		// If JWT present, route to new key handler; otherwise legacy bootstrap
		if _, err := ParseJWT(extractBearer(r)); err == nil {
			// JWT path: inject user into context then call new handler
			claims, _ := ParseJWT(extractBearer(r))
			user := JWTUser{ID: claims.UserID, Email: claims.Email}
			keyH.CreateKey(w, r.WithContext(withJWTUser(r.Context(), user)))
		} else {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			h.CreateAPIKey(w, r)
		}
	})

	// ── JWT-protected routes ────────────────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(JWTAuth())

		// Orgs
		r.With(requireVerified(pool)).Post("/v1/orgs", orgH.CreateOrg)
		r.Get("/v1/orgs", orgH.ListOrgs)
		r.Get("/v1/orgs/{id}", orgH.GetOrg)
		r.Patch("/v1/orgs/{id}", orgH.UpdateOrg)
		r.Post("/v1/orgs/{id}/members", orgH.InviteMember)
		r.Delete("/v1/orgs/{id}/members/{userId}", orgH.RemoveMember)
		r.Patch("/v1/orgs/{id}/members/{userId}", orgH.UpdateMemberRole)

		// Org invites — user-based strict limit: 10/hour per user (SES cost control)
		r.With(requireVerified(pool), StrictUserRateLimit(10.0/3600.0, 10)).Post("/v1/orgs/{id}/invites", inviteH.CreateInvite)
		r.Get("/v1/orgs/{id}/invites", inviteH.ListInvites)
		r.Delete("/v1/orgs/{id}/invites/{inviteId}", inviteH.CancelInvite)
		r.Post("/v1/orgs/{id}/invites/{inviteId}/resend", inviteH.ResendInvite)

		// Projects
		r.Post("/v1/orgs/{id}/projects", projH.CreateProject)
		r.Get("/v1/orgs/{id}/projects", projH.ListProjects)
		r.Patch("/v1/projects/{id}", projH.UpdateProject)
		if seedH != nil {
			r.Post("/v1/projects/{id}/seed", seedH.SeedProject)
		}

		// Billing
		r.Get("/v1/orgs/{id}/usage", UsageHandler(pool))
		r.Post("/v1/billing/checkout", billingH.CreateCheckout)
		r.Post("/v1/billing/portal", billingH.CreatePortal)
		r.Post("/v1/billing/subscription/cancel", billingH.CancelSubscription)
		r.Post("/v1/billing/downgrade-choice", billingH.DowngradeChoice)

		// Project memberships
		r.Post("/v1/projects/{id}/members", projMemberH.AddMember)
		r.Get("/v1/projects/{id}/members", projMemberH.ListMembers)
		r.Patch("/v1/projects/{id}/members/{userId}", projMemberH.UpdateMemberRole)
		r.Delete("/v1/projects/{id}/members/{userId}", projMemberH.RemoveMember)

		// Agents
		r.Post("/v1/orgs/{id}/agents", agentH.CreateAgent)
		r.Get("/v1/orgs/{id}/agents", agentH.ListAgents)
		r.Get("/v1/agents/{id}", agentH.GetAgent)
		r.With(SkillAuth(pool)).Get("/v1/skills", skillH.List)
		r.With(SkillAuth(pool)).Get("/v1/skills/{id}", skillH.Get)
		r.Get("/api/v1/agents/{id}/onboarding", agentOnboardingH.GetForAgent)
		r.Get("/api/v1/agents/{id}/skills/{skillId}", skillH.GetForAgent)
		r.Patch("/v1/agents/{id}", agentH.UpdateAgent)
		r.Delete("/v1/agents/{id}", agentH.DeleteAgent)
		r.Post("/v1/agents/{id}/projects", agentH.AddProject)
		r.Delete("/v1/agents/{id}/projects/{projectId}", agentH.RemoveProject)

		// Agent keys
		r.Post("/v1/agents/{id}/keys", agentKeyH.CreateAgentKey)
		r.Get("/v1/agents/{id}/keys", agentKeyH.ListAgentKeys)
		r.Delete("/v1/agents/{id}/keys/{keyId}", agentKeyH.DeleteAgentKey)
		// API keys
		r.Get("/v1/keys", keyH.ListKeys)
		r.Delete("/v1/keys/{id}", keyH.DeleteKey)

		// Agent types
		r.Post("/v1/orgs/{id}/agent-types", agentTypeH.CreateAgentType)
		r.Get("/v1/orgs/{id}/agent-types", agentTypeH.ListAgentTypes)
		r.Post("/v1/orgs/{id}/agent-types/{slug}/fork", agentTypeH.ForkAgentType)
		r.Post("/v1/orgs/{id}/agent-types/{slug}/reset", agentTypeH.ResetOrHideAgentType)
		r.Get("/v1/agent-types/{id}", agentTypeH.GetAgentType)
		r.Patch("/v1/agent-types/{id}", agentTypeH.UpdateAgentType)
		r.Delete("/v1/agent-types/{id}", agentTypeH.DeleteAgentType)

		// Chunk types
		r.Post("/v1/orgs/{id}/chunk-types", chunkTypeH.CreateChunkType)
		r.Get("/v1/orgs/{id}/chunk-types", chunkTypeH.ListChunkTypes)
		r.Post("/v1/orgs/{id}/chunk-types/{slug}/override", chunkTypeH.ForkOverride)
		r.Delete("/v1/orgs/{id}/chunk-types/{slug}/override", chunkTypeH.ResetOrHide)
		r.Get("/v1/chunk-types/{id}", chunkTypeH.GetChunkType)
		r.Patch("/v1/chunk-types/{id}", chunkTypeH.UpdateChunkType)
		r.Delete("/v1/chunk-types/{id}", chunkTypeH.DeleteChunkType)

		// Review inbox
		r.Get("/v1/orgs/{id}/review-inbox", reviewH.ReviewInbox)

		// Sessions
		if sessionH != nil {
			r.Post("/v1/sessions", sessionH.StartSession)
			r.Post("/v1/sessions/{id}/resume", sessionH.ResumeSession)
			r.Post("/v1/sessions/{id}/focus", sessionH.RegisterFocus)
			r.Post("/v1/sessions/{id}/end", sessionH.EndSession)
			r.Get("/v1/sessions/{id}/memory-chunks", sessionH.GetSessionMemoryChunks)
			r.Get("/v1/sessions/{id}/consolidation-chunks", sessionH.GetSessionConsolidationChunks)
			r.Post("/v1/sessions/{id}/consolidate", sessionH.ConsolidateSession)
			r.Get("/v1/orgs/{id}/sessions", sessionH.ListSessions)
			r.Get("/v1/orgs/{id}/session-lifecycles", sessionH.ListSessionLifecycles)
			r.Post("/v1/orgs/{id}/session-lifecycles", sessionH.CreateSessionLifecycle)
			r.Post("/v1/orgs/{id}/session-lifecycles/{slug}/fork", sessionH.ForkSessionLifecycle)
			r.Post("/v1/orgs/{id}/session-lifecycles/{slug}/reset", sessionH.ResetOrHideSessionLifecycle)
			r.Get("/v1/session-lifecycles/{id}", sessionH.GetSessionLifecycle)
			r.Patch("/v1/session-lifecycles/{id}", sessionH.UpdateSessionLifecycle)
			r.Delete("/v1/session-lifecycles/{id}", sessionH.DeleteSessionLifecycle)
		}
	})

	// MCP endpoint (requires API key auth). POST serves JSON-RPC requests directly,
	// while GET/DELETE advertise stateless Streamable HTTP semantics to remote clients.
	// Rate: 120/min per API key (mixed read/write ops; stacks on global IP limit).
	r.With(BodyLimit(mcpBodyLimitBytes), APIKeyAuth(pool), APIKeyRateLimit(120.0/60.0, 60)).Route("/mcp", func(r chi.Router) {
		r.Method(http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mcpServer == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			mcpServer.ServeHTTP(w, r)
		}))
		r.Method(http.MethodPost, "/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mcpServer == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			// Track MCP calls generically as "read" (mixed ops; fine for v0.2)
			if tracker != nil {
				if claims, ok := ClaimsFrom(r.Context()); ok {
					tracker.Track(claims.OrgID, claims.ProjectID, usage.OpRead)
				}
			}
			mcpServer.ServeHTTP(w, r)
		}))
		r.Method(http.MethodDelete, "/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mcpServer == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			mcpServer.ServeHTTP(w, r)
		}))
	})

	// Dynamic agent onboarding endpoint (requires API key auth)
	r.With(APIKeyAuth(pool)).Get("/api/v1/agent-onboarding", agentOnboardingH.Get)
	r.With(SkillAuth(pool)).Get("/api/v1/skills", skillH.List)
	r.With(SkillAuth(pool)).Get("/api/v1/skills/{id}", skillH.Get)

	// Platform admin routes
	r.Route("/admin", func(r chi.Router) {
		r.Use(JWTAuth())
		r.Use(PlatformAdminOnly())
		r.Get("/orgs", adminH.ListOrgs)
		r.Get("/users", adminH.ListUsers)
		r.Get("/projects", adminH.ListProjects)
		r.Get("/keys", adminH.ListKeys)
	})

	// Usage analytics endpoint (requires auth, scoped to org)
	r.With(APIKeyAuth(pool)).Get("/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
			return
		}
		claims, ok := ClaimsFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "AUTH_INVALID", "no auth claims")
			return
		}

		days := 30
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := parseInt(d); err == nil && n > 0 {
				days = n
			}
		}
		filterProject := r.URL.Query().Get("project_id")

		snapshots, err := usage.Query(r.Context(), pool, claims.OrgID, filterProject, days)
		if err != nil {
			writeInternalError(r, w, "QUERY_FAILED", err)
			return
		}
		if snapshots == nil {
			snapshots = []usage.DailySnapshot{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"org_id": claims.OrgID,
			"days":   days,
			"data":   snapshots,
		})
	})

	// REST API (requires auth)
	r.Route("/v1/context", func(r chi.Router) {
		r.Use(ContextAuth(pool))

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			if tracker != nil {
				if claims, ok := ClaimsFrom(r.Context()); ok {
					tracker.Track(claims.OrgID, claims.ProjectID, usage.OpRead)
				}
			}
			h.ReadContext(w, r)
		})
		// Write ops: 60/min per API key (embedding cost control)
		r.With(APIKeyRateLimit(60.0/60.0, 30), BodyLimit(chunkWriteBodyLimitBytes)).Post("/", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			if tracker != nil {
				if claims, ok := ClaimsFrom(r.Context()); ok {
					tracker.Track(claims.OrgID, claims.ProjectID, usage.OpWrite)
				}
			}
			h.WriteContext(w, r)
		})
		// Search: 120/min per API key (pgvector query load)
		r.With(APIKeyRateLimit(120.0/60.0, 60)).Get("/search", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			if tracker != nil {
				if claims, ok := ClaimsFrom(r.Context()); ok {
					tracker.Track(claims.OrgID, claims.ProjectID, usage.OpSearch)
				}
			}
			h.SearchContext(w, r)
		})
		r.Get("/compact", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			if tracker != nil {
				if claims, ok := ClaimsFrom(r.Context()); ok {
					tracker.Track(claims.OrgID, claims.ProjectID, usage.OpCompact)
				}
			}
			h.CompactContext(w, r)
		})
		r.Get("/identity", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			if tracker != nil {
				if claims, ok := ClaimsFrom(r.Context()); ok {
					tracker.Track(claims.OrgID, claims.ProjectID, usage.OpRead)
				}
			}
			h.GetIdentity(w, r)
		})
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				if tracker != nil {
					if claims, ok := ClaimsFrom(r.Context()); ok {
						tracker.Track(claims.OrgID, claims.ProjectID, usage.OpRead)
					}
				}
				h.ReadContext(w, r)
			})
			r.Get("/versions", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				if tracker != nil {
					if claims, ok := ClaimsFrom(r.Context()); ok {
						tracker.Track(claims.OrgID, claims.ProjectID, usage.OpRead)
					}
				}
				h.GetContextVersions(w, r)
			})
			r.Get("/reviews", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				if tracker != nil {
					if claims, ok := ClaimsFrom(r.Context()); ok {
						tracker.Track(claims.OrgID, claims.ProjectID, usage.OpRead)
					}
				}
				h.GetContextReviews(w, r)
			})
			r.With(BodyLimit(chunkWriteBodyLimitBytes)).Patch("/", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				if tracker != nil {
					if claims, ok := ClaimsFrom(r.Context()); ok {
						tracker.Track(claims.OrgID, claims.ProjectID, usage.OpUpdate)
					}
				}
				h.UpdateContext(w, r)
			})
			r.Delete("/", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				if tracker != nil {
					if claims, ok := ClaimsFrom(r.Context()); ok {
						tracker.Track(claims.OrgID, claims.ProjectID, usage.OpDelete)
					}
				}
				h.DeleteContext(w, r)
			})
			r.With(BodyLimit(chunkWriteBodyLimitBytes)).Post("/review", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				if tracker != nil {
					if claims, ok := ClaimsFrom(r.Context()); ok {
						tracker.Track(claims.OrgID, claims.ProjectID, usage.OpReview)
					}
				}
				h.ReviewContext(w, r)
			})
		})
	})

	return r
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": version,
	})
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, &parseIntError{}
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

type parseIntError struct{}

func (e *parseIntError) Error() string { return "invalid integer" }

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return ""
}
