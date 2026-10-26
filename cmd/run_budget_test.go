package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHeadlessRoundBudget(t *testing.T) {
	// Each subprocess gets a fresh default-project singleton and isolated HOME.
	if os.Getenv("PHI_TEST_RUN_BUDGET") == "1" {
		args := []string{"run", "-p", "read the config repeatedly", "--tools", "read", "--timeout", "10s"}
		args = append(args, strings.Fields(os.Getenv("PHI_TEST_RUN_FLAGS"))...)
		var exit *exitError
		require.ErrorAs(t, root().Dispatch(args), &exit)
		require.Equal(t, ExitMaxRounds, exit.code)
		return
	}

	for _, tc := range []struct {
		name  string
		flags string
		want  int32
	}{
		{name: "config default", want: 3},
		{name: "lower CLI override", flags: "--max-rounds 1", want: 2},
		{name: "higher CLI override", flags: "--max-rounds 3", want: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			configPath := filepath.Join(home, ".phi", "config.yaml")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				id := requests.Add(1)
				args, err := json.Marshal(map[string]string{"path": configPath})
				assert.NoError(t, err)
				chunk := map[string]any{"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{"tool_calls": []any{map[string]any{
						"index": 0, "id": fmt.Sprintf("call_%d", id), "type": "function",
						"function": map[string]string{"name": "read", "arguments": string(args)},
					}}},
					"finish_reason": "tool_calls",
				}}}
				data, err := json.Marshal(chunk)
				assert.NoError(t, err)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			}))
			defer server.Close()

			binDir := filepath.Join(home, ".phi", "bin")
			require.NoError(t, os.MkdirAll(binDir, 0o755))
			for _, name := range []string{"fd", "rg"} {
				require.NoError(t, os.WriteFile(filepath.Join(binDir, executableName(name)), []byte("x"), 0o755))
			}
			config := fmt.Sprintf(`models:
  - name: fake
    api_key: test
    base_url: %s
max_rounds: 2
agents:
  enabled: false
`, server.URL)
			require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
			for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL", "PHI_SKILL_PATH", "PHI_THINK_LEVEL"} {
				t.Setenv(key, "")
			}
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PHI_TEST_RUN_BUDGET", "1")
			t.Setenv("PHI_TEST_RUN_FLAGS", tc.flags)
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRunHeadlessRoundBudget$")
			cmd.Dir = home
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Equal(t, tc.want, requests.Load(), "one final request exceeds the tool-round budget")
		})
	}
}
