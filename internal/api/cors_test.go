package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveAllowedOrigins(t *testing.T) {
	t.Setenv("CORS_ALLOW_ORIGINS", "")
	t.Setenv("APP_BASE_URL", "")
	t.Setenv("ENV", "")

	t.Run("prefers explicit cors origins", func(t *testing.T) {
		t.Setenv("CORS_ALLOW_ORIGINS", "https://one.example, https://two.example")
		t.Setenv("APP_BASE_URL", "https://app.example")

		got := resolveAllowedOrigins()
		want := []string{"https://one.example", "https://two.example"}
		assertStringSliceEqual(t, got, want)
	})

	t.Run("uses local defaults in development", func(t *testing.T) {
		t.Setenv("ENV", "development")

		got := resolveAllowedOrigins()
		want := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
		assertStringSliceEqual(t, got, want)
	})

	t.Run("falls back to app base url outside development", func(t *testing.T) {
		t.Setenv("APP_BASE_URL", "https://staging.example")

		got := resolveAllowedOrigins()
		want := []string{"https://staging.example"}
		assertStringSliceEqual(t, got, want)
	})

	t.Run("uses canonical app url when deploy env vars are missing", func(t *testing.T) {
		got := resolveAllowedOrigins()
		want := []string{defaultAppBaseURL}
		assertStringSliceEqual(t, got, want)
	})
}

func TestCORSMiddlewareHandlesPreflightFromAppBaseURLFallback(t *testing.T) {
	t.Setenv("CORS_ALLOW_ORIGINS", "")
	t.Setenv("APP_BASE_URL", "https://winnow.xferops.dev")
	t.Setenv("ENV", "production")

	req := httptest.NewRequest(http.MethodOptions, "/v1/auth/register", nil)
	req.Header.Set("Origin", "https://winnow.xferops.dev")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)

	rr := httptest.NewRecorder()
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called for preflight requests")
	}))

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://winnow.xferops.dev" {
		t.Fatalf("allow origin = %q, want %q", got, "https://winnow.xferops.dev")
	}
}

func assertStringSliceEqual(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}
