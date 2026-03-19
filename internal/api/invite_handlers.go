package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/XferOps/winnow/internal/email"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const inviteTTL = 48 * time.Hour

type InviteHandlers struct {
	pool  *pgxpool.Pool
	email *email.Client // nil in local dev (EMAIL_FROM unset)
}

func NewInviteHandlers(ctx context.Context, pool *pgxpool.Pool) (*InviteHandlers, error) {
	ec, err := email.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("invite handlers: init email client: %w", err)
	}
	return &InviteHandlers{pool: pool, email: ec}, nil
}

// generateToken returns a 32-byte cryptographically random hex token.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// appBaseURL returns the public base URL for building invite links.
// Falls back to winnow.xferops.dev.
func appBaseURL() string {
	if u := os.Getenv("APP_BASE_URL"); u != "" {
		return u
	}
	return "https://winnow.xferops.dev"
}

// POST /v1/orgs/:id/invites
func (h *InviteHandlers) CreateInvite(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	caller, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "email is required")
		return
	}
	if body.Role == "" {
		body.Role = "member"
	}
	if !isValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "INVALID_ROLE", "role must be owner, admin, member, or viewer")
		return
	}

	// Fetch org name for email copy.
	var orgName string
	if err := h.pool.QueryRow(r.Context(), `SELECT name FROM orgs WHERE id = $1`, orgID).Scan(&orgName); err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// If user is already registered, add them directly and send a notification.
	var existingUserID string
	err := h.pool.QueryRow(r.Context(), `SELECT id FROM users WHERE email = $1`, body.Email).Scan(&existingUserID)
	if err == nil {
		// User exists — upsert membership.
		_, err = h.pool.Exec(r.Context(), `
			INSERT INTO org_memberships (user_id, org_id, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, org_id) DO UPDATE SET role = EXCLUDED.role
		`, existingUserID, orgID, body.Role)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}

		// Send "you've been added" notification (best-effort).
		loginURL := appBaseURL() + "/login"
		html, text := email.InviteExistingUserEmail(orgName, loginURL)
		_ = h.email.Send(r.Context(), email.Message{
			To:      body.Email,
			Subject: fmt.Sprintf("You've been added to %s on Hizal", orgName),
			HTML:    html,
			Text:    text,
		})

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"status":  "added",
			"message": "User already has an account — added to org directly.",
			"user_id": existingUserID,
			"role":    body.Role,
		})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// User doesn't exist — create invite record.
	token, err := generateToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_FAILED", err.Error())
		return
	}

	inviteID := uuid.New().String()
	expiresAt := time.Now().Add(inviteTTL)
	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO org_invites (id, org_id, invited_by, email, role, token, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, inviteID, orgID, caller.ID, body.Email, body.Role, token, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Send invite email (best-effort — don't fail the request if SES is down).
	inviteURL := fmt.Sprintf("%s/register?invite=%s", appBaseURL(), token)
	html, text := email.InviteEmail(orgName, inviteURL)
	_ = h.email.Send(r.Context(), email.Message{
		To:      body.Email,
		Subject: fmt.Sprintf("You've been invited to join %s on Hizal", orgName),
		HTML:    html,
		Text:    text,
	})

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         inviteID,
		"status":     "invited",
		"email":      body.Email,
		"role":       body.Role,
		"expires_at": expiresAt.Format(time.RFC3339),
	})
}

// GET /v1/orgs/:id/invites — list pending invites
func (h *InviteHandlers) ListInvites(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, email, role, expires_at, created_at
		FROM org_invites
		WHERE org_id = $1 AND accepted_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type inviteItem struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expires_at"`
		CreatedAt string `json:"created_at"`
	}
	var invites []inviteItem
	for rows.Next() {
		var i inviteItem
		var expiresAt, createdAt time.Time
		if err := rows.Scan(&i.ID, &i.Email, &i.Role, &expiresAt, &createdAt); err != nil {
			continue
		}
		i.ExpiresAt = expiresAt.Format(time.RFC3339)
		i.CreatedAt = createdAt.Format(time.RFC3339)
		invites = append(invites, i)
	}
	if invites == nil {
		invites = []inviteItem{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"invites": invites})
}

// DELETE /v1/orgs/:id/invites/:inviteId — cancel a pending invite
func (h *InviteHandlers) CancelInvite(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	inviteID := chi.URLParam(r, "inviteId")

	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM org_invites WHERE id = $1 AND org_id = $2 AND accepted_at IS NULL`,
		inviteID, orgID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "invite not found or already accepted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /v1/orgs/:id/invites/:inviteId/resend
func (h *InviteHandlers) ResendInvite(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "id")
	inviteID := chi.URLParam(r, "inviteId")

	if _, err := requireOrgRole(r, h.pool, orgID, "owner", "admin"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var orgName, inviteEmail, role, token string
	var expiresAt time.Time
	err := h.pool.QueryRow(r.Context(), `
		SELECT o.name, i.email, i.role, i.token, i.expires_at
		FROM org_invites i JOIN orgs o ON o.id = i.org_id
		WHERE i.id = $1 AND i.org_id = $2 AND i.accepted_at IS NULL
	`, inviteID, orgID).Scan(&orgName, &inviteEmail, &role, &token, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "invite not found or already accepted")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Extend expiry from now.
	newExpiry := time.Now().Add(inviteTTL)
	_, _ = h.pool.Exec(r.Context(),
		`UPDATE org_invites SET expires_at = $1 WHERE id = $2`, newExpiry, inviteID,
	)

	inviteURL := fmt.Sprintf("%s/register?invite=%s", appBaseURL(), token)
	html, text := email.InviteEmail(orgName, inviteURL)
	if err := h.email.Send(r.Context(), email.Message{
		To:      inviteEmail,
		Subject: fmt.Sprintf("You've been invited to join %s on Hizal", orgName),
		HTML:    html,
		Text:    text,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "EMAIL_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":         inviteID,
		"email":      inviteEmail,
		"expires_at": newExpiry.Format(time.RFC3339),
		"resent":     true,
	})
}

// POST /v1/auth/accept-invite — accept an invite token during registration
// Called by the frontend after the user submits the registration form with ?invite=<token>.
func (h *InviteHandlers) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if body.Token == "" || body.Name == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "token, name, and password are required")
		return
	}

	// Look up invite — must be valid and unexpired.
	var inviteID, orgID, inviteEmail, role string
	var expiresAt time.Time
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, org_id, email, role, expires_at
		FROM org_invites
		WHERE token = $1 AND accepted_at IS NULL AND expires_at > NOW()
	`, body.Token).Scan(&inviteID, &orgID, &inviteEmail, &role, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "INVALID_TOKEN", "invite token is invalid or has expired")
			return
		}
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Register the user (reuse auth handler logic inline).
	authH := NewAuthHandlers(h.pool)
	userID, jwtToken, err := authH.registerUser(r.Context(), inviteEmail, body.Password, body.Name)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "EMAIL_TAKEN", "an account with this email already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "REGISTER_FAILED", err.Error())
		return
	}

	// Add to org with the invited role.
	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO org_memberships (user_id, org_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, org_id) DO UPDATE SET role = EXCLUDED.role
	`, userID, orgID, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Mark invite as consumed.
	_, _ = h.pool.Exec(r.Context(),
		`UPDATE org_invites SET accepted_at = NOW() WHERE id = $1`, inviteID,
	)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"token":   jwtToken,
		"user_id": userID,
		"email":   inviteEmail,
		"name":    body.Name,
		"org_id":  orgID,
		"role":    role,
	})
}
