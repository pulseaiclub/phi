package code

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
)

type recordingLists struct {
	shown int
}

func (l *recordingLists) ShowList(
	[]listpicker.Item,
	listpicker.ShowConfig,
	func(listpicker.Item),
	func(listpicker.Item),
) {
	l.shown++
}

func (*recordingLists) SetListFocusReturn(components.Widget) {}

func TestCodeCommandRegistersSlashAndOpens(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n"), 0o600))
	registry := commands.NewCommandRegistry()
	lists := &recordingLists{}
	noop := func() {}
	p := New(components.DefaultTheme(), dir, Deps{
		Registry:    registry,
		AddRef:      func(chat.Ref) {},
		Toast:       nil,
		Publish:     func(string) {},
		Lists:       lists,
		FocusPane:   noop,
		FocusChat:   noop,
		FocusTarget: nil,
	})

	assert.Equal(t, "/code", registry.LookupInsert("code"))
	_, incomplete := registry.IncompleteSlash("/code")
	assert.False(t, incomplete, "a bare /code opens the file picker")

	assert.True(t, registry.DispatchSlash("/code", commands.NewContext(controller.NewBus(nil), nil)))
	assert.Equal(t, 1, lists.shown, "bare /code opens the file list picker")

	assert.True(t, registry.DispatchSlash("/code a.go:2", commands.NewContext(controller.NewBus(nil), nil)))
	assert.True(t, p.Active(), "explicit path opens the viewer")
	assert.Equal(t, 1, lists.shown, "explicit path must not open the picker")

	p.Close()
	assert.False(t, p.Active())
}
