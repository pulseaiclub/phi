package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

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

	comp := &stubSettingsComposer{}
	s := &SettingsCommands{Ctrl: ctrl, Bus: bus, Composer: comp}
	s.setModel("big-model")

	assert.Equal(t, "big-model", comp.name)
	assert.Equal(t, 1_000_000, ctrl.ContextWindow())

	s.setModel("small-model")
	require.True(t, s.saveModel("big-model"))
	assert.Equal(t, "big-model", comp.name)
}

// Every settings picker marks the live value with the shared dot, so the
// Register wiring (which reads Ctrl state) is exercised end to end.
func TestSettingsPaletteMarksCurrentValues(t *testing.T) {
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
		"    default: true\n" +
		"  - name: big-model\n" +
		"    api_key: k\n" +
		"    base_url: http://127.0.0.1:9\n"
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "config.yaml"), []byte(cfg), 0o600))

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj, cwd)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)

	s := &SettingsCommands{Ctrl: ctrl, Bus: bus, ModelNames: []string{"small-model", "big-model"}}
	r := NewCommandRegistry()
	s.Register(r)
	byID := map[string]palette.PaletteCommand{}
	for _, c := range r.BuildPalette(nil) {
		byID[c.ID] = c
	}

	model := byID["settings-model"]
	require.Len(t, model.Submenu, 2)
	require.Equal(t, "small-model", ctrl.ModelName())
	assert.Equal(t, chrome.CurrentMarker(true)+"small-model", model.Submenu[0].Verb)
	assert.Equal(t, chrome.CurrentMarker(false)+"big-model", model.Submenu[1].Verb)

	// The startup theme is marked before any palette switch.
	theme := byID["settings-theme"]
	require.NotEmpty(t, theme.Submenu)
	assert.Equal(t, chrome.CurrentMarker(true)+"Dark (builtin)", theme.Submenu[0].Verb)

	// The TUI starts in bypass, so the "never ask" row is current.
	perm := byID["settings-permissions"]
	require.Len(t, perm.Submenu, 2)
	assert.Equal(t, chrome.CurrentMarker(true)+"off — never ask, allow everything", perm.Submenu[0].Verb)

	// No per-role override configured, so each role inherits the parent.
	agents := byID["settings-agents"]
	require.Len(t, agents.Submenu, 3)
	explore := agents.Submenu[2].Submenu[0]
	require.NotEmpty(t, explore.Submenu)
	assert.Equal(t, chrome.CurrentMarker(true)+"(inherit parent)", explore.Submenu[0].Verb)
}
