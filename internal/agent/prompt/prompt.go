// Package prompt builds the agent system prompt from templates and catalogs.
package prompt

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pulseaiclub/phi/internal/llm/skills"
)

var (
	//go:embed system-prompt.tmpl
	systemPromptTmpl string
	//go:embed skills-prompt.tmpl
	skillsPromptTmpl string
	//go:embed mcp-prompt.tmpl
	mcpPromptTmpl string

	systemPrompt = template.Must(template.New("system").Parse(systemPromptTmpl))
	skillsPrompt = template.Must(template.New("skills").Parse(skillsPromptTmpl))
	mcpPrompt    = template.Must(template.New("mcp").Parse(mcpPromptTmpl))
)

type systemData struct {
	Cwd           string
	Workspace     string
	AgentsEnabled bool
	MaxConcurrent int // sub-agent concurrency cap (0 when agents disabled)
}

type skillsData struct {
	Catalog string
}

type mcpData struct {
	Servers []string
}

// Build assembles the system prompt for a session rooted at cwd: its working
// directory, the AGENTS.md chain above it, the skills catalogs, and the MCP list.
// Passing the session cwd (not the process cwd) keeps sub-agents with their own
// workdir from reading the parent's project instructions.
// Empty cwd falls back to the process working directory.
// agentsEnabled must match whether agent_* tools are registered.
// mcpServers are configured server names only (no tool schemas).
func Build(cwd string, skillPaths []string, agentsEnabled bool, maxConcurrent int, mcpServers []string) string {
	cwd = resolveDir(cwd)
	var buf strings.Builder
	data := systemData{
		Cwd:           cwd,
		Workspace:     workspaceDir(cwd),
		AgentsEnabled: agentsEnabled,
		MaxConcurrent: maxConcurrent,
	}
	if err := systemPrompt.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("system prompt: %v", err))
	}
	parts := []string{buf.String()}
	if ctx := ProjectContext(cwd); ctx != "" {
		parts = append(parts, ctx)
	}
	if skillBlock := skillsBlock(skillPaths); skillBlock != "" {
		parts = append(parts, skillBlock)
	}
	if mcpBlock := mcpBlock(mcpServers); mcpBlock != "" {
		parts = append(parts, mcpBlock)
	}
	return strings.Join(parts, "\n\n")
}

func execTmpl(t *template.Template, data any) string {
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("%s prompt: %v", t.Name(), err))
	}
	return strings.TrimSpace(buf.String())
}

func skillsBlock(skillPaths []string) string {
	list := skills.LoadAll(skillPaths)
	if len(list) == 0 {
		return ""
	}
	catalog := strings.TrimSpace(skills.ToPromptMarkdown(list))
	if catalog == "" {
		return ""
	}
	return execTmpl(skillsPrompt, skillsData{Catalog: catalog})
}

func mcpBlock(serverNames []string) string {
	servers := make([]string, 0, len(serverNames))
	for _, name := range serverNames {
		name = strings.TrimSpace(name)
		if name != "" {
			servers = append(servers, name)
		}
	}
	if len(servers) == 0 {
		return ""
	}
	return execTmpl(mcpPrompt, mcpData{Servers: servers})
}

// resolveDir returns the absolute form of dir, falling back to the process
// working directory when dir is empty or when Abs fails. A prompt with a
// degraded cwd beats aborting the whole session.
func resolveDir(dir string) string {
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// workspaceDir returns the nearest ancestor of dir that contains .git, or "".
func workspaceDir(dir string) string {
	if dir == "" {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
