package api

import (
	"encoding/json"
	"net/http"

	"github.com/XferOps/contextor/internal/embeddings"
	"github.com/XferOps/contextor/internal/mcp"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

const version = "0.1.0"

func NewRouter(pool *pgxpool.Pool, embed *embeddings.Client) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	r.Get("/health", healthHandler)

	// Bootstrap: create API key (no auth required)
	var h *Handlers
	var mcpServer *mcp.Server
	if pool != nil {
		mcpServer = mcp.NewServer(pool, embed)
		h = NewHandlers(mcpServer.Tools(), pool)
	}

	r.Post("/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		if h == nil {
			writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
			return
		}
		h.CreateAPIKey(w, r)
	})

	// MCP JSON-RPC endpoint (requires auth)
	r.With(APIKeyAuth(pool)).Post("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if mcpServer == nil {
			writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
			return
		}
		mcpServer.ServeHTTP(w, r)
	})

	// REST API (requires auth)
	r.Route("/v1/context", func(r chi.Router) {
		r.Use(APIKeyAuth(pool))

		r.Post("/", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			h.WriteContext(w, r)
		})
		r.Get("/search", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			h.SearchContext(w, r)
		})
		r.Get("/compact", func(w http.ResponseWriter, r *http.Request) {
			if h == nil {
				writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
				return
			}
			h.CompactContext(w, r)
		})
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				h.ReadContext(w, r)
			})
			r.Get("/versions", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				h.GetContextVersions(w, r)
			})
			r.Patch("/", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				h.UpdateContext(w, r)
			})
			r.Delete("/", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
				}
				h.DeleteContext(w, r)
			})
			r.Post("/review", func(w http.ResponseWriter, r *http.Request) {
				if h == nil {
					writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not connected")
					return
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
