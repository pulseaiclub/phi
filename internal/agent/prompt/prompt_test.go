package prompt

import (
	"strings"
	"testing"
)

func TestBuildAgentsEnabledToggle(t *testing.T) {
	with := Build("", true, nil)
	without := Build("", false, nil)

	if !strings.Contains(with, "agent_task") {
		t.Fatal("expected agent_task guidance when agents enabled")
	}
	if !strings.Contains(with, "Sub-agents:") {
		t.Fatal("expected Sub-agents section when agents enabled")
	}
	if strings.Contains(without, "agent_task") || strings.Contains(without, "agent_spawn") {
		t.Fatal("did not expect sub-agent tool names when agents disabled")
	}
	if !strings.Contains(without, "`glob` / `grep` / `list` yourself") {
		t.Fatal("expected direct-search guidance when agents disabled")
	}
}

func TestBuildMCPCatalog(t *testing.T) {
	none := Build("", false, nil)
	if strings.Contains(none, "# MCP") {
		t.Fatal("expected no MCP section without servers")
	}

	got := Build("", false, []string{"browsermcp", "github"})
	if !strings.Contains(got, "# MCP") {
		t.Fatal("expected MCP section")
	}
	if !strings.Contains(got, "- browsermcp") || !strings.Contains(got, "- github") {
		t.Fatalf("expected server names in catalog, got:\n%s", got)
	}
	if !strings.Contains(got, "mcp_list") || !strings.Contains(got, "mcp_inspect") ||
		!strings.Contains(got, "mcp_call") {
		t.Fatal("expected mcp_* usage guidance")
	}
	if strings.Contains(got, `"properties"`) || strings.Contains(got, "inputSchema") {
		t.Fatal("MCP catalog must not include tool schemas")
	}
}
