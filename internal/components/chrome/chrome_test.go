package chrome_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/status"
)

func TestHintDialect(t *testing.T) {
	assert.Equal(t, " ↑↓ move · ⏎ select · esc close ", chrome.ListHint("select"))
	assert.Equal(t, " ↑↓ move · ⏎ resume · esc close ", chrome.ListHint("resume"))
	assert.Equal(t, " ↑↓ move · ⏎ resume · ctrl+x delete · esc close ", chrome.ListHintExtra("resume", "ctrl+x delete"))
	assert.Equal(t, " ⏎ select · esc close ", chrome.ListHintShort("select"))
	assert.Equal(t, "↑↓ move · Enter select · Esc cancel", chrome.AskHint("↑↓ move", "select", "cancel"))
	assert.Equal(t, "↑↓ move · Enter select · Esc stop", chrome.AskHint("↑↓ move", "select", "stop"))
	assert.Equal(t, "↑↓ move · Enter confirm · Y yes · N/Esc cancel", chrome.ConfirmHint())
	assert.Equal(t, "Enter send · Esc cancel", chrome.FeedbackHint())
}

func TestToolIconRejectedUsesDestructive(t *testing.T) {
	th := components.DefaultTheme()
	icon, st := chrome.ToolIcon(status.ToolRejected, th, nil)
	assert.Equal(t, chrome.Stop, icon)
	assert.Equal(t, th.Destructive.Fg, st.Fg)

	icon, st = chrome.ToolIcon(status.ToolCancelled, th, nil)
	assert.Equal(t, chrome.Stop, icon)
	assert.Equal(t, th.Muted.Fg, st.Fg)
}

func TestCurrentMarker(t *testing.T) {
	assert.Equal(t, chrome.DotOn+" ", chrome.CurrentMarker(true))
	assert.Equal(t, "  ", chrome.CurrentMarker(false))
}

func TestExpandArrow(t *testing.T) {
	assert.Equal(t, chrome.Expand, chrome.ExpandArrow(false))
	assert.Equal(t, chrome.Collapse, chrome.ExpandArrow(true))
}

func TestOptionLineSelected(t *testing.T) {
	th := components.DefaultTheme()
	primary := chrome.DecisionPrimary(th)
	lines := chrome.OptionLine(th, primary, "Approve", true, 40, 0)
	require.NotEmpty(t, lines)
	joined := ""
	for _, sp := range lines[0] {
		joined += sp.Text
	}
	assert.Contains(t, joined, chrome.SelectArrow)
	assert.Contains(t, joined, chrome.DotOn)
	assert.Contains(t, joined, "Approve")
	assert.NotContains(t, joined, "Alt+")
}
