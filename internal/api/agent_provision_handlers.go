package api

import (
	"net/http"

	"github.com/XferOps/hizal/internal/audit"
	"github.com/XferOps/hizal/internal/auth"
	"github.com/XferOps/hizal/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentProvisionHandlers serves agent creation over API-key auth, so an
// autonomous agent can provision a sibling agent (campfire orchestration).
//
// This deliberately duplicates NONE of the org-role logic that the JWT path
// uses — a key has no user role. Instead it derives authority from the key's
// own claims, with three hard rules:
//
//  1. The key must carry can_create_agents (opt-in; off by default).
//  2. The key must be USER-owned. An AGENT-owned key can never create agents,
//     so an agent cannot mint sibling agents — no self-replication.
//  3. The new agent is created in the key's org and owned by the key's owning
//     user. Cross-org creation is impossible: org_id comes from the key.
type AgentProvisionHandlers struct {
	pool        *pgxpool.Pool
	auditLogger *audit.AuditLogger
}

func NewAgentProvisionHandlers(pool *pgxpool.Pool, auditLogger *audit.AuditLogger) *AgentProvisionHandlers {
	return &AgentProvisionHandlers{pool: pool, auditLogger: auditLogger}
}

// requireCreateAgentsScope resolves the caller's claims and enforces the three
// rules above. Returns the claims (org_id, key id, owning user id) or writes
// the error and returns ok=false.
func (h *AgentProvisionHandlers) requireCreateAgentsScope(w http.ResponseWriter, r *http.Request) (claims AuthClaims, ownerUserID string, ok bool) {
	claims, ok = ClaimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "API key auth required")
		return claims, "", false
	}
	if claims.OrgID == "" {
		writeError(w, http.StatusForbidden, "NO_ORG", "API key is not bound to an org")
		return claims, "", false
	}
	// Rule 2: user-owned only. An agent-owned key has AgentID set and no user.
	if claims.AgentID != "" {
		writeError(w, http.StatusForbidden, "AGENT_KEY_CANNOT_PROVISION",
			"an agent-owned key cannot create agents; use a user-owned key with the create-agents scope")
		return claims, "", false
	}
	// Rule 1: opt-in scope, read from the key row (claims don't carry it).
	var canCreate bool
	var userID *string
	err := h.pool.QueryRow(r.Context(),
		`SELECT can_create_agents, user_id FROM api_keys WHERE id = $1`, claims.KeyID,
	).Scan(&canCreate, &userID)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return claims, "", false
	}
	if !canCreate {
		writeError(w, http.StatusForbidden, "SCOPE_REQUIRED",
			"this API key lacks the create-agents scope (can_create_agents)")
		return claims, "", false
	}
	if userID == nil || *userID == "" {
		writeError(w, http.StatusForbidden, "AGENT_KEY_CANNOT_PROVISION",
			"the key has no owning user; agents require a user owner")
		return claims, "", false
	}
	return claims, *userID, true
}

