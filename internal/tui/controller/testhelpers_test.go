package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/project"
)

func setupThinkingTest(t *testing.T, baseURL string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL", "PHI_THINK_LEVEL"} {
		t.Setenv(key, "")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	config := fmt.Sprintf(`models:
  - name: primary
    default: true
    api: openai
    api_key: test-key
    base_url: %q
    think_level: off
  - name: secondary
    api: openai
    api_key: test-key
    base_url: %q
    think_level: high
`, baseURL, baseURL)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(config), 0o600))
	return t.TempDir()
}

func newThinkingTestController(t *testing.T, cwd string) *EngineController {
	t.Helper()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	return ctrl
}
