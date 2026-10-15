package block

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestThinkingCacheTracksBodyChanges(t *testing.T) {
	block := &ThinkingBlock{Text: "hello 世界", Expanded: true}
	ctx := components.DrawContext{Max: components.Size{Width: 20}}
	block.Draw(ctx)
	for _, change := range []func(){
		func() { block.Text += " updated" },
		func() { block.Theme = components.DarculaTheme() },
		func() { ctx.Max.Width = 8 },
		func() { block.Streaming = true },
		func() { block.Expanded = false },
		func() { block.Text = "new body"; block.Expanded = true },
	} {
		change()
		fresh := &ThinkingBlock{
			Text: block.Text, Expanded: block.Expanded,
			Theme: block.Theme, Streaming: block.Streaming,
		}
		got, want := block.Draw(ctx), fresh.Draw(ctx)
		assert.Equal(t, want.Size, got.Size)
		assert.Equal(t, want.Buffer, got.Buffer)
	}
}
