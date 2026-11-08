package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// forkEditor wires the shell the way cmd does, on a live controller over a
// throwaway project, so /fork runs the whole path: dispatch → controller fork →
// transcript refresh.
func forkEditor(t *testing.T) *Editor {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL", "PHI_THINK_LEVEL"} {
		t.Setenv(key, "")
	}
	t.Setenv(loader.EnvExtensions, "off")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	config := `models:
  - name: primary
    default: true
    api: OpenAI
    api_key: test-key
    base_url: "http://127.0.0.1:9"
`
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(config), 0o600))

	dir := t.TempDir()
	proj, err := project.Discover(dir)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)

	return NewEditor(app.NewApp(nil), bus, ctrl, nil, components.DefaultTheme(), dir, "primary", nil, nil)
}

func TestForkSlashSwitchesToANewSession(t *testing.T) {
	e := forkEditor(t)
	srcID, srcFile := e.ctrl.SessionID(), e.ctrl.SessionFile()

	require.True(t, e.commands.DispatchSlash("/fork", commands.NewContext(e.bus, nil)))
	e.drainBus()

	assert.NotEqual(t, srcID, e.ctrl.SessionID(), "/fork must open the session it created")
	assert.NotEqual(t, srcFile, e.ctrl.SessionFile())
	assert.Empty(t, e.transcript.Snapshot().Messages, "a fresh session forks empty")
}

func TestForkSlashRejectsArguments(t *testing.T) {
	e := forkEditor(t)
	srcID := e.ctrl.SessionID()

	require.True(t, e.commands.DispatchSlash("/fork now", commands.NewContext(e.bus, nil)))
	e.drainBus()

	assert.Equal(t, srcID, e.ctrl.SessionID(), "arguments must not fork silently")
}
