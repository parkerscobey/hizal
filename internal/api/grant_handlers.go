package api

import (
	"net/http"

	"github.com/XferOps/hizal/internal/mcp"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectGrants (#141): per-project cross-org grants.
//
// Granting an org access to a project is owner/admin-only (the same bar as
// project membership). A grant is per-project and explicit; org-to-org links are
// deliberately NOT offered — a link would share every future project too.
//
// Revocation is a hard DELETE: the reachability check in internal/mcp reads
// project_org_grants live, so removing a row revokes access on the NEXT tool
// call (fail-closed), not just for new attaches.

type ProjectGrantHandlers struct {
	pool *pgxpool.Pool
}

func NewProjectGrantHandlers(pool *pgxpool.Pool) *ProjectGrantHandlers {
	return &ProjectGrantHandlers{pool: pool}
}

// callerUserID returns the authenticated user's id, or "" when none is set
// (which NULLIF turns into a NULL granted_by).
func callerUserID(r *http.Request) string {
	if u, ok := JWTUserFrom(r.Context()); ok {
		return u.ID
	}
	return ""
}

// POST /v1/projects/{id}/grants  { org_id, role }
func (h *ProjectGrantHandlers) Grant(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")

	// Only owners/admins of the project's HOME org can grant a foreign org.
	_, grantorOrgID, err := requireProjectAccess(r, h.pool, projectID, "owner", "admin")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	var body struct {
		OrgID string `json:"org_id"`
		Role  string `json:"role"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeJSONDecodeError(w, err, "org_id is required")
		return
	}
	if body.OrgID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "org_id is required")
		return
	}
	if body.Role == "" {
		body.Role = "member"
	}
	if body.Role != "member" && body.Role != "viewer" {
		writeError(w, http.StatusBadRequest, "INVALID_ROLE", "role must be member or viewer")
		return
	}

	// The project's home org cannot be "granted" to itself — it already owns it.
	homeOrg, err := mcp.ProjectHomeOrg(r.Context(), h.pool, projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	if body.OrgID == homeOrg {
		writeError(w, http.StatusConflict, "SELF_GRANT", "the project's home org already has access")
		return
	}
	// Only the project's home org may grant; grantorOrgID must be the home org.
	if grantorOrgID != homeOrg {
		writeError(w, http.StatusForbidden, "NOT_HOME_ORG", "only the project's home org can grant access")
		return
	}

	// Target org must exist.
	var exists bool
	if err := h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM orgs WHERE id = $1)`, body.OrgID).Scan(&exists); err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "ORG_NOT_FOUND", "org not found")
		return
	}

	// UPSERT on (project_id, org_id): re-granting updates the role, keeping one
	// row per pair so revocation stays a single DELETE.
	var role string
	if err := h.pool.QueryRow(r.Context(), `
		INSERT INTO project_org_grants (project_id, org_id, role, granted_by)
		VALUES ($1, $2, $3, NULLIF($4,'')::uuid)
		ON CONFLICT (project_id, org_id) DO UPDATE SET role = EXCLUDED.role
		RETURNING role`, projectID, body.OrgID, body.Role, callerUserID(r)).Scan(&role); err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"project_id": projectID,
		"org_id":     body.OrgID,
		"role":       role,
	})
}

// DELETE /v1/projects/{id}/grants/{orgId}
func (h *ProjectGrantHandlers) Revoke(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	orgID := chi.URLParam(r, "orgId")

	_, grantorOrgID, err := requireProjectAccess(r, h.pool, projectID, "owner", "admin")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	homeOrg, err := mcp.ProjectHomeOrg(r.Context(), h.pool, projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	if grantorOrgID != homeOrg {
		writeError(w, http.StatusForbidden, "NOT_HOME_ORG", "only the project's home org can revoke access")
		return
	}

	// Hard DELETE: reachability is read live, so this takes effect on the next
	// tool call (fail-closed).
	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM project_org_grants WHERE project_id = $1 AND org_id = $2`, projectID, orgID)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "GRANT_NOT_FOUND", "no grant for that org on this project")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /v1/projects/{id}/grants
func (h *ProjectGrantHandlers) List(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if _, _, err := requireProjectAccess(r, h.pool, projectID, "owner", "admin", "member", "viewer"); err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT g.org_id, o.name, g.role, g.created_at
		  FROM project_org_grants g JOIN orgs o ON o.id = g.org_id
		 WHERE g.project_id = $1
		 ORDER BY o.name`, projectID)
	if err != nil {
		writeInternalError(r, w, "DB_ERROR", err)
		return
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var orgID, name, role string
		var createdAt interface{}
		if err := rows.Scan(&orgID, &name, &role, &createdAt); err != nil {
			writeInternalError(r, w, "DB_ERROR", err)
			return
		}
		out = append(out, map[string]interface{}{
			"org_id": orgID, "org_name": name, "role": role, "created_at": createdAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"grants": out})
}
