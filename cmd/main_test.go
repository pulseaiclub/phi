package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cli "github.com/pulseaiclub/pli"
)

// Commands that take no positionals must reject one instead of running their
// default action: `phi sessions show <id>` used to list sessions and exit 0.
func TestNoArgCommandsRejectPositionalArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"sessions", "show", "abc"}, `unknown command "show"`},
		{[]string{"sessions", "delete", "abc"}, `unknown command "delete"`},
		{[]string{"mcp", "list", "extra"}, `unexpected argument "extra"`},
		{[]string{"mcp", "doctor", "extra"}, `unexpected argument "extra"`},
		{[]string{"config", "extra"}, `unexpected argument "extra"`},
		{[]string{"update", "--check", "extra"}, `unexpected argument "extra"`},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			err := root().Dispatch(tc.args)
			var ue *cli.UsageError
			require.ErrorAs(t, err, &ue)
			assert.Contains(t, ue.Error(), tc.want)
		})
	}
}

func TestRootDispatchHelpAndUnknown(t *testing.T) {
	err := root().Dispatch([]string{"--help"})
	var he *cli.HelpError
	require.ErrorAs(t, err, &he)
	assert.Contains(t, he.Help, "usage: phi")
	assert.Contains(t, he.Help, "run")
	assert.Contains(t, he.Help, "sessions")
	assert.NotContains(t, he.Help, "plugin")

	err = root().Dispatch([]string{"bogus"})
	var ue *cli.UsageError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, err.Error(), `unknown command "bogus"`)
}
