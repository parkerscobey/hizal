package api

import (
	"encoding/json"
	"net/http"

	"github.com/XferOps/winnow/internal/embeddings"
	"github.com/XferOps/winnow/internal/mcp"
	"github.com/XferOps/winnow/internal/usage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

const version = "0.2.0"

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
	orgH := NewOrgHandlers(pool)
	projH := NewProjectHandlers(pool)
	projMemberH := NewProjectMembershipHandlers(pool)
	agentH := NewAgentHandlers(pool)
	agentKeyH := NewAgentKeyHandlers(pool)
	keyH := NewKeyHandlers(pool)

	// ── Auth routes (no auth required for register/login) ──────────────────
	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/register", authH.Register)
		r.Post("/login", authH.Login)
		r.With(JWTAuth()).Get("/me", authH.Me)
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

		// Projects
		r.Post("/v1/orgs/{id}/projects", projH.CreateProject)
		r.Get("/v1/orgs/{id}/projects", projH.ListProjects)
		r.Patch("/v1/projects/{id}", projH.UpdateProject)

		// Project memberships
		r.Post("/v1/projects/{id}/members", projMemberH.AddMember)
		r.Get("/v1/projects/{id}/members", projMemberH.ListMembers)
		r.Patch("/v1/projects/{id}/members/{userId}", projMemberH.UpdateMemberRole)
		r.Delete("/v1/projects/{id}/members/{userId}", projMemberH.RemoveMember)

		// Agents
		r.Post("/v1/orgs/{id}/agents", agentH.CreateAgent)
		r.Get("/v1/orgs/{id}/agents", agentH.ListAgents)
		r.Get("/v1/agents/{id}", agentH.GetAgent)
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
	})

	// MCP JSON-RPC endpoint (requires API key auth)
	r.With(APIKeyAuth(pool)).Post("/mcp", func(w http.ResponseWriter, r *http.Request) {
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
