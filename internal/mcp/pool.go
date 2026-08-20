package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Pool lazily connects to configured MCP servers.
type Pool struct {
	mu      sync.Mutex
	servers map[string]ServerConfig
	clients map[string]Client
}

// DoctorResult is one row from Doctor.
type DoctorResult struct {
	Name   string
	OK     bool
	Detail string
	Tools  int
}

// NewPool wraps a server config map. Pass nil/empty for a no-op pool.
func NewPool(servers map[string]ServerConfig) *Pool {
	if servers == nil {
		servers = map[string]ServerConfig{}
	}
	return &Pool{
		servers: servers,
		clients: map[string]Client{},
	}
}

// LoadPool loads config for projectConfigPath (e.g. <root>/.phi/mcp.json)
// and returns a pool, or nil when disabled.
func LoadPool(projectConfigPath string) (*Pool, error) {
	if Disabled() {
		return nil, nil
	}
	servers, err := Load(projectConfigPath)
	if err != nil {
		return nil, err
	}
	return NewPool(servers), nil
}

// ServerNames returns sorted configured server names.
func (p *Pool) ServerNames() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.servers))
	for name := range p.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasServers reports whether any servers are configured.
func (p *Pool) HasServers() bool {
	return p != nil && len(p.ServerNames()) > 0
}

// ListTools lists tools for a server (lazy connect).
func (p *Pool) ListTools(ctx context.Context, server string) ([]ToolDef, error) {
	c, err := p.client(server)
	if err != nil {
		return nil, err
	}
	return c.ListTools(ctx)
}

// RefreshTools forces a fresh tool list for a server.
func (p *Pool) RefreshTools(ctx context.Context, server string) ([]ToolDef, error) {
	c, err := p.client(server)
	if err != nil {
		return nil, err
	}
	return c.RefreshTools(ctx)
}

// Inspect returns one tool definition.
func (p *Pool) Inspect(ctx context.Context, server, tool string) (*ToolDef, error) {
	c, err := p.client(server)
	if err != nil {
		return nil, err
	}
	return c.FindTool(ctx, tool)
}

// Call invokes a tool on a server.
func (p *Pool) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	c, err := p.client(server)
	if err != nil {
		return "", err
	}
	return c.CallTool(ctx, tool, args)
}

// Doctor checks config and connectivity for each server.
func (p *Pool) Doctor(ctx context.Context) []DoctorResult {
	if p == nil {
		return []DoctorResult{{Name: "(none)", OK: false, Detail: "mcp disabled or not loaded"}}
	}
	names := p.ServerNames()
	if len(names) == 0 {
		return []DoctorResult{{Name: "(none)", OK: false, Detail: "no servers in mcp.json"}}
	}
	out := make([]DoctorResult, 0, len(names))
	for _, name := range names {
		out = append(out, p.doctorOne(ctx, name))
	}
	return out
}

func (p *Pool) doctorOne(ctx context.Context, name string) DoctorResult {
	p.mu.Lock()
	cfg := p.servers[name]
	p.mu.Unlock()

	if err := validateServerConfig(cfg); err != nil {
		return DoctorResult{Name: name, OK: false, Detail: err.Error()}
	}
	tools, err := p.ListTools(ctx, name)
	if err != nil {
		return DoctorResult{Name: name, OK: false, Detail: err.Error()}
	}
	return DoctorResult{
		Name:   name,
		OK:     true,
		Detail: fmt.Sprintf("%d tools", len(tools)),
		Tools:  len(tools),
	}
}

func validateServerConfig(cfg ServerConfig) error {
	switch {
	case cfg.IsStdio():
		_, err := cfg.CmdLine()
		return err
	case cfg.IsHTTP():
		if strings.TrimSpace(cfg.URL) == "" {
			return errors.New("http transport requires url")
		}
		return nil
	default:
		return fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

// Close shuts down all live clients.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var first error
	for name, c := range p.clients {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
		delete(p.clients, name)
	}
	return first
}

func (p *Pool) client(server string) (Client, error) {
	if p == nil {
		return nil, errors.New("mcp pool is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[server]; ok {
		return c, nil
	}
	cfg, ok := p.servers[server]
	if !ok {
		return nil, fmt.Errorf("unknown mcp server %q", server)
	}
	c, err := NewClient(server, cfg)
	if err != nil {
		return nil, err
	}
	p.clients[server] = c
	return c, nil
}
