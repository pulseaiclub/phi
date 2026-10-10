package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/project"
)

// TestAskExtPickerRelaysTheChoice stands in for the shell: the controller asks
// the bus for a picker, the shell answers, and the extension-facing reply must
// carry the chosen row.
func TestAskExtPickerRelaysTheChoice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv(loader.EnvExtensions, "off")

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	require.NoError(t, proj.LoadConfig())

	ctrl, err := NewController(NewBus(nil), proj, cwd)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	ctrl.bus.Drain() // drop startup messages

	asked := make(chan ExtPickerMsg, 1)
	go func() {
		for range ctrl.bus.Chan() {
			for _, m := range ctrl.bus.Drain() {
				msg, ok := m.(ExtPickerMsg)
				if !ok {
					continue
				}
				asked <- msg
				msg.Reply <- ExtPickerReply{OK: true, ID: "beta"}
				return
			}
		}
	}()

	reply := ctrl.askExtPicker(ext.PickerRequest{
		Title: "Models",
		Items: []ext.PickerItem{{ID: "alpha", Label: "alpha"}, {ID: "beta", Label: "beta"}},
	})
	assert.Equal(t, ext.PickerReply{OK: true, ID: "beta"}, reply)

	msg := <-asked
	assert.Equal(t, "Models", msg.Request.Title)
	require.Len(t, msg.Request.Items, 2)
	assert.Equal(t, "beta", msg.Request.Items[1].ID)
}