// POST /v1/agents  (API-key auth, requires create-agents scope)
func (h *AgentProvisionHandlers) CreateAgent(w http.ResponseWriter, r *http.Request) {
	claims, ownerUserID, ok := h.requireCreateAgentsScope(w, r)
	if !ok {
		return
	}
	orgID := claims.OrgID

	var body struct {
		Name        string   `json:"name"`
		Slug        string   `json:"slug"`
		Type        string   `json:"type"`
		TypeID      string   `json:"type_id"`
		Description string   `json:"description"`
		Platform    string   `json:"platform"`
		InstanceID  string   `json:"instance_id"`
		Tags        []string `json:"tags"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "name and slug are required")
		return
	}
	if body.Name == "" || body.Slug == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name and slug are required")
		return
	}

	var agentType, typeID *string
	if body.TypeID != "" {
		var slug string
		err := h.pool.QueryRow(r.Context(), `
			SELECT slug FROM agent_types WHERE id = $1 AND (org_id IS NULL OR org_id = $2)
		`, body.TypeID, orgID).Scan(&slug)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_TYPE_ID", "type_id is not a valid agent type for this org")
			return
		}
		typeID = &body.TypeID
		agentType = &slug
	} else if body.Type != "" {
		if !validAgentTypes[body.Type] {
			writeError(w, http.StatusBadRequest, "INVALID_TYPE", "type must be ASSISTANT, CODER, QA, OPS, or CUSTOM")
			return
		}
		agentType = &body.Type
	} else {
		defaultType := "CUSTOM"
		agentType = &defaultType
	}

	agent := models.Agent{
		ID:      uuid.New().String(),
		OrgID:   orgID,
		OwnerID: ownerUserID,
		Name:    body.Name,
		Slug:    body.Slug,
		Type:    *agentType,
		Status:  "ACTIVE",
		TypeID:  typeID,
		Tags:    body.Tags,
	}
	if agent.Tags == nil {
		agent.Tags = []string{}
	}
	_, err := h.pool.Exec(r.Context(), `
		INSERT INTO agents (id, org_id, owner_id, name, slug, type, type_id, description, platform, instance_id, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, agent.ID, agent.OrgID, agent.OwnerID, agent.Name, agent.Slug, agent.Type, agent.TypeID,
		nullableStr(body.Description), nullableStr(body.Platform), nullableStr(body.InstanceID), agent.Tags)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "SLUG_TAKEN", "an agent with that slug already exists in this org")
			return
		}
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	if h.auditLogger != nil {
		h.auditLogger.Log(r.Context(), audit.Entry{
			OrgID:        orgID,
			ActorType:    audit.ActorTypeAPIKey,
			ActorID:      claims.KeyID,
			Action:       "AGENT_CREATED",
			ResourceType: strPtr("agent"),
			ResourceID:   &agent.ID,
			Metadata:     map[string]any{"agent_name": agent.Name, "agent_type": agent.Type, "via": "api_key"},
		})
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       agent.ID,
		"org_id":   agent.OrgID,
		"owner_id": agent.OwnerID,
		"name":     agent.Name,
		"slug":     agent.Slug,
		"type":     agent.Type,
		"type_id":  agent.TypeID,
		"status":   agent.Status,
	})
}

// POST /v1/agents/{id}/keys  (API-key auth, requires create-agents scope)
//
// Mints an AGENT-owned key for an agent in the caller's org. Scope is required
// for the same reason creation is: minting a key is provisioning an identity.
func (h *AgentProvisionHandlers) CreateAgentKey(w http.ResponseWriter, r *http.Request) {
	claims, _, ok := h.requireCreateAgentsScope(w, r)
	if !ok {
		return
	}
	agentID := chi.URLParam(r, "id")

	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "name is required")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "name is required")
		return
	}

	// Rule 3: the agent must live in the key's org. This is the cross-org guard.
	var agent models.Agent
	if err := h.pool.QueryRow(r.Context(),
		`SELECT id, org_id, slug FROM agents WHERE id = $1`, agentID,
	).Scan(&agent.ID, &agent.OrgID, &agent.Slug); err != nil {
		writeError(w, http.StatusNotFound, "AGENT_NOT_FOUND", "no such agent")
		return
	}
	if agent.OrgID != claims.OrgID {
		// Deliberately 404, not 403: do not confirm the existence of an agent
		// in another org to a key that cannot reach it.
		writeError(w, http.StatusNotFound, "AGENT_NOT_FOUND", "no such agent")
		return
	}

	plaintext, keyHash, err := auth.GenerateAPIKey(agent.Slug)
	if err != nil {
		writeInternalError(r, w, "KEYGEN_FAILED", err)
		return
	}

	var keyID string
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO api_keys (owner_type, agent_id, org_id, key_hash, name, scope_all_projects, allowed_project_ids)
		VALUES ('AGENT', $1, $2, $3, $4, FALSE, '{}')
		RETURNING id
	`, agentID, agent.OrgID, keyHash, body.Name).Scan(&keyID)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	if h.auditLogger != nil {
		h.auditLogger.Log(r.Context(), audit.Entry{
			OrgID:        claims.OrgID,
			ActorType:    audit.ActorTypeAPIKey,
			ActorID:      claims.KeyID,
			Action:       "AGENT_KEY_CREATED",
			ResourceType: strPtr("agent_key"),
			ResourceID:   &keyID,
			Metadata:     map[string]any{"agent_id": agentID, "key_name": body.Name, "via": "api_key"},
		})
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       keyID,
		"key":      plaintext,
		"name":     body.Name,
		"agent_id": agentID,
		"note":     "Store this key securely — it will not be shown again.",
	})
}
