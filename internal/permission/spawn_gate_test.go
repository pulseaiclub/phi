package permission

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The workdir a spawn inherits becomes the child's workspace, so a session
// rooted outside the gate's workspace must ask (interactive) or deny.
func TestSpawnChecksInheritedDirectory(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	for _, mode := range []Mode{ModeInteractive, ModeAutopilot, ModeHeadlessStrict, ModeReadonly} {
		t.Run(string(mode), func(t *testing.T) {
			policy := DefaultPolicy()
			policy.Mode = mode
			gate, err := NewGate(policy, ws)
			require.NoError(t, err)

			dec, reason := gate.Check(t.Context(), Request{
				Tool:  "agent_spawn",
				Kind:  KindDelegate,
				Paths: []string{outside},
			})
			expected := Deny
			if mode == ModeInteractive {
				expected = Ask
			}
			require.Equal(t, expected, dec, reason)

			dec, reason = gate.Check(t.Context(), Request{Tool: "agent_control", Kind: KindControl})
			require.Equal(t, Allow, dec, reason)
		})
	}
}
