package urls

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm/skills"
)

func TestSkillRouter(t *testing.T) {
	root := t.TempDir()
	instruction := filepath.Join(root, "SKILL.md")
	asset := filepath.Join(root, "a b.txt")
	require.NoError(t, os.WriteFile(instruction, []byte("private"), 0o000))
	require.NoError(t, os.WriteFile(asset, []byte("asset"), 0o600))
	s := &skills.Skill{Name: "plugin:name", Path: root, SkillFilePath: instruction}
	list := []*skills.Skill{nil, s}
	router := NewSkillRouter(list)
	s.Name, s.Path, s.SkillFilePath = "changed", t.TempDir(), "elsewhere"
	list[1] = nil
	for input, want := range map[string]string{
		"skill://plugin:name":                        instruction,
		"skills://plugin:name/":                      instruction,
		"SKILL://plugin%3Aname/SKILL.md?x=1#section": instruction,
		"skill://plugin:name/a%20b.txt":              asset,
	} {
		t.Run(input, func(t *testing.T) {
			got, err := router.Locate(t.Context(), input)
			require.NoError(t, err)
			real, err := filepath.EvalSymlinks(want)
			require.NoError(t, err)
			assert.Equal(t, real, got)
		})
	}
	for _, input := range []string{
		"mcp://plugin:name", "skill://unknown", "skill://", "relative", "skill:plugin:name", "skill://plugin:name/missing",
		"skill://plugin:name/../outside", "skill://plugin:name/%2e%2e/outside", "skill://plugin:name/sub/../../outside",
		"skill://plugin:name/%2fetc/passwd", "skill://plugin:name//etc/passwd", "skill://plugin:name/%5c..%5coutside", "skill://plugin:name/%00", "skill://plugin:name/C:/file",
	} {
		t.Run(input, func(t *testing.T) { _, err := router.Locate(t.Context(), input); require.Error(t, err) })
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := router.Locate(ctx, "skill://plugin:name")
	require.ErrorIs(t, err, context.Canceled)
}

func TestSkillSymlinkContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), nil, 0o600))
	require.NoError(t, os.Symlink("inside", filepath.Join(root, "safe")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "SKILL.md")))
	router := NewSkillRouter([]*skills.Skill{{Name: "demo", Path: root}})
	_, err := router.Locate(t.Context(), "skill://demo/safe")
	require.NoError(t, err)
	for _, input := range []string{"skill://demo", "skill://demo/escape/secret", "skill://demo/escape/missing"} {
		_, err := router.Locate(t.Context(), input)
		require.Error(t, err)
	}
}

type testHandler struct{}

func (testHandler) Locate(_ context.Context, u InternalURL) (string, error) { return u.Host, nil }

func TestRouterCopiesHandlers(t *testing.T) {
	handlers := map[string]ProtocolHandler{"custom": testHandler{}}
	router := NewRouter(handlers)
	delete(handlers, "custom")
	got, err := router.Locate(t.Context(), "custom://target")
	require.NoError(t, err)
	assert.Equal(t, "target", got)
	var empty Router
	_, err = empty.Locate(t.Context(), "custom://target")
	require.ErrorContains(t, err, "unknown internal URL scheme")
}
