package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type sessionTransport struct {
	calls         []string
	listParams    []map[string]any
	listResponses []json.RawMessage
	callErrors    map[string][]error
	closed        int
}

func (f *sessionTransport) call(_ context.Context, method string, params map[string]any) (json.RawMessage, error) {
	f.calls = append(f.calls, method)
	if errorsForMethod := f.callErrors[method]; len(errorsForMethod) > 0 {
		err := errorsForMethod[0]
		f.callErrors[method] = errorsForMethod[1:]
		return nil, err
	}
	if method == "tools/list" {
		f.listParams = append(f.listParams, params)
		if len(f.listResponses) > 0 {
			raw := f.listResponses[0]
			f.listResponses = f.listResponses[1:]
			return raw, nil
		}
		return rawJSON(`{"tools":[]}`), nil
	}
	return rawJSON(`{}`), nil
}

func (*sessionTransport) notify(context.Context, string, map[string]any) error {
	return nil
}

func (f *sessionTransport) close() error {
	f.closed++
	return nil
}

func TestSessionCachesEmptyToolsList(t *testing.T) {
	f := &sessionTransport{
		callErrors:    map[string][]error{},
		listResponses: []json.RawMessage{rawJSON(`{"tools":[]}`)},
	}
	s := newSession("fake", f)

	for range 2 {
		tools, err := s.ListTools(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(tools) != 0 {
			t.Fatalf("tools = %v, want empty", tools)
		}
	}
	if got := countMethod(f.calls, "tools/list"); got != 1 {
		t.Fatalf("tools/list calls = %d, want 1", got)
	}
}

func TestSessionRefreshesExpiredTools(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{},
		listResponses: []json.RawMessage{
			toolPage("old", ""),
			toolPage("new", ""),
		},
	}
	s := newSession("fake", f)
	if _, err := s.ListTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.toolsFetchedAt = time.Now().Add(-toolsCacheTTL)

	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "new" {
		t.Fatalf("tools = %+v, want new", tools)
	}
	if got := countMethod(f.calls, "tools/list"); got != 2 {
		t.Fatalf("tools/list calls = %d, want 2", got)
	}
}

func TestSessionRefreshFailureInvalidatesCache(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{},
		listResponses: []json.RawMessage{
			toolPage("old", ""),
			toolPage("new", ""),
		},
	}
	s := newSession("fake", f)
	if _, err := s.ListTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.callErrors["tools/list"] = []error{errors.New("temporary list failure")}
	if _, err := s.RefreshTools(t.Context()); err == nil {
		t.Fatal("expected refresh error")
	}
	if s.toolsValid {
		t.Fatal("expected failed refresh to invalidate cache")
	}
	if len(s.tools) != 1 || s.tools[0].Name != "old" {
		t.Fatalf("tools = %+v, want preserved old snapshot", s.tools)
	}

	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "new" {
		t.Fatalf("tools = %+v, want new", tools)
	}
}

func TestSessionPaginatesTools(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{},
		listResponses: []json.RawMessage{
			toolPage("first", "cursor-1"),
			toolPage("second", ""),
		},
	}
	s := newSession("fake", f)

	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "first" || tools[1].Name != "second" {
		t.Fatalf("tools = %+v, want both pages", tools)
	}
	if got := f.listParams[1]["cursor"]; got != "cursor-1" {
		t.Fatalf("second cursor = %v, want cursor-1", got)
	}
}

func TestSessionPaginatesWithEmptyCursor(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{},
		listResponses: []json.RawMessage{
			rawJSON(`{"tools":[{"name":"first"}],"nextCursor":""}`),
			toolPage("second", ""),
		},
	}
	s := newSession("fake", f)

	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "first" || tools[1].Name != "second" {
		t.Fatalf("tools = %+v, want both pages", tools)
	}
	cursor, ok := f.listParams[1]["cursor"]
	if !ok || cursor != "" {
		t.Fatalf("second cursor = %v (present %v), want present empty string", cursor, ok)
	}
}

func TestSessionRejectsRepeatedToolsCursor(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{},
		listResponses: []json.RawMessage{
			toolPage("first", "cursor-1"),
			toolPage("again", "cursor-1"),
		},
	}
	s := newSession("fake", f)

	_, err := s.ListTools(t.Context())
	if err == nil || !strings.Contains(err.Error(), "repeated tools/list cursor") {
		t.Fatalf("err = %v, want repeated cursor error", err)
	}
	if s.toolsValid {
		t.Fatal("expected repeated cursor to invalidate cache")
	}
}

func TestSessionLimitsToolsListPages(t *testing.T) {
	f := &sessionTransport{callErrors: map[string][]error{}}
	for i := range maxToolsListPages {
		f.listResponses = append(f.listResponses, toolPage(fmt.Sprintf("tool-%d", i), fmt.Sprintf("cursor-%d", i)))
	}
	s := newSession("fake", f)

	_, err := s.ListTools(t.Context())
	if err == nil || !strings.Contains(err.Error(), "tools/list exceeded") {
		t.Fatalf("err = %v, want page limit error", err)
	}
	if s.toolsValid {
		t.Fatal("expected page limit to invalidate cache")
	}
}

func TestSessionFindToolMissInvalidatesCache(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{},
		listResponses: []json.RawMessage{
			toolPage("old", ""),
			toolPage("new", ""),
		},
	}
	s := newSession("fake", f)
	if _, err := s.ListTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindTool(t.Context(), "missing"); err == nil {
		t.Fatal("expected missing tool error")
	}
	if s.toolsValid {
		t.Fatal("expected miss to invalidate cache")
	}

	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "new" {
		t.Fatalf("tools = %+v, want new", tools)
	}
}

func TestSessionTransportErrorResetsSession(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{
			"tools/list": {fmt.Errorf("read failed: %w", errTransportBroken)},
		},
		listResponses: []json.RawMessage{toolPage("new", "")},
	}
	s := newSession("fake", f)
	if _, err := s.ListTools(t.Context()); !errors.Is(err, errTransportBroken) {
		t.Fatalf("err = %v, want transport error", err)
	}
	if s.ready || s.toolsValid {
		t.Fatalf("session state after transport error: ready=%v valid=%v", s.ready, s.toolsValid)
	}
	if f.closed != 0 {
		t.Fatalf("session closed transport %d times, want 0", f.closed)
	}

	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "new" {
		t.Fatalf("tools = %+v, want new", tools)
	}
	if got := countMethod(f.calls, "initialize"); got != 2 {
		t.Fatalf("initialize calls = %d, want 2", got)
	}
}

func TestSessionJSONRPCErrorKeepsState(t *testing.T) {
	f := &sessionTransport{
		callErrors: map[string][]error{
			"tools/call": {errors.New("mcp tools/call: [1] rejected")},
		},
		listResponses: []json.RawMessage{toolPage("echo", "")},
	}
	s := newSession("fake", f)
	if _, err := s.ListTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CallTool(t.Context(), "echo", nil); err == nil {
		t.Fatal("expected call error")
	}
	if !s.ready || !s.toolsValid {
		t.Fatalf("session state after JSON-RPC error: ready=%v valid=%v", s.ready, s.toolsValid)
	}
}

func rawJSON(value string) json.RawMessage {
	return json.RawMessage(value)
}

func toolPage(name, next string) json.RawMessage {
	payload := map[string]any{
		"tools": []map[string]string{{"name": name}},
	}
	if next != "" {
		payload["nextCursor"] = next
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return raw
}

func countMethod(calls []string, method string) int {
	count := 0
	for _, call := range calls {
		if call == method {
			count++
		}
	}
	return count
}
