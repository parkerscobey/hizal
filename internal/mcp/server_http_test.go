package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
