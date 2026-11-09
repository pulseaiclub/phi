package composer

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
)

func TestOrdinaryListClearsExtensionCloseHandler(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "", t.TempDir())
	closed := 0
	c.ShowExtensionSelect(ext.SelectRequest{Items: []ext.SelectItem{{Label: "first"}}},
		func(int) {}, func() { closed++ })
	c.ShowList([]listpicker.Item{{ID: "session"}}, listpicker.ShowConfig{}, nil, nil, nil)
	assert.Equal(t, 1, closed, "replacement closes the extension picker")
	pressKey(t, c, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	assert.Equal(t, 1, closed, "ordinary lists must not inherit the extension close handler")
}
