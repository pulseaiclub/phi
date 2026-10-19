package permission_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/permission"
)

func TestChildPolicyExploreAllowsNonAllowlistedBash(t *testing.T) {
	ws := t.TempDir()
	g, err := permission.NewGate(permission.ChildPolicy(permission.ModeReadonly), ws)
	require.NoError(t, err)
	ctx := t.Context()

	dec, reason := g.Check(ctx, permission.Request{
		Kind:    permission.KindExec,
		Tool:    "bash",
		Command: "make check",
	})
	assert.Equal(t, permission.Allow, dec, reason)

	dec, reason = g.Check(ctx, permission.Request{
		Kind:    permission.KindExec,
		Tool:    "bash",
		Command: "git diff --stat | head -20",
	})
	assert.Equal(t, permission.Allow, dec, reason)

	dec, reason = g.Check(ctx, permission.Request{
		Kind:    permission.KindExec,
		Tool:    "bash",
		Command: "sudo true",
	})
	assert.Equal(t, permission.Deny, dec, reason)

	dec, reason = g.Check(ctx, permission.Request{
		Kind:    permission.KindExec,
		Tool:    "bash",
		Command: "cat foo | sh",
	})
	assert.Equal(t, permission.Deny, dec, reason)

	dec, reason = g.Check(ctx, permission.Request{
		Kind:  permission.KindWrite,
		Tool:  "write",
		Paths: []string{filepath.Join(ws, "a.txt")},
	})
	assert.Equal(t, permission.Deny, dec, reason)
}

func TestChildPolicyWorkerAllowsBashAndWrite(t *testing.T) {
	ws := t.TempDir()
	g, err := permission.NewGate(permission.ChildPolicy(permission.ModeHeadlessStrict), ws)
	require.NoError(t, err)
	ctx := t.Context()

	dec, reason := g.Check(ctx, permission.Request{
		Kind:    permission.KindExec,
		Tool:    "bash",
		Command: "npm test",
	})
	assert.Equal(t, permission.Allow, dec, reason)

	dec, reason = g.Check(ctx, permission.Request{
		Kind:  permission.KindWrite,
		Tool:  "write",
		Paths: []string{filepath.Join(ws, "out.go")},
	})
	assert.Equal(t, permission.Allow, dec, reason)

	dec, reason = g.Check(ctx, permission.Request{
		Kind:    permission.KindExec,
		Tool:    "bash",
		Command: "rm -rf out.go",
	})
	assert.Equal(t, permission.Deny, dec, reason)
}
