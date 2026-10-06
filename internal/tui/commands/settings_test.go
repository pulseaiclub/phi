package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type stubSettingsFooter struct{ window int }

func (s *stubSettingsFooter) SetContextWindow(window int) { s.window = window }

type stubSettingsComposer struct{ name string }

func (s *stubSettingsComposer) SetModelLabel(name, _ string) { s.name = name }

// Switching models must move the footer's context window too: it starts as the
// default model's window, so a 1M model would otherwise report fill against 192k.
func TestSetModelUpdatesFooterContextWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "")
	t.Setenv("PHI_API_KEY", "")
	t.Setenv("PHI_BASE_URL", "")

	confDir := filepath.Join(home, ".phi")
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	cfg := "" +
		"models:\n" +
		"  - name: small-model\n" +
		"    api_key: k\n" +
		"    base_url: http://127.0.0.1:9\n" +
		"    context_window: 192000\n" +
		"    default: true\n" +
		"  - name: big-model\n" +
		"    api_key: k\n" +
		"    base_url: http://127.0.0.1:9\n" +
		"    context_window: 1000000\n"
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "config.yaml"), []byte(cfg), 0o600))

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj, cwd)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	require.Equal(t, 192000, ctrl.ContextWindow())

	foot := &stubSettingsFooter{}
	comp := &stubSettingsComposer{}
	s := &SettingsCommands{Ctrl: ctrl, Bus: bus, Composer: comp, Footer: foot}
	s.setModel("big-model")

	assert.Equal(t, "big-model", comp.name)
	assert.Equal(t, 1_000_000, foot.window)
	assert.Equal(t, 1_000_000, ctrl.ContextWindow())
}
