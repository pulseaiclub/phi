package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	toolsCacheTTL     = 5 * time.Minute
	maxToolsListPages = 100
)

// transport is the wire protocol for one MCP connection.
// Implementations must be safe for use under session's mutex
// (one call at a time per session).
type transport interface {
	call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params map[string]any) error
	close() error
}

// session implements Client on top of a transport: handshake, tool cache, call.
type session struct {
	name string
	tr   transport

	mu             sync.Mutex
	tools          []ToolDef
	toolsValid     bool
	toolsFetchedAt time.Time
	ready          bool
}

func newSession(name string, tr transport) *session {
	return &session{name: name, tr: tr}
}

func (s *session) Initialize(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initLocked(ctx)
}

func (s *session) initLocked(ctx context.Context) error {
	if s.ready {
		return nil
	}
	if _, err := s.tr.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "phi", "version": "0.1"},
	}); err != nil {
		s.handleInitErrorLocked(err)
		return err
	}
	if err := s.tr.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		s.handleInitErrorLocked(err)
		return err
	}
	s.ready = true
	return nil
}

func (s *session) ListTools(ctx context.Context) ([]ToolDef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listToolsLocked(ctx, false)
}

func (s *session) RefreshTools(ctx context.Context) ([]ToolDef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listToolsLocked(ctx, true)
}

func (s *session) listToolsLocked(ctx context.Context, force bool) ([]ToolDef, error) {
	if err := s.initLocked(ctx); err != nil {
		return nil, err
	}
	if !force && s.toolsValid && time.Since(s.toolsFetchedAt) < toolsCacheTTL {
		return cloneTools(s.tools), nil
	}

	tools, err := s.fetchToolsLocked(ctx)
	if err != nil {
		s.handleToolsErrorLocked(err)
		return nil, err
	}
	s.tools = tools
	s.toolsValid = true
	s.toolsFetchedAt = time.Now()
	return cloneTools(tools), nil
}

func (s *session) fetchToolsLocked(ctx context.Context) ([]ToolDef, error) {
	var tools []ToolDef
	seen := make(map[string]struct{})
	cursor := ""
	hasCursor := false

	for range maxToolsListPages {
		params := map[string]any{}
		if hasCursor {
			params["cursor"] = cursor
		}
		raw, err := s.tr.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		result, err := decodeToolsList(raw)
		if err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == nil {
			return tools, nil
		}
		nextCursor := *result.NextCursor
		if _, ok := seen[nextCursor]; ok {
			return nil, fmt.Errorf("server %q returned repeated tools/list cursor %q", s.name, nextCursor)
		}
		seen[nextCursor] = struct{}{}
		cursor = nextCursor
		hasCursor = true
	}

	return nil, fmt.Errorf("server %q tools/list exceeded %d pages", s.name, maxToolsListPages)
}

func (s *session) FindTool(ctx context.Context, name string) (*ToolDef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools, err := s.listToolsLocked(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range tools {
		if tools[i].Name == name {
			t := tools[i]
			return &t, nil
		}
	}
	s.invalidateToolsLocked()
	return nil, fmt.Errorf("tool %q not found on server %q", name, s.name)
}

func (s *session) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initLocked(ctx); err != nil {
		return "", err
	}
	if args == nil {
		args = map[string]any{}
	}
	raw, err := s.tr.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		if errors.Is(err, errTransportBroken) {
			s.resetSessionLocked()
		}
		return "", err
	}
	return extractToolContent(raw), nil
}

func (s *session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetSessionLocked()
	return s.tr.close()
}

func (s *session) handleInitErrorLocked(err error) {
	if errors.Is(err, errTransportBroken) {
		s.resetSessionLocked()
		return
	}
	_ = s.tr.close()
	s.resetSessionLocked()
}

func (s *session) handleToolsErrorLocked(err error) {
	s.invalidateToolsLocked()
	if errors.Is(err, errTransportBroken) {
		s.resetSessionLocked()
	}
}

func (s *session) invalidateToolsLocked() {
	s.toolsValid = false
	s.toolsFetchedAt = time.Time{}
}

func (s *session) resetSessionLocked() {
	s.ready = false
	s.tools = nil
	s.invalidateToolsLocked()
}

func cloneTools(in []ToolDef) []ToolDef {
	out := make([]ToolDef, len(in))
	copy(out, in)
	return out
}
