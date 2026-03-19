package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

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
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  map[string]interface{} `json:"inputSchema"`
	AllowedTypes []string               `json:"allowed_types,omitempty"`
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
		Description: "Store a context chunk with embedding for later retrieval. DEPRECATED: Use purpose-built tools (write_identity, write_memory, write_knowledge, write_convention, write_org_knowledge, store_principle) or write_chunk for custom org types.",
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
		Name:        "write_identity",
		Description: "Store an IDENTITY chunk scoped to an agent (always_inject=true). Use to define agent identity, personality, and core traits.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"agent_id":     map[string]interface{}{"type": "string", "description": "Agent UUID this identity applies to"},
				"query_key":    map[string]interface{}{"type": "string", "description": "Unique key for this identity topic"},
				"title":        map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":      map[string]interface{}{"type": "string", "description": "Identity content"},
				"source_file":  map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"agent_id", "query_key", "title", "content"},
		},
	},
	{
		Name:        "write_memory",
		Description: "Store a MEMORY chunk scoped to an agent (always_inject=false). Use for episodic context, conversation history, or task-specific memory.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"agent_id":     map[string]interface{}{"type": "string", "description": "Agent UUID this memory applies to"},
				"query_key":    map[string]interface{}{"type": "string", "description": "Unique key for this memory topic"},
				"title":        map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":      map[string]interface{}{"type": "string", "description": "Memory content"},
				"source_file":  map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"agent_id", "query_key", "title", "content"},
		},
	},
	{
		Name:        "write_knowledge",
		Description: "Store a KNOWLEDGE chunk scoped to a project (always_inject=false). Use for project-specific facts, documentation, and reference material.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id":   map[string]interface{}{"type": "string", "description": "Project UUID to scope this knowledge"},
				"query_key":    map[string]interface{}{"type": "string", "description": "Unique key for this knowledge topic"},
				"title":        map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":      map[string]interface{}{"type": "string", "description": "Knowledge content"},
				"source_file":  map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"project_id", "query_key", "title", "content"},
		},
	},
	{
		Name:        "write_convention",
		Description: "Store a CONVENTION chunk scoped to a project (always_inject=true). Use for coding standards, patterns, and project conventions.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id":   map[string]interface{}{"type": "string", "description": "Project UUID to scope this convention"},
				"query_key":    map[string]interface{}{"type": "string", "description": "Unique key for this convention topic"},
				"title":        map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":      map[string]interface{}{"type": "string", "description": "Convention content"},
				"source_file":  map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"project_id", "query_key", "title", "content"},
		},
	},
	{
		Name:        "write_org_knowledge",
		Description: "Store a KNOWLEDGE chunk scoped to an org (always_inject=false). Use for organization-wide facts and documentation.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"org_id":       map[string]interface{}{"type": "string", "description": "Org UUID to scope this knowledge"},
				"query_key":    map[string]interface{}{"type": "string", "description": "Unique key for this knowledge topic"},
				"title":        map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":      map[string]interface{}{"type": "string", "description": "Knowledge content"},
				"source_file":  map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"org_id", "query_key", "title", "content"},
		},
	},
	{
		Name:         "store_principle",
		Description:  "Store a PRINCIPLE chunk scoped to an org (always_inject=true). Requires promoted_by_user_id to enforce human promotion. Use for fundamental principles that agents must follow.",
		AllowedTypes: []string{"orchestrator", "admin"},
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"org_id":              map[string]interface{}{"type": "string", "description": "Org UUID to scope this principle"},
				"query_key":           map[string]interface{}{"type": "string", "description": "Unique key for this principle topic"},
				"title":               map[string]interface{}{"type": "string", "description": "Principle content"},
				"content":             map[string]interface{}{"type": "string", "description": "Principle content"},
				"promoted_by_user_id": map[string]interface{}{"type": "string", "description": "User ID of the human who promoted this principle (required)"},
				"source_file":         map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":             map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":             map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"org_id", "query_key", "title", "content", "promoted_by_user_id"},
		},
	},
	{
		Name:        "write_chunk",
		Description: "Generic chunk writing tool. Looks up the type's scope and always_inject defaults from the chunk_types table, then applies any overrides. Use this for custom org chunk types. The six named tools (write_identity, write_memory, write_knowledge, write_convention, write_org_knowledge, store_principle) remain for the 12 global defaults — they're opinionated shortcuts that guarantee correct semantics.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type":          map[string]interface{}{"type": "string", "description": "Chunk type slug (e.g. KNOWLEDGE, MEMORY, SPEC). Must be a valid type for the org."},
				"query_key":     map[string]interface{}{"type": "string", "description": "Unique key for this context topic"},
				"title":         map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":       map[string]interface{}{"type": "string", "description": "Full context content"},
				"project_id":    map[string]interface{}{"type": "string", "description": "Project UUID — required for PROJECT scope"},
				"agent_id":      map[string]interface{}{"type": "string", "description": "Agent UUID — required for AGENT scope"},
				"org_id":        map[string]interface{}{"type": "string", "description": "Org UUID — required for ORG scope"},
				"always_inject": map[string]interface{}{"type": "boolean", "description": "Override default_always_inject from chunk_types table"},
				"scope":         map[string]interface{}{"type": "string", "description": "Override default_scope from chunk_types table (PROJECT | AGENT | ORG)"},
				"source_file":   map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"type", "query_key", "title", "content"},
		},
	},
	{
		Name:        "search_context",
		Description: "Semantic search over stored context chunks. Searches across all accessible scopes by default (PROJECT, AGENT, ORG). Use scope, agent_id, org_id, chunk_type, and always_inject_only to narrow results.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id":         map[string]interface{}{"type": "string", "description": "Project UUID — required for PROJECT scope searches"},
				"query":              map[string]interface{}{"type": "string", "description": "Search query"},
				"scope":              map[string]interface{}{"type": "string", "description": "Filter to scope: PROJECT | AGENT | ORG. Omit to search all accessible scopes."},
				"agent_id":           map[string]interface{}{"type": "string", "description": "Filter to AGENT-scoped chunks for this agent UUID"},
				"org_id":             map[string]interface{}{"type": "string", "description": "Filter to ORG-scoped chunks for this org UUID"},
				"chunk_type":         map[string]interface{}{"type": "string", "description": "Filter by chunk_type: KNOWLEDGE | MEMORY | CONVENTION | IDENTITY | PRINCIPLE"},
				"always_inject_only": map[string]interface{}{"type": "boolean", "description": "If true, return only always_inject=true chunks"},
				"limit":              map[string]interface{}{"type": "integer", "description": "Max results (default 10)"},
				"query_key":          map[string]interface{}{"type": "string", "description": "Filter by exact query_key"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "read_context",
		Description: "Fetch a context chunk by ID or query_key including version history. If both are provided, id wins. Scope-aware: works for PROJECT, AGENT, and ORG chunks.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID — required when reading by query_key"},
				"id":         map[string]interface{}{"type": "string", "description": "Chunk UUID"},
				"query_key":  map[string]interface{}{"type": "string", "description": "Unique key for this context topic"},
			},
			"required": []string{},
		},
	},
	{
		Name:        "update_context",
		Description: "Update an existing context chunk, recording a new version. Scope-aware: works for chunks across all scopes.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":           map[string]interface{}{"type": "string"},
				"title":        map[string]interface{}{"type": "string"},
				"content":      map[string]interface{}{"type": "string"},
				"source_file":  map[string]interface{}{"type": "string"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"change_note":  map[string]interface{}{"type": "string", "description": "Required: describe what changed"},
			},
			"required": []string{"id", "change_note"},
		},
	},
	{
		Name:        "get_context_versions",
		Description: "List version history for a context chunk.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":    map[string]interface{}{"type": "string"},
				"limit": map[string]interface{}{"type": "integer", "description": "Max versions (default 10)"},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "compact_context",
		Description: "Retrieve semantically relevant chunks for agent-side synthesis. READ ONLY — never delete source chunks after writing a synthesis. Compaction is lossy; this tool is for reading, not merging. Scope-aware: use scope/agent_id/org_id to target the right memory layer.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID for PROJECT-scoped search"},
				"scope":      map[string]interface{}{"type": "string", "description": "Filter to scope: PROJECT | AGENT | ORG"},
				"agent_id":   map[string]interface{}{"type": "string", "description": "Filter to AGENT-scoped chunks"},
				"org_id":     map[string]interface{}{"type": "string", "description": "Filter to ORG-scoped chunks"},
				"chunk_type": map[string]interface{}{"type": "string", "description": "Filter by chunk_type"},
				"query":      map[string]interface{}{"type": "string"},
				"limit":      map[string]interface{}{"type": "integer", "description": "Max chunks (default 50)"},
			},
			"required": []string{"query"},
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
	{
		Name:        "start_session",
		Description: "Begin a new session for an agent. Returns the session ID and all always_inject chunks for the agent's context window. Fails if the agent already has an active session — use resume_session instead.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id":     map[string]interface{}{"type": "string", "description": "Primary project UUID for this session (optional)"},
				"lifecycle_slug": map[string]interface{}{"type": "string", "description": "Lifecycle preset: default, dev, admin, or org custom slug. Defaults to 'default'."},
			},
			"required": []string{},
		},
	},
	{
		Name:        "get_active_session",
		Description: "Returns the calling agent's current active session, derived from the API key. No input required. Use this to recover your session_id after a context reset or compaction. Returns status='none' if no active session exists — call start_session in that case.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	},
	{
		Name:        "resume_session",
		Description: "Extend an existing active session's TTL and re-inject always_inject chunks fresh. Use after a break or when resuming across tool calls.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": map[string]interface{}{"type": "string", "description": "UUID of the active session to resume"},
			},
			"required": []string{"session_id"},
		},
	},
	{
		Name:        "register_focus",
		Description: "Record what task the agent is currently working on within a session. Required if the lifecycle config has 'register_focus' in required_steps.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": map[string]interface{}{"type": "string", "description": "UUID of the active session"},
				"task":       map[string]interface{}{"type": "string", "description": "Description of the current task or goal"},
			},
			"required": []string{"session_id", "task"},
		},
	},
	{
		Name:        "end_session",
		Description: "Close the session and return chunks written during it for KEEP / PROMOTE / DISCARD consolidation review.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": map[string]interface{}{"type": "string", "description": "UUID of the active session to end"},
			},
			"required": []string{"session_id"},
		},
	},
	// Orchestrator tools (WNW-74): only available to orchestrator-type agents
	{
		Name:         "create_project",
		Description:  "Creates a new Winnow project within the orchestrator's org.",
		AllowedTypes: []string{"orchestrator"},
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "description": "Project name"},
				"slug":        map[string]interface{}{"type": "string", "description": "Optional project slug (auto-generated if omitted)"},
				"description": map[string]interface{}{"type": "string", "description": "Optional project description"},
			},
			"required": []string{"name"},
		},
	},
	{
		Name:         "list_agents",
		Description:  "Returns all agents in the org visible to the orchestrator.",
		AllowedTypes: []string{"orchestrator"},
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	},
	{
		Name:         "add_agent_to_project",
		Description:  "Assigns an agent to a project so it can read/write project-scoped context chunks.",
		AllowedTypes: []string{"orchestrator"},
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"agent_id":   map[string]interface{}{"type": "string", "description": "Agent UUID to assign"},
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID to assign agent to"},
			},
			"required": []string{"agent_id", "project_id"},
		},
	},
	{
		Name:         "remove_agent_from_project",
		Description:  "Removes an agent from a project when its work is done.",
		AllowedTypes: []string{"orchestrator"},
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"agent_id":   map[string]interface{}{"type": "string", "description": "Agent UUID to remove"},
				"project_id": map[string]interface{}{"type": "string", "description": "Project UUID to remove agent from"},
			},
			"required": []string{"agent_id", "project_id"},
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
		agentTypeSlug, err := s.resolveAgentType(ctx, r)
		if err != nil {
			log.Printf("resolve agent type for tools/list: %v", err)
		}
		filtered := filterToolList(toolList, agentTypeSlug)
		resp.Result = map[string]interface{}{"tools": filtered}

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

