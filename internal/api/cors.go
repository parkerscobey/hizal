package api

import (
	"net/http"
	"os"
	"strings"
)

const (
	corsAllowHeaders     = "Authorization, Content-Type, X-Project-ID"
	corsAllowMethods     = "GET, POST, PATCH, DELETE, OPTIONS"
	corsAllowCredentials = "true"
	defaultAppBaseURL    = "https://winnow.xferops.dev"
)

func corsMiddleware(next http.Handler) http.Handler {
	origins := resolveAllowedOrigins()
	allowAll := len(origins) == 1 && origins[0] == "*"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if isAllowedOrigin(origin, origins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}

			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
			w.Header().Set("Access-Control-Allow-Credentials", corsAllowCredentials)
			w.Header().Set("Access-Control-Max-Age", "600")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func resolveAllowedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("CORS_ALLOW_ORIGINS"))
	if raw != "" {
		out := splitCSV(raw)
		if len(out) > 0 {
			return out
		}
	}

	// Safe local default for dev.
	if os.Getenv("ENV") == "development" {
		return []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	}

	if appBaseURL := strings.TrimSpace(os.Getenv("APP_BASE_URL")); appBaseURL != "" {
		return splitCSV(appBaseURL)
	}

	return []string{defaultAppBaseURL}
}

func isAllowedOrigin(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == origin {
			return true
		}
	}
	return false
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
