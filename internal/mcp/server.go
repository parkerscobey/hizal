package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/XferOps/winnow/internal/auth"
	"github.com/XferOps/winnow/internal/embeddings"
	"github.com/XferOps/winnow/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server implements a simple JSON-RPC 2.0 MCP server.
type Server struct {
	pool  *pgxpool.Pool
	tools *Tools
}

// NewServer creates a new MCP server instance.
func NewServer(pool *pgxpool.Pool, embed *embeddings.Client) *Server {
	return &Server{
		pool:  pool,
		tools: NewTools(pool, embed),
	}
}

// Tools returns the underlying Tools instance (for use by REST handlers).
func (s *Server) Tools() *Tools {
	return s.tools
}

// ---- JSON-RPC types ----

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcErrResp(id interface{}, code int, msg string) jsonRPCResponse {
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// ---- Tool schemas for tools/list ----

type toolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

var toolList = []toolSchema{
	{
		Name:        "list_projects",
		Description: "List projects available to the current API key, including short descriptions.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "write_context",
		Description: "Store a context chunk with embedding for later retrieval.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id":   map[string]interface{}{"type": "string", "description": "Project UUID to scope this operation"},
				"query_key":    map[string]interface{}{"type": "string", "description": "Unique key for this context topic"},
				"title":        map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":      map[string]interface{}{"type": "string", "description": "Full context content"},
				"source_file":  map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"project_id", "query_key", "title", "content"},
		},
	},
	{
		Name:        "search_context",
		Description: "Semantic search over stored context chunks.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID to scope this search"},
				"query":      map[string]interface{}{"type": "string", "description": "Search query"},
				"limit":      map[string]interface{}{"type": "integer", "description": "Max results (default 10)"},
				"query_key":  map[string]interface{}{"type": "string", "description": "Filter by query_key"},
			},
			"required": []string{"project_id", "query"},
		},
	},
	{
		Name:        "read_context",
		Description: "Fetch a context chunk by ID including version history.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID containing this chunk"},
				"id":         map[string]interface{}{"type": "string", "description": "Chunk UUID"},
			},
			"required": []string{"project_id", "id"},
		},
	},
	{
		Name:        "update_context",
		Description: "Update an existing context chunk, recording a new version.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id":   map[string]interface{}{"type": "string", "description": "Project UUID containing this chunk"},
				"id":           map[string]interface{}{"type": "string"},
				"title":        map[string]interface{}{"type": "string"},
				"content":      map[string]interface{}{"type": "string"},
				"source_file":  map[string]interface{}{"type": "string"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"change_note":  map[string]interface{}{"type": "string", "description": "Required: describe what changed"},
			},
			"required": []string{"project_id", "id", "change_note"},
		},
	},
	{
		Name:        "get_context_versions",
		Description: "List version history for a context chunk.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID containing this chunk"},
				"id":         map[string]interface{}{"type": "string"},
				"limit":      map[string]interface{}{"type": "integer", "description": "Max versions (default 10)"},
			},
			"required": []string{"project_id", "id"},
		},
	},
	{
		Name:        "compact_context",
		Description: "Retrieve semantically relevant chunks for context compaction (no server-side summarization).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID to compact within"},
				"query":      map[string]interface{}{"type": "string"},
				"limit":      map[string]interface{}{"type": "integer", "description": "Max chunks (default 50)"},
			},
			"required": []string{"project_id", "query"},
		},
	},
	{
		Name:        "review_context",
		Description: "Submit usefulness/correctness feedback for a context chunk.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id":       map[string]interface{}{"type": "string", "description": "Project UUID containing this chunk"},
				"chunk_id":         map[string]interface{}{"type": "string"},
				"task":             map[string]interface{}{"type": "string"},
				"usefulness":       map[string]interface{}{"type": "integer", "description": "1-5"},
				"usefulness_note":  map[string]interface{}{"type": "string"},
				"correctness":      map[string]interface{}{"type": "integer", "description": "1-5"},
				"correctness_note": map[string]interface{}{"type": "string"},
				"action":           map[string]interface{}{"type": "string", "enum": []string{"useful", "needs_update", "outdated", "incorrect"}},
			},
			"required": []string{"project_id", "chunk_id", "usefulness", "correctness", "action"},
		},
	},
	{
		Name:        "delete_context",
		Description: "Delete a context chunk and all its versions and reviews.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID containing this chunk"},
				"id":         map[string]interface{}{"type": "string"},
			},
			"required": []string{"project_id", "id"},
		},
	},
}

// ServeHTTP handles MCP JSON-RPC 2.0 requests.
// Expects X-Project-ID header for project scoping.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "SSE streaming is not supported on this endpoint", http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "MCP sessions are not used on this endpoint", http.StatusMethodNotAllowed)
		return
	case http.MethodPost:
		// Handled below.
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	version, err := negotiatedProtocolVersionHeader(r.Header.Get("MCP-Protocol-Version"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if version != "" {
		w.Header().Set("MCP-Protocol-Version", version)
	}
	w.Header().Set("Content-Type", "application/json")

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(rpcErrResp(nil, -32700, "parse error"))
		return
	}

	projectID := r.Header.Get("X-Project-ID")
	ctx := r.Context()

	var resp jsonRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "initialize":
		version := initializeResponseVersion(req.Params)
		w.Header().Set("MCP-Protocol-Version", version)
		resp.Result = map[string]interface{}{
			"protocolVersion": version,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "winnow", "version": "0.2.1"},
		}

	case "ping":
		resp.Result = map[string]interface{}{}

	case "notifications/initialized":
		w.Header().Del("Content-Type")
		w.WriteHeader(http.StatusAccepted)
		return

	case "tools/list":
		resp.Result = map[string]interface{}{"tools": toolList}

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		result, err := s.dispatchTool(ctx, r, projectID, params.Name, params.Arguments)
		if err != nil {
			log.Printf("tool %s error: %v", params.Name, err)
			resp.Error = &rpcError{Code: -32603, Message: err.Error()}
		} else {
			resp.Result = map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": mustJSON(result)},
				},
			}
		}

	default:
		if isNotification(req) && strings.HasPrefix(req.Method, "notifications/") {
			w.Header().Del("Content-Type")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		resp.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}

	json.NewEncoder(w).Encode(resp)
}

