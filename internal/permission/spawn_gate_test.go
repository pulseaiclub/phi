package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpawnChecksInheritedDirectory(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	for _, mode := range []Mode{ModeInteractive, ModeAutopilot, ModeHeadlessStrict, ModeReadonly} {
		t.Run(string(mode), func(t *testing.T) {
			policy := DefaultPolicy()
			policy.Mode = mode
			gate, err := NewGate(policy, ws)
			require.NoError(t, err)
			for _, raw := range []string{`{"prompt":"x"}`, `{"prompt":"x","workdir":""}`, `{"prompt":"x","workdir":"  "}`} {
				req, err := ExtractAt("agent_spawn", json.RawMessage(raw), outside)
				require.NoError(t, err)
				dec, reason := gate.Check(t.Context(), req)
				expected := Deny
				if mode == ModeInteractive {
					expected = Ask
				}
				require.Equal(t, expected, dec, reason)
			}
			for _, tool := range []string{"agent_wait", "agent_cancel"} {
				req, err := ExtractAt(tool, json.RawMessage(`{"job_id":"j"}`), outside)
				require.NoError(t, err)
				dec, reason := gate.Check(t.Context(), req)
				require.Equal(t, Allow, dec, reason)
			}
		})
	}
}

func TestAgentGateUsesAction(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	for _, mode := range []Mode{ModeInteractive, ModeAutopilot, ModeHeadlessStrict, ModeReadonly} {
		t.Run(string(mode), func(t *testing.T) {
			policy := DefaultPolicy()
			policy.Mode = mode
			gate, err := NewGate(policy, ws)
			require.NoError(t, err)
			for _, tool := range []string{"", "agent_wait", "agent_cancel", "agent_spawn", "custom_tool"} {
				t.Run(tool, func(t *testing.T) {
					for _, path := range []string{ws, outside} {
						want := Allow
						if mode == ModeReadonly {
							want = Deny
						} else if path == outside {
							want = Ask
							if mode != ModeInteractive {
								want = Deny
							}
						}
						dec, reason := gate.Check(t.Context(), Request{
							Action: ActionAgentSpawn, Tool: tool, Paths: []string{path},
						})
						require.Equal(t, want, dec, reason)
					}
					dec, reason := gate.Check(t.Context(), Request{Action: ActionAgentControl, Tool: tool})
					require.Equal(t, Allow, dec, reason)
				})
			}
		})
	}
}

func TestSpawnRejectsSymlinkAndInvalidDirectories(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "escape")))
	policy := DefaultPolicy()
	policy.Mode = ModeHeadlessStrict
	gate, err := NewGate(policy, ws)
	require.NoError(t, err)
	for _, paths := range [][]string{nil, {"mcp://server/tools/x"}, {filepath.Join(ws, "escape")}, {ws, outside}} {
		dec, reason := gate.Check(t.Context(), Request{Action: ActionAgentSpawn, Paths: paths})
		require.Equal(t, Deny, dec, reason)
	}
}