// ---- Orchestrator tool types (WNW-74) ----

type CreateProjectInput struct {
	Name        string  `json:"name"`
	Slug        *string `json:"slug,omitempty"`
	Description *string `json:"description,omitempty"`
}

type CreateProjectResult struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

type ListAgentsResult struct {
	Agents []AgentInfo `json:"agents"`
}

type AgentInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Slug          string   `json:"slug"`
	TypeID        *string  `json:"type_id,omitempty"`
	TypeSlug      *string  `json:"type_slug,omitempty"`
	MemoryEnabled bool     `json:"memory_enabled"`
	Projects      []string `json:"projects"`
}

type AddAgentToProjectInput struct {
	AgentID   string `json:"agent_id"`
	ProjectID string `json:"project_id"`
}

type AddAgentToProjectResult struct {
	AgentID   string `json:"agent_id"`
	ProjectID string `json:"project_id"`
	Confirmed bool   `json:"confirmed"`
}

type RemoveAgentFromProjectInput struct {
	AgentID   string `json:"agent_id"`
	ProjectID string `json:"project_id"`
}

type RemoveAgentFromProjectResult struct {
	AgentID   string `json:"agent_id"`
	ProjectID string `json:"project_id"`
	Confirmed bool   `json:"confirmed"`
}

