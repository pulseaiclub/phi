package listrows

import (
	"slices"
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
)

func TestFileItems(t *testing.T) {
	paths := []string{"internal/b/main.go", "README.md", "internal/a/main.go"}
	original := slices.Clone(paths)
	assert.Equal(t, []listpicker.Item{
		{ID: "README.md", Primary: "README.md", Detail: "."},
		{ID: "internal/a/main.go", Primary: "main.go", Detail: "internal/a"},
		{ID: "internal/b/main.go", Primary: "main.go", Detail: "internal/b"},
	}, FileItems(paths))
	assert.Equal(t, original, paths, "sorting must not mutate the caller's paths")
	assert.Empty(t, FileItems(nil))
}

func TestFileConfig(t *testing.T) {
	cfg := FileConfig()
	assert.Equal(t, "Files", cfg.Title)
	assert.Equal(t, "filename or path", cfg.FilterHint)
	assert.Equal(t, "No files found", cfg.Empty)
	assert.Equal(t, chrome.ListHint("open"), cfg.Hint)
	assert.Equal(t, 30, cfg.PrimaryWidth)
}

func TestFilePickerFiltersFullPath(t *testing.T) {
	var accepted listpicker.Item
	p := &listpicker.Picker{OnAccept: func(item listpicker.Item) { accepted = item }}
	p.Show(FileItems([]string{"internal/a/main.go", "internal/b/main.go", "main.go"}), FileConfig())
	ctx := &components.EventContext{}
	p.Handle(ctx, xui.PasteEvent{Text: "internal/b/main.go"})
	s := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	text := components.SurfaceText(s.Children[0].Surface)
	assert.Contains(t, text, "Files · 1/3")
	assert.Contains(t, strings.Join(strings.Fields(text), " "), "main.go internal/b")
	assert.NotContains(t, text, "internal/a")
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	assert.Equal(t, "internal/b/main.go", accepted.ID)
}
