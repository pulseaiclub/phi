package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
)

func TestModelLifecycle(t *testing.T) {
	ctrl := newTestController(t)
	cwd := ctrl.cwd
	flush := func() {
		require.NoError(t, ctrl.session.Append(llm.Message{Role: llm.RoleUser, Content: "hello"}))
		require.NoError(t, ctrl.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "done"}))
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

func TestAppendUserBashAttributesOutputToActiveSession(t *testing.T) {
	ctrl := newTestController(t)
	oldID := ctrl.SessionID()

	require.NoError(t, ctrl.NewSession())
	newID := ctrl.SessionID()
	require.NotEqual(t, oldID, newID)

	require.NoError(t, ctrl.AppendUserBash(oldID, "printf old", "old", 0, false))
	assert.Empty(t, ctrl.ReplaySnapshot().Messages, "stale bash output must not enter the new session")

	require.NoError(t, ctrl.AppendUserBash(newID, "printf new", "new", 0, false))
	snapshot := ctrl.ReplaySnapshot()
	require.Len(t, snapshot.Messages, 1)
	assert.Equal(t, "new", snapshot.Tools[snapshot.Messages[0].ID].Output)
}

func TestUnlistedModelUsesPrimaryConnection(t *testing.T) {
	const baseURL = "http://127.0.0.1:9"
	ctrl := newTestController(t)
	require.NoError(t, ctrl.SetModel("unlisted"))
	cfg := ctrl.modelSnapshot()
	assert.Equal(t, "unlisted", cfg.Name)
	assert.Equal(t, baseURL, cfg.BaseURL)
	assert.Equal(t, "test-key", cfg.APIKey)
}

func newTestController(t *testing.T) *EngineController {
	t.Helper()
	const baseURL = "http://127.0.0.1:9"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "primary")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", baseURL)
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
