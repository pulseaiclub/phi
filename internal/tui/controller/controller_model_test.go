package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
)

func TestModelLifecycle(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:9")
	cwd := ctrl.cwd
	flush := func() {
		require.NoError(t, ctrl.engine.Session().Append(llm.Message{Role: llm.RoleUser, Content: "hello"}))
		require.NoError(t, ctrl.engine.Session().Append(llm.Message{Role: llm.RoleAssistant, Content: "done"}))
	}

	require.NoError(t, ctrl.SetModel("primary"))
	firstID := ctrl.SessionID()
	flush()
	require.NoError(t, ctrl.NewSession())
	secondID := ctrl.SessionID()
	require.NoError(t, ctrl.SetModel("secondary"))
	flush()

	_, err := ctrl.Resume(firstID)
	require.NoError(t, err)
	assert.Equal(t, "primary", ctrl.ModelName())
	_, err = ctrl.Resume(secondID)
	require.NoError(t, err)
	assert.Equal(t, "secondary", ctrl.ModelName())
	require.NoError(t, ctrl.NewSession())
	assert.Equal(t, "secondary", ctrl.ModelName())

	ctrl.Close()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	restarted, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	defer restarted.Close()
	assert.Equal(t, "primary", restarted.ModelName(), "startup must use the configured default")
	_, err = restarted.Resume(secondID)
	require.NoError(t, err)
	assert.Equal(t, "secondary", restarted.ModelName(), "resume must restore only the selected session's model")
}

func newTestController(t *testing.T, baseURL string) *EngineController {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "primary")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", baseURL)
	t.Setenv(loader.EnvExtensions, "off")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	config := fmt.Sprintf(`models:
  - name: primary
    api: OpenAI
    api_key: test-key
    base_url: %q
  - name: secondary
    api: OpenAI
    api_key: test-key
    base_url: %q
`, baseURL, baseURL)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(config), 0o600))
	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	return ctrl
}
