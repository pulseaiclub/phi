package permission

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// An undeclared tool must not be able to pick its own permission tier: the
// request stays KindUnknown, which asks (and folds to deny where Ask is
// unavailable).
func TestUnknownActionAsksInEveryMode(t *testing.T) {
	root := t.TempDir()
	for _, mode := range []Mode{ModeInteractive, ModeReadonly, ModeAutopilot, ModeHeadlessStrict} {
		t.Run(string(mode), func(t *testing.T) {
			policy := DefaultPolicy()
			policy.Mode = mode
			gate, err := NewGate(policy, root)
			require.NoError(t, err)
			for _, tool := range []string{"agent_control", "custom_tool"} {
				req := Request{Tool: tool, Kind: KindUnknown}
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
