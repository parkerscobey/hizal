package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/XferOps/contextor/internal/embeddings"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server implements a simple JSON-RPC 2.0 MCP server.
type Server struct {
	tools *Tools
}

// NewServer creates a new MCP server instance.
func NewServer(pool *pgxpool.Pool, embed *embeddings.Client) *Server {
	return &Server{
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
		Name:        "write_context",
		Description: "Store a context chunk with embedding for later retrieval.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query_key":    map[string]interface{}{"type": "string", "description": "Unique key for this context topic"},
				"title":        map[string]interface{}{"type": "string", "description": "Short descriptive title"},
				"content":      map[string]interface{}{"type": "string", "description": "Full context content"},
				"source_file":  map[string]interface{}{"type": "string", "description": "Source file path"},
				"source_lines": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "[start, end] line numbers"},
				"gotchas":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of gotchas/warnings"},
				"related":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Related query keys"},
			},
			"required": []string{"query_key", "title", "content"},
		},
	},
	{
		Name:        "search_context",
		Description: "Semantic search over stored context chunks.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":     map[string]interface{}{"type": "string", "description": "Search query"},
				"limit":     map[string]interface{}{"type": "integer", "description": "Max results (default 10)"},
				"query_key": map[string]interface{}{"type": "string", "description": "Filter by query_key"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "read_context",
		Description: "Fetch a context chunk by ID including version history.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string", "description": "Chunk UUID"},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "update_context",
		Description: "Update an existing context chunk, recording a new version.",
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
		Description: "Retrieve semantically relevant chunks for context compaction (no server-side summarization).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
				"limit": map[string]interface{}{"type": "integer", "description": "Max chunks (default 50)"},
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
				"chunk_id":         map[string]interface{}{"type": "string"},
				"task":             map[string]interface{}{"type": "string"},
				"usefulness":       map[string]interface{}{"type": "integer", "description": "1-5"},
				"usefulness_note":  map[string]interface{}{"type": "string"},
				"correctness":      map[string]interface{}{"type": "integer", "description": "1-5"},
				"correctness_note": map[string]interface{}{"type": "string"},
				"action":           map[string]interface{}{"type": "string", "enum": []string{"useful", "needs_update", "outdated", "incorrect"}},
			},
			"required": []string{"chunk_id", "usefulness", "correctness", "action"},
		},
	},
	{
		Name:        "delete_context",
		Description: "Delete a context chunk and all its versions and reviews.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string"},
			},
			"required": []string{"id"},
		},
	},
}

// ServeHTTP handles MCP JSON-RPC 2.0 requests.
// Expects X-Project-ID header for project scoping.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "contextor", "version": "0.1.0"},
		}

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
		result, err := s.dispatchTool(ctx, projectID, params.Name, params.Arguments)
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
		resp.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}

	json.NewEncoder(w).Encode(resp)
}

func (s *Server) dispatchTool(ctx context.Context, projectID, name string, args json.RawMessage) (interface{}, error) {
	switch name {
	case "write_context":
		var in WriteContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.WriteContext(ctx, projectID, in)

	case "search_context":
		var in SearchContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.SearchContext(ctx, projectID, in)

	case "read_context":
		var in ReadContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.ReadContext(ctx, projectID, in.ID)

	case "update_context":
		var in UpdateContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.UpdateContext(ctx, projectID, in)

	case "get_context_versions":
		var in GetVersionsInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.GetContextVersions(ctx, projectID, in.ID, in.Limit)

	case "compact_context":
		var in CompactContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.CompactContext(ctx, projectID, in)

	case "review_context":
		var in ReviewContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.ReviewContext(ctx, projectID, in)

	case "delete_context":
		var in DeleteContextInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		return s.tools.DeleteContext(ctx, projectID, in.ID)

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
