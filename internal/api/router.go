package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/XferOps/winnow/internal/embeddings"
	"github.com/XferOps/winnow/internal/mcp"
	"github.com/XferOps/winnow/internal/usage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

const version = "0.2.1"

func NewRouter(pool *pgxpool.Pool, embed *embeddings.Client) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(corsMiddleware)

	r.Get("/health", healthHandler)

	var h *Handlers
	var mcpServer *mcp.Server
	var tracker *usage.Tracker
	if pool != nil {
		mcpServer = mcp.NewServer(pool, embed)
		h = NewHandlers(mcpServer.Tools(), pool)
		tracker = usage.New(pool)
	}

	authH := NewAuthHandlers(pool)
	inviteH, _ := NewInviteHandlers(context.Background(), pool)
	orgH := NewOrgHandlers(pool)
	projH := NewProjectHandlers(pool)
	projMemberH := NewProjectMembershipHandlers(pool)
	agentH := NewAgentHandlers(pool)
	agentKeyH := NewAgentKeyHandlers(pool)
	agentOnboardingH := NewAgentOnboardingHandlers(pool)
	skillH := NewSkillHandlers(pool)
	keyH := NewKeyHandlers(pool)
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
	// Stripe webhook — no JWT auth, verified by Stripe-Signature header
	r.Post("/v1/webhooks/stripe", billingH.HandleWebhook)

	// ── Auth routes (no auth required for register/login) ──────────────────
	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/register", authH.Register)
		r.Post("/login", authH.Login)
		r.With(JWTAuth()).Get("/me", authH.Me)
		r.With(JWTAuth()).Patch("/me", authH.UpdateUser)
		r.Post("/accept-invite", inviteH.AcceptInvite)
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
		r.Post("/v1/orgs", orgH.CreateOrg)
		r.Get("/v1/orgs", orgH.ListOrgs)
		r.Get("/v1/orgs/{id}", orgH.GetOrg)
		r.Patch("/v1/orgs/{id}", orgH.UpdateOrg)
		r.Post("/v1/orgs/{id}/members", orgH.InviteMember)
		r.Delete("/v1/orgs/{id}/members/{userId}", orgH.RemoveMember)
		r.Patch("/v1/orgs/{id}/members/{userId}", orgH.UpdateMemberRole)

		// Org invites
		r.Post("/v1/orgs/{id}/invites", inviteH.CreateInvite)
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
		r.Get("/v1/agent-types/{id}", agentTypeH.GetAgentType)
		r.Patch("/v1/agent-types/{id}", agentTypeH.UpdateAgentType)
		r.Delete("/v1/agent-types/{id}", agentTypeH.DeleteAgentType)

		// Chunk types
		r.Post("/v1/orgs/{id}/chunk-types", chunkTypeH.CreateChunkType)
		r.Get("/v1/orgs/{id}/chunk-types", chunkTypeH.ListChunkTypes)
		r.Get("/v1/chunk-types/{id}", chunkTypeH.GetChunkType)
		r.Patch("/v1/chunk-types/{id}", chunkTypeH.UpdateChunkType)
		r.Delete("/v1/chunk-types/{id}", chunkTypeH.DeleteChunkType)

		// Sessions
		if sessionH != nil {
			r.Post("/v1/sessions", sessionH.StartSession)
			r.Post("/v1/sessions/{id}/resume", sessionH.ResumeSession)
			r.Post("/v1/sessions/{id}/focus", sessionH.RegisterFocus)
			r.Post("/v1/sessions/{id}/end", sessionH.EndSession)
			r.Get("/v1/sessions/{id}/memory-chunks", sessionH.GetSessionMemoryChunks)
			r.Post("/v1/sessions/{id}/consolidate", sessionH.ConsolidateSession)
			r.Get("/v1/orgs/{id}/sessions", sessionH.ListSessions)
			r.Get("/v1/orgs/{id}/session-lifecycles", sessionH.ListSessionLifecycles)
		}
	})

	// MCP endpoint (requires API key auth). POST serves JSON-RPC requests directly,
	// while GET/DELETE advertise stateless Streamable HTTP semantics to remote clients.
	r.With(APIKeyAuth(pool)).Route("/mcp", func(r chi.Router) {
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
			writeError(w, http.StatusInternalServerError, "QUERY_FAILED", err.Error())
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
		r.Post("/", func(w http.ResponseWriter, r *http.Request) {
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
		r.Get("/search", func(w http.ResponseWriter, r *http.Request) {
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
			r.Patch("/", func(w http.ResponseWriter, r *http.Request) {
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
			r.Post("/review", func(w http.ResponseWriter, r *http.Request) {
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