type apiKeyScope struct {
	OwnerType         string
	AgentID           *string
	OrgID             string
	ScopeAllProjects  bool
	AllowedProjectIDs []string
	SearchFilters     models.AgentTypeFilterConfig
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

	case "write_identity":
		var in WriteIdentityInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.WriteIdentity(ctx, in)

	case "write_memory":
		var in WriteMemoryInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.WriteMemory(ctx, in)

	case "write_knowledge":
		var in WriteKnowledgeInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.WriteKnowledge(ctx, projectID, in)

	case "write_convention":
		var in WriteConventionInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.WriteConvention(ctx, projectID, in)

	case "write_org_knowledge":
		var in WriteOrgKnowledgeInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.tools.WriteOrgKnowledge(ctx, scope.OrgID, in)

	case "store_principle":
		var in StorePrincipleInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.tools.StorePrinciple(ctx, scope.OrgID, in)

	case "write_chunk":
		var in WriteChunkInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.WriteChunk(ctx, projectID, in)

	case "search_context":
		var in SearchContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.tools.SearchContext(ctx, projectID, in, scope.SearchFilters)

	case "read_context":
		var in ReadContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		projectID, err := s.resolveProjectID(ctx, r, headerProjectID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		return s.tools.ReadContext(ctx, projectID, in)

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
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.tools.CompactContext(ctx, projectID, in, scope.SearchFilters)

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

	case "start_session":
		var in StartSessionInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		agentID := ""
		if scope.AgentID != nil {
			agentID = *scope.AgentID
		}
		return s.tools.StartSession(ctx, scope.OrgID, agentID, in)

	case "get_active_session":
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		agentID := ""
		if scope.AgentID != nil {
			agentID = *scope.AgentID
		}
		return s.tools.GetActiveSession(ctx, agentID)

	case "resume_session":
		var in ResumeSessionInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.tools.ResumeSession(ctx, scope.OrgID, in)

	case "register_focus":
		var in RegisterFocusInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.tools.RegisterFocus(ctx, scope.OrgID, in)

	case "end_session":
		var in EndSessionInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.tools.EndSession(ctx, scope.OrgID, in)

	// Orchestrator tools (WNW-74)
	case "create_project":
		if err := s.requireOrchestrator(ctx, r); err != nil {
			return nil, err
		}
		var in CreateProjectInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.createProject(ctx, scope.OrgID, in)

	case "list_agents":
		if err := s.requireOrchestrator(ctx, r); err != nil {
			return nil, err
		}
		scope, err := s.loadAPIKeyScope(ctx, r)
		if err != nil {
			return nil, err
		}
		return s.listAgents(ctx, scope.OrgID)

	case "add_agent_to_project":
		if err := s.requireOrchestrator(ctx, r); err != nil {
			return nil, err
		}
		var in AddAgentToProjectInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.addAgentToProject(ctx, in)

	case "remove_agent_from_project":
		if err := s.requireOrchestrator(ctx, r); err != nil {
			return nil, err
		}
		var in RemoveAgentFromProjectInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.removeAgentFromProject(ctx, in)

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

// ---- Orchestrator tool implementations (WNW-74) ----

// requireOrchestrator checks that the calling agent has type 'orchestrator'.
func (s *Server) requireOrchestrator(ctx context.Context, r *http.Request) error {
	agentTypeSlug, err := s.resolveAgentType(ctx, r)
	if err != nil {
		return err
	}
	if agentTypeSlug != "orchestrator" {
		return fmt.Errorf("this tool is only available to orchestrator-type agents")
	}
	return nil
}

// createProject creates a new Winnow project within the orchestrator's org.
func (s *Server) createProject(ctx context.Context, orgID string, in CreateProjectInput) (*CreateProjectResult, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	slug := in.Name
	if in.Slug != nil && *in.Slug != "" {
		slug = *in.Slug
	}

	var result CreateProjectResult
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (org_id, name, slug, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, slug, created_at
	`, orgID, in.Name, slug, in.Description).Scan(&result.ProjectID, &result.Name, &result.Slug, &result.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}

	return &result, nil
}

// listAgents returns all agents in the org visible to the orchestrator.
func (s *Server) listAgents(ctx context.Context, orgID string) (*ListAgentsResult, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.name, a.slug, a.type_id, at.slug as type_slug, a.memory_enabled,
		       COALESCE(array_agg(ap.project_id) FILTER (WHERE ap.project_id IS NOT NULL), '{}'::uuid[]) as projects
		FROM agents a
		LEFT JOIN agent_types at ON at.id = a.type_id
		LEFT JOIN agent_projects ap ON ap.agent_id = a.id
		WHERE a.org_id = $1
		GROUP BY a.id, a.name, a.slug, a.type_id, at.slug, a.memory_enabled
		ORDER BY a.created_at ASC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []AgentInfo
	for rows.Next() {
		var a AgentInfo
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.TypeID, &a.TypeSlug, &a.MemoryEnabled, &a.Projects); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &ListAgentsResult{Agents: agents}, nil
}

// addAgentToProject assigns an agent to a project so it can read/write project-scoped context chunks.
func (s *Server) addAgentToProject(ctx context.Context, in AddAgentToProjectInput) (*AddAgentToProjectResult, error) {
	if in.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	if in.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_projects (agent_id, project_id)
		VALUES ($1, $2)
		ON CONFLICT (agent_id, project_id) DO NOTHING
	`, in.AgentID, in.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("add agent to project: %w", err)
	}

	return &AddAgentToProjectResult{
		AgentID:   in.AgentID,
		ProjectID: in.ProjectID,
		Confirmed: true,
	}, nil
}

// removeAgentFromProject removes an agent from a project when its work is done.
func (s *Server) removeAgentFromProject(ctx context.Context, in RemoveAgentFromProjectInput) (*RemoveAgentFromProjectResult, error) {
	if in.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	if in.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}

	_, err := s.pool.Exec(ctx, `
		DELETE FROM agent_projects
		WHERE agent_id = $1 AND project_id = $2
	`, in.AgentID, in.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("remove agent from project: %w", err)
	}

	return &RemoveAgentFromProjectResult{
		AgentID:   in.AgentID,
		ProjectID: in.ProjectID,
		Confirmed: true,
	}, nil
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

	if scope.AgentID != nil {
		scope.SearchFilters = s.resolveAgentSearchFilters(ctx, *scope.AgentID)
	}

	return scope, nil
}

func (s *Server) resolveAgentSearchFilters(ctx context.Context, agentID string) models.AgentTypeFilterConfig {
	var typeSearchFiltersJSON, overridesJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(
				(SELECT at.search_filters FROM agent_types at WHERE at.id = a.type_id),
				(SELECT at.search_filters FROM agent_types at WHERE at.slug = 'dev' AND at.org_id IS NULL LIMIT 1),
				'{}'::jsonb
			) AS type_search_filters,
			COALESCE(a.search_filter_overrides, '{}'::jsonb) AS overrides
		FROM agents a
		WHERE a.id = $1
	`, agentID).Scan(&typeSearchFiltersJSON, &overridesJSON)
	if err != nil {
		return models.AgentTypeFilterConfig{}
	}

	var typeFilters, overrides models.AgentTypeFilterConfig
	json.Unmarshal(typeSearchFiltersJSON, &typeFilters)
	json.Unmarshal(overridesJSON, &overrides)
	return mergeSearchFilters(typeFilters, overrides)
}

func mergeSearchFilters(typeFilters, overrides models.AgentTypeFilterConfig) models.AgentTypeFilterConfig {
	result := typeFilters

	if len(overrides.IncludeScopes) > 0 {
		result.IncludeScopes = overrides.IncludeScopes
	}
	if len(overrides.ExcludeScopes) > 0 {
		result.ExcludeScopes = overrides.ExcludeScopes
	}
	if len(overrides.IncludeChunkTypes) > 0 {
		result.IncludeChunkTypes = overrides.IncludeChunkTypes
	}
	if len(overrides.ExcludeChunkTypes) > 0 {
		result.ExcludeChunkTypes = overrides.ExcludeChunkTypes
	}
	if len(overrides.ExcludeQueryKeyPrefixes) > 0 {
		result.ExcludeQueryKeyPrefixes = overrides.ExcludeQueryKeyPrefixes
	}
	if overrides.OrgSearchRequiresExplicitScope {
		result.OrgSearchRequiresExplicitScope = true
	}

	return result
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

func (s *Server) resolveAgentType(ctx context.Context, r *http.Request) (string, error) {
	scope, err := s.loadAPIKeyScope(ctx, r)
	if err != nil {
		return "", err
	}

	if scope.AgentID == nil {
		return "", nil
	}

	var agentTypeSlug *string
	err = s.pool.QueryRow(ctx, `
		SELECT at.slug
		FROM agents a
		LEFT JOIN agent_types at ON at.id = a.type_id
		WHERE a.id = $1
	`, *scope.AgentID).Scan(&agentTypeSlug)
	if err != nil {
		return "", fmt.Errorf("resolve agent type: %w", err)
	}

	if agentTypeSlug == nil {
		return "", nil
	}
	return *agentTypeSlug, nil
}

func filterToolList(tools []toolSchema, agentTypeSlug string) []toolSchema {
	if agentTypeSlug == "" {
		return tools
	}

	filtered := make([]toolSchema, 0, len(tools))
	for _, tool := range tools {
		if len(tool.AllowedTypes) == 0 {
			filtered = append(filtered, tool)
			continue
		}
		for _, t := range tool.AllowedTypes {
			if t == agentTypeSlug {
				filtered = append(filtered, tool)
				break
			}
		}
	}
	return filtered
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
