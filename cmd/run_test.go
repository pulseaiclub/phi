package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestRunOptionsFromFlags(t *testing.T) {
	f, _, err := runCommand.Parse([]string{
		"-p", "do the thing",
		"--jsonl",
		"--timeout", "10m",
		"--session", "abc123",
		"--tools", "grep, read,grep",
		"--model", "fast",
	})
	require.NoError(t, err)
	opts, err := runOptionsFromFlags(nil, f)
	require.NoError(t, err)
	assert.Equal(t, "do the thing", opts.prompt)
	assert.True(t, opts.jsonl)
	assert.Equal(t, 10*time.Minute, opts.timeout)
	assert.Equal(t, "abc123", opts.session)
	assert.Equal(t, "grep, read,grep", opts.builtinTools)
	assert.Equal(t, "fast", opts.model)
	// Selection is order-stable and deduplicated at selection time.
	selected, err := selectBuiltinTools(opts.builtinTools, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"read", "grep"}, toolNames(selected))
	assert.False(t, opts.continueLast)
}

func TestRunOptionsFromFlagsEqualsForms(t *testing.T) {
	f, _, err := runCommand.Parse([]string{
		"--prompt=hi",
		"--timeout=1500ms",
		"--session-dir=/tmp/sess",
		"--continue-last",
		"--tools=write,bash",
		"--model=smart",
	})
	require.NoError(t, err)
	opts, err := runOptionsFromFlags(nil, f)
	require.NoError(t, err)
	assert.Equal(t, "hi", opts.prompt)
	assert.Equal(t, 1500*time.Millisecond, opts.timeout)
	assert.Equal(t, "/tmp/sess", opts.sessionDir)
	assert.True(t, opts.continueLast)
	assert.Equal(t, "write,bash", opts.builtinTools)
	assert.Equal(t, "smart", opts.model)
	selected, err := selectBuiltinTools(opts.builtinTools, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "write"}, toolNames(selected))
}

func TestRunCommandParseErrors(t *testing.T) {
	cases := [][]string{
		{"--prompt"},             // missing value
		{"--timeout"},            // missing value
		{"--timeout", "abc"},     // invalid duration
		{"--timeout", "0"},       // non-positive
		{"--timeout", "-1s"},     // non-positive
		{"--tools"},              // missing value
		{"--tools="},             // empty list
		{"--tools", "read,,ls"},  // empty name
		{"--tools", "read,nope"}, // unknown name
		{"--bogus", "x"},         // unknown flag
	}
	for _, args := range cases {
		_, _, err := runCommand.Parse(args)
		assert.Error(t, err, "args %v should error", args)
	}
}

func TestRunOptionsRequiresPrompt(t *testing.T) {
	f, _, err := runCommand.Parse([]string{"--jsonl"})
	require.NoError(t, err)
	_, err = runOptionsFromFlags(nil, f)
	var ue *cli.UsageError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, err.Error(), "prompt is required")
}

func TestRunOptionsMutualExclusion(t *testing.T) {
	f, _, err := runCommand.Parse([]string{"-p", "hi", "--continue-last", "--session", "x"})
	require.NoError(t, err)
	_, err = runOptionsFromFlags(nil, f)
	var ue *cli.UsageError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestRunOptionsRejectsPositionalArgs(t *testing.T) {
	// An unquoted prompt: the shell hands "the" and "bug" over as positionals.
	f, pos, err := runCommand.Parse([]string{"-p", "fix", "the", "bug"})
	require.NoError(t, err)
	require.Equal(t, []string{"the", "bug"}, pos)
	_, err = runOptionsFromFlags(pos, f)
	var ue *cli.UsageError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, err.Error(), `unexpected argument "the"`)
}

func TestRunOptionsLeavesBuiltinToolsUnsetByDefault(t *testing.T) {
	f, _, err := runCommand.Parse([]string{"-p", "hi"})
	require.NoError(t, err)
	opts, err := runOptionsFromFlags(nil, f)
	require.NoError(t, err)
	assert.Empty(t, opts.builtinTools)
	assert.Empty(t, opts.model)
}

func toolNames(list []tooldef.Tool) []string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Definition.Name)
	}
	return names
}

func TestRunCommandHelp(t *testing.T) {
	err := root().Dispatch([]string{"run", "--help"})
	var he *cli.HelpError
	require.ErrorAs(t, err, &he)
	assert.Contains(t, he.Help, "usage: phi run")
	assert.Contains(t, he.Help, "--prompt")
	assert.Contains(t, he.Help, "--tools")
	assert.Contains(t, he.Help, "--model")
	assert.Contains(t, he.Help, "--model > config model > PHI_MODEL")
}
