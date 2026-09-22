package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeHTTPInitializeNegotiatesProtocolVersion(t *testing.T) {
	srv := &Server{}

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("MCP-Protocol-Version"); got != "2025-03-26" {
		t.Fatalf("expected negotiated protocol header, got %q", got)
	}

	var resp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("expected initialize result protocol version %q, got %q", "2025-03-26", resp.Result.ProtocolVersion)
	}
}

func TestServeHTTPAcceptsInitializedNotification(t *testing.T) {
	srv := &Server{}

	body := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty response body, got %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "" {
		t.Fatalf("expected no content type for notification response, got %q", got)
	}
}

func TestServeHTTPRejectsUnsupportedTransportMethods(t *testing.T) {
	srv := &Server{}

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/mcp", nil)
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected status 405, got %d", rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET, POST, DELETE" {
				t.Fatalf("expected Allow header, got %q", got)
			}
		})
	}
}

func TestServeHTTPRejectsOversizedBody(t *testing.T) {
	srv := &Server{}
	body := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"payload":"` + strings.Repeat("a", 256) + `"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Body = http.MaxBytesReader(rec, req.Body, 64)

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "request body exceeds the configured size limit") {
		t.Fatalf("expected payload too large message, got %s", rec.Body.String())
	}
}

func TestFilterToolList(t *testing.T) {
	tools := []toolSchema{
		{Name: "tool_a"},
		{Name: "tool_b", AllowedTypes: []string{"orchestrator"}},
		{Name: "tool_c", AllowedTypes: []string{"admin"}},
		{Name: "tool_d", AllowedTypes: []string{"orchestrator", "admin"}},
		{Name: "tool_e"},
	}

	t.Run("empty agent type returns all tools", func(t *testing.T) {
		filtered := filterToolList(tools, "")
		if len(filtered) != len(tools) {
			t.Fatalf("expected %d tools, got %d", len(tools), len(filtered))
		}
	})

	t.Run("orchestrator sees tools with no type and orchestrator", func(t *testing.T) {
		filtered := filterToolList(tools, "orchestrator")
		if len(filtered) != 4 {
			t.Fatalf("expected 4 tools, got %d", len(filtered))
		}
		names := make(map[string]bool)
		for _, t := range filtered {
			names[t.Name] = true
		}
		if !names["tool_a"] || !names["tool_b"] || !names["tool_d"] || !names["tool_e"] {
			t.Errorf("expected tool_a, tool_b, tool_d, tool_e; got %v", names)
		}
	})

	t.Run("admin sees tools with no type and admin", func(t *testing.T) {
		filtered := filterToolList(tools, "admin")
		if len(filtered) != 4 {
			t.Fatalf("expected 4 tools, got %d", len(filtered))
		}
		names := make(map[string]bool)
		for _, t := range filtered {
			names[t.Name] = true
		}
		if !names["tool_a"] || !names["tool_c"] || !names["tool_d"] || !names["tool_e"] {
			t.Errorf("expected tool_a, tool_c, tool_d, tool_e; got %v", names)
		}
	})

	t.Run("dev only sees tools with no type", func(t *testing.T) {
		filtered := filterToolList(tools, "dev")
		if len(filtered) != 2 {
			t.Fatalf("expected 2 tools (no restrictions), got %d", len(filtered))
		}
		names := make(map[string]bool)
		for _, t := range filtered {
			names[t.Name] = true
		}
		if !names["tool_a"] || !names["tool_e"] {
			t.Errorf("expected tool_a, tool_e; got %v", names)
		}
	})
}

func TestReadContextToolSchemaSupportsQueryKey(t *testing.T) {
	var readContext toolSchema
	for _, tool := range toolList {
		if tool.Name == "read_context" {
			readContext = tool
			break
		}
	}
	if readContext.Name == "" {
		t.Fatal("read_context tool schema not found")
	}

	properties, ok := readContext.InputSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("read_context properties missing")
	}
	if _, ok := properties["query_key"]; !ok {
		t.Fatal("read_context schema missing query_key")
	}

	required, ok := readContext.InputSchema["required"].([]string)
	if !ok {
		t.Fatal("read_context required field malformed")
	}
	if len(required) != 0 {
		t.Fatalf("read_context required = %v, want no required fields", required)
	}
}

func TestReadToolsDoNotRequireProjectID(t *testing.T) {
	t.Parallel()

	// search_context and read_context support AGENT/ORG scope queries where
	// project_id is optional. Their schemas must NOT list project_id as required.
	readTools := []string{"search_context", "read_context"}

	toolMap := make(map[string]toolSchema)
	for _, tool := range toolList {
		toolMap[tool.Name] = tool
	}

	for _, name := range readTools {
		t.Run(name, func(t *testing.T) {
			tool, ok := toolMap[name]
			if !ok {
				t.Fatalf("tool %q not found in toolList", name)
			}
			required, _ := tool.InputSchema["required"].([]string)
			for _, field := range required {
				if field == "project_id" {
					t.Errorf("%s: project_id must NOT be in required (AGENT/ORG scope queries don't need it)", name)
				}
			}
		})
	}
}

func TestAllWriteToolSchemasExposeInjectAudience(t *testing.T) {
	t.Parallel()

	writeTools := []string{
		"write_context",
		"write_identity",
		"write_memory",
		"write_knowledge",
		"write_convention",
		"write_org_knowledge",
		"store_principle",
		"write_chunk",
	}

	toolMap := make(map[string]toolSchema)
	for _, tool := range toolList {
		toolMap[tool.Name] = tool
	}

	for _, name := range writeTools {
		t.Run(name, func(t *testing.T) {
			tool, ok := toolMap[name]
			if !ok {
				t.Fatalf("tool %q not found in toolList", name)
			}
			properties, ok := tool.InputSchema["properties"].(map[string]interface{})
			if !ok {
				t.Fatalf("%s: properties missing or wrong type", name)
			}
			iaProp, ok := properties["inject_audience"]
			if !ok {
				t.Fatalf("%s: schema missing inject_audience property", name)
			}
			propMap, ok := iaProp.(map[string]interface{})
			if !ok {
				t.Fatalf("%s: inject_audience property is not a map", name)
			}
			if propMap["type"] != "object" {
				t.Errorf("%s: inject_audience type = %q, want \"object\"", name, propMap["type"])
			}
		})
	}
}

func TestUpdateContextSchemaExposesInjectAudience(t *testing.T) {
	t.Parallel()

	// update_context must expose inject_audience + clear_inject_audience so
	// agents can retarget or clear auto-injection without delete + recreate
	// (preserves chunk ID and version history). See GH #126.
	toolMap := make(map[string]toolSchema)
	for _, tool := range toolList {
		toolMap[tool.Name] = tool
	}
	tool, ok := toolMap["update_context"]
	if !ok {
		t.Fatal("tool \"update_context\" not found in toolList")
	}
	properties, ok := tool.InputSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("update_context: properties missing or wrong type")
	}
	iaProp, ok := properties["inject_audience"].(map[string]interface{})
	if !ok {
		t.Fatal("update_context: schema missing inject_audience property")
	}
	if iaProp["type"] != "object" {
		t.Errorf("update_context: inject_audience type = %q, want \"object\"", iaProp["type"])
	}
	clearProp, ok := properties["clear_inject_audience"].(map[string]interface{})
	if !ok {
		t.Fatal("update_context: schema missing clear_inject_audience property")
	}
	if clearProp["type"] != "boolean" {
		t.Errorf("update_context: clear_inject_audience type = %q, want \"boolean\"", clearProp["type"])
	}
}
