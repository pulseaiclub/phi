package permission

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnknownToolCannotSelectPermissionAction(t *testing.T) {
	root := t.TempDir()
	for _, mode := range []Mode{ModeInteractive, ModeReadonly, ModeAutopilot, ModeHeadlessStrict} {
		t.Run(string(mode), func(t *testing.T) {
			policy := DefaultPolicy()
			policy.Mode = mode
			gate, err := NewGate(policy, root)
			require.NoError(t, err)
			for _, tool := range []string{"agent_control", "custom_tool"} {
				req, err := ExtractAt(tool, json.RawMessage(`{}`), root)
				require.NoError(t, err)
				require.Equal(t, ActionUnknown, req.Action)
				require.Equal(t, tool, Summarize(req))
				want := Ask
				wantReason := fmt.Sprintf("unknown action %q requires approval", tool)
				if mode != ModeInteractive {
					want = Deny
					wantReason = string(mode) + " mode: " + wantReason
				}
				dec, reason := gate.Check(t.Context(), req)
				require.Equal(t, want, dec)
				require.Equal(t, wantReason, reason)
			}
		})
	}
}

func TestExtractBash(t *testing.T) {
	req, err := Extract("bash", json.RawMessage(`{"command":"git status"}`))
	require.NoError(t, err)
	require.Equal(t, ActionBash, req.Action)
	require.Equal(t, "git status", req.Command)
}

func TestExtractWritePath(t *testing.T) {
	req, err := Extract("write", json.RawMessage(`{"path":"out.txt","content":"x"}`))
	require.NoError(t, err)
	require.Equal(t, ActionWrite, req.Action)
	require.Len(t, req.Paths, 1)
	require.True(t, filepath.IsAbs(req.Paths[0]))
}

func TestExtractAtUsesExplicitCwd(t *testing.T) {
	root := t.TempDir()
	req, err := ExtractAt("write", json.RawMessage(`{"path":"out.txt","content":"x"}`), root)
	require.NoError(t, err)
	want := filepath.Join(root, "out.txt")
	require.Len(t, req.Paths, 1)
	require.Equal(t, want, req.Paths[0])
}

func TestExtractEditPayloadTargets(t *testing.T) {
	req, err := Extract("edit", json.RawMessage(
		`{"payload":"*** SM:EDIT a.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n*** SM:EDIT b.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
	))
	require.NoError(t, err)
	require.Equal(t, ActionEdit, req.Action)
	require.Len(t, req.Paths, 2)
	require.True(t, strings.HasSuffix(req.Paths[0], "a.go"))
	require.True(t, strings.HasSuffix(req.Paths[1], "b.go"))
}

func TestExtractEditWithoutTargetFails(t *testing.T) {
	_, err := Extract("edit", json.RawMessage(`{"payload":"*** SM:FIND\nx\n*** SM:PUT\ny\n"}`))
	require.Error(t, err)
}
