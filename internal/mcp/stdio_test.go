package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStdioRestartsAfterEOF(t *testing.T) {
	tr := newHelperStdioTransport(t, "exit-first")
	s := newSession("helper", tr)

	if _, err := s.ListTools(t.Context()); !errors.Is(err, errTransportBroken) {
		t.Fatalf("err = %v, want transport error", err)
	}
	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want echo after restart", tools)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStdioTimeoutRetiresWorker(t *testing.T) {
	tr := newHelperStdioTransport(t, "delay-first")
	tr.timeout = 250 * time.Millisecond
	s := newSession("helper", tr)

	if _, err := s.ListTools(t.Context()); !errors.Is(err, errTransportBroken) {
		t.Fatalf("err = %v, want transport error", err)
	}
	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want echo after timeout restart", tools)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func newHelperStdioTransport(t *testing.T, mode string) *stdioTransport {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(home, "helper-started")
	tr, err := newStdioTransport("helper", ServerConfig{
		Command: []string{os.Args[0]},
		Args:    []string{"-test.run=TestStdioHelperProcess", "--"},
		Env: map[string]string{
			"PHI_MCP_HELPER":        "1",
			"PHI_MCP_HELPER_MODE":   mode,
			"PHI_MCP_HELPER_MARKER": marker,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestStdioHelperProcess(_ *testing.T) {
	if os.Getenv("PHI_MCP_HELPER") != "1" {
		return
	}

	mode := os.Getenv("PHI_MCP_HELPER_MODE")
	marker := os.Getenv("PHI_MCP_HELPER_MARKER")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			panic(err)
		}
		switch request.Method {
		case "initialize":
			writeHelperResponse(request.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "0.1"},
			})
		case "notifications/initialized":
			continue
		case "tools/list":
			first := false
			if _, err := os.Stat(marker); os.IsNotExist(err) {
				if err := os.WriteFile(marker, []byte("started\n"), 0o600); err != nil {
					panic(err)
				}
				first = true
			}
			if first && mode == "exit-first" {
				return
			}
			if first && mode == "delay-first" {
				time.Sleep(time.Second)
			}
			writeHelperResponse(request.ID, map[string]any{
				"tools": []map[string]string{{"name": "echo"}},
			})
		default:
			writeHelperResponse(request.ID, map[string]any{})
		}
	}
}

func writeHelperResponse(id json.RawMessage, result any) {
	var idValue any
	if err := json.Unmarshal(id, &idValue); err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      idValue,
		"result":  result,
	}); err != nil {
		panic(err)
	}
}