func isNotification(req jsonRPCRequest) bool {
	return req.ID == nil
}

func initializeResponseVersion(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "2024-11-05"
	}

	var params initializeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return "2024-11-05"
	}

	if isSupportedProtocolVersion(params.ProtocolVersion) {
		return params.ProtocolVersion
	}

	return "2024-11-05"
}

func negotiatedProtocolVersionHeader(requested string) (string, error) {
	if requested == "" {
		return "", nil
	}
	if !isSupportedProtocolVersion(requested) {
		return "", fmt.Errorf("unsupported MCP protocol version: %s", requested)
	}
	return requested, nil
}

func isSupportedProtocolVersion(version string) bool {
	switch version {
	case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
		return true
	default:
		return false
	}
}

type serverProject struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description,omitempty"`
}

type listProjectsResult struct {
	Projects []serverProject `json:"projects"`
}

type apiKeyScope struct {
	OwnerType         string
	AgentID           *string
	OrgID             string
	ScopeAllProjects  bool
	AllowedProjectIDs []string
}

func (s *Server) dispatchTool(ctx context.Context, r *http.Request, headerProjectID, name string, args json.RawMessage) (interface{}, error) {
	switch name {
	case "list_projects":
		return s.listProjects(ctx, r)

	case "write_context":
		var in WriteContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.WriteContext(ctx, projectID, in)

	case "search_context":
		var in SearchContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.SearchContext(ctx, projectID, in)

	case "read_context":
		var in ReadContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.ReadContext(ctx, projectID, in.ID)

	case "update_context":
		var in UpdateContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.UpdateContext(ctx, projectID, in)

	case "get_context_versions":
		var in GetVersionsInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.GetContextVersions(ctx, projectID, in.ID, in.Limit)

	case "compact_context":
		var in CompactContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.CompactContext(ctx, projectID, in)

	case "review_context":
		var in ReviewContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.ReviewContext(ctx, projectID, in)

	case "delete_context":
		var in DeleteContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.DeleteContext(ctx, projectID, in.ID)

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) resolveProjectID(ctx context.Context, r *http.Request, headerProjectID, argProjectID string) (string, error) {
	projectID := strings.TrimSpace(argProjectID)
	if projectID == "" {
		projectID = strings.TrimSpace(headerProjectID)
	}
	if projectID == "" {
		return "", fmt.Errorf("project_id is required")
	}

	scope, err := s.loadAPIKeyScope(ctx, r)
	if err != nil {
		return "", err
	}
	projects, err := s.queryAccessibleProjects(ctx, scope)
	if err != nil {
		return "", err
	}
	for _, project := range projects {
		if project.ID == projectID {
			return projectID, nil
		}
	}
	return "", fmt.Errorf("project_id is not accessible for this API key")
}

func (s *Server) listProjects(ctx context.Context, r *http.Request) (*listProjectsResult, error) {
	scope, err := s.loadAPIKeyScope(ctx, r)
	if err != nil {
		return nil, err
	}
	projects, err := s.queryAccessibleProjects(ctx, scope)
	if err != nil {
		return nil, err
	}
	return &listProjectsResult{Projects: projects}, nil
}

func (s *Server) loadAPIKeyScope(ctx context.Context, r *http.Request) (*apiKeyScope, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("database not connected")
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("missing or invalid Authorization header")
	}
	keyHash := auth.HashKey(strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")))

	scope := &apiKeyScope{}
	err := s.pool.QueryRow(ctx, `
		SELECT owner_type, agent_id, org_id, scope_all_projects, allowed_project_ids
		FROM api_keys
		WHERE key_hash = $1
	`, keyHash).Scan(&scope.OwnerType, &scope.AgentID, &scope.OrgID, &scope.ScopeAllProjects, &scope.AllowedProjectIDs)
	if err != nil {
		return nil, fmt.Errorf("invalid API key")
	}
	return scope, nil
}

func (s *Server) queryAccessibleProjects(ctx context.Context, scope *apiKeyScope) ([]serverProject, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.name, p.slug, p.description
		FROM projects p
		WHERE p.id = ANY(
			CASE
				WHEN $1 = 'AGENT' THEN COALESCE(ARRAY(
					SELECT ap.project_id
					FROM agent_projects ap
					WHERE ap.agent_id = $2
				), '{}'::uuid[])
				WHEN $3 THEN COALESCE(ARRAY(
					SELECT p2.id
					FROM projects p2
					WHERE p2.org_id = $4
				), '{}'::uuid[])
				ELSE COALESCE($5::uuid[], '{}'::uuid[])
			END
		)
		ORDER BY p.created_at
	`, scope.OwnerType, scope.AgentID, scope.ScopeAllProjects, scope.OrgID, scope.AllowedProjectIDs)
	if err != nil {
		return nil, fmt.Errorf("load accessible projects: %w", err)
	}
	defer rows.Close()

	projects := make([]serverProject, 0)
	for rows.Next() {
		var project models.Project
		if err := rows.Scan(&project.ID, &project.Name, &project.Slug, &project.Description); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, serverProject{
			ID:          project.ID,
			Name:        project.Name,
			Slug:        project.Slug,
			Description: project.Description,
		})
	}
	return projects, rows.Err()
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
