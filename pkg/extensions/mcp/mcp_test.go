package mcp

import (
	"context"
	"testing"
)

// ── MCPRetryPolicy ────────────────────────────────────────────────────────────

func TestMCPRetryPolicy_ConnectionErrors(t *testing.T) {
	cases := []struct {
		code     MCPErrorCode
		expected int
	}{
		{MCPConnectionLost, 2},
		{MCPConnectionFailed, 2},
		{MCPConnectionTimeout, 2},
		{MCPRemoteError, 1},
		{MCPRemoteUnavailable, 1},
		{MCPClientError, 0},
	}

	for _, c := range cases {
		got := MCPRetryPolicy(c.code)
		if got != c.expected {
			t.Errorf("MCPRetryPolicy(%q) = %d, want %d", c.code, got, c.expected)
		}
	}
}

func TestMCPRetryPolicy_UnknownCode(t *testing.T) {
	if got := MCPRetryPolicy("UNKNOWN"); got != 0 {
		t.Errorf("unknown error code should return 0, got %d", got)
	}
}

// ── mcpToolName ───────────────────────────────────────────────────────────────

func TestMCPToolName(t *testing.T) {
	name := mcpToolName("server-1", "get_weather")
	expected := "mcp:server-1:get_weather"
	if name != expected {
		t.Errorf("expected %q, got %q", expected, name)
	}
}

func TestMCPToolName_EmptyParts(t *testing.T) {
	name := mcpToolName("", "")
	if name != "mcp::" {
		t.Errorf("expected 'mcp::', got %q", name)
	}
}

// ── MCPManager (no-network paths) ────────────────────────────────────────────

func TestMCPManager_ListServers_Empty(t *testing.T) {
	m := NewMCPManager(nil, nil)
	servers := m.ListServers()
	if len(servers) != 0 {
		t.Errorf("new manager should have 0 servers, got %d", len(servers))
	}
}

func TestMCPManager_ListToolSchemas_Empty(t *testing.T) {
	m := NewMCPManager(nil, nil)
	schemas := m.ListToolSchemas()
	if len(schemas) != 0 {
		t.Errorf("new manager should have 0 tool schemas, got %d", len(schemas))
	}
}

func TestMCPManager_CallTool_ServerNotFound(t *testing.T) {
	m := NewMCPManager(nil, nil)
	_, err := m.CallTool(context.Background(), "nonexistent-server", "some_tool", nil)
	if err == nil {
		t.Fatal("calling tool on non-existent server should return error")
	}
}

func TestMCPManager_Remove_NonExistent_NoOp(t *testing.T) {
	m := NewMCPManager(nil, nil)
	// Remove on non-existent ID should not panic
	m.Remove("ghost-id")
	if len(m.ListServers()) != 0 {
		t.Error("remove on empty manager should leave 0 servers")
	}
}

// ── MCPServerConfig defaults ──────────────────────────────────────────────────

func TestMCPServerConfig_TrustedDefault(t *testing.T) {
	cfg := MCPServerConfig{}
	if cfg.Trusted {
		t.Error("new MCPServerConfig should default to Trusted=false (conservative)")
	}
}

// ── A2AAgentCard ──────────────────────────────────────────────────────────────

func TestA2AAgentCard_Fields(t *testing.T) {
	card := A2AAgentCard{
		Capabilities: map[string]bool{"streaming": true},
		Skills:       []A2ASkillRef{{ID: "s1", Tags: []string{"retrieval"}}},
	}
	if len(card.Skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(card.Skills))
	}
	if !card.Capabilities["streaming"] {
		t.Error("streaming capability should be true")
	}
}

// ── Transport constants ───────────────────────────────────────────────────────

func TestMCPTransport_Values(t *testing.T) {
	if MCPStdio != "stdio" {
		t.Errorf("expected stdio, got %q", MCPStdio)
	}
	if MCPStreamableHTTP != "streamable_http" {
		t.Errorf("expected streamable_http, got %q", MCPStreamableHTTP)
	}
	if MCPSSE != "sse" {
		t.Errorf("expected sse, got %q", MCPSSE)
	}
}
