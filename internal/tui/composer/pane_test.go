package composer

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/mention"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/clipboard"
	"github.com/pulseaiclub/phi/internal/util/gitx"
	imgutil "github.com/pulseaiclub/phi/internal/util/image"
)

// png1x1Base64 is a 1x1 transparent PNG (same fixture as util/image tests).
const png1x1Base64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func TestTryAttachClipboardImageBlockedWithoutModelSupport(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/tmp")
	bus := controller.NewBus(nil)
	c.bus = bus
	c.imageEnabled = func() bool { return false }

	ctx := &components.EventContext{}
	require.True(t, c.tryAttachClipboardImage(ctx))
	assert.True(t, ctx.Consume)
	assert.Contains(t, drainToast(t, bus), "does not support images")
}

func TestTryAttachClipboardImageFallsThroughWhenSupported(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/tmp")
	c.imageEnabled = func() bool { return true }

	ctx := &components.EventContext{}
	// No clipboard tooling in CI: the gate passes and the read reports
	// ErrUnavailable, so the key is not consumed.
	require.False(t, c.tryAttachClipboardImage(ctx))
}

func TestTryAttachClipboardImageAllowedWithoutModelInfo(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/tmp")

	ctx := &components.EventContext{}
	require.False(t, c.tryAttachClipboardImage(ctx))
}

// stubClipboard replaces the two clipboard reads behind Ctrl+V.
func stubClipboard(c *ComposerPane, image func() (imgutil.Result, error), text func() (string, error)) {
	c.clipboardImage = image
	c.clipboardText = text
}

func noClipboardImage() (imgutil.Result, error) { return imgutil.Result{}, clipboard.ErrUnavailable }

func TestPasteKeyPastesClipboardText(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/tmp")
	c.imageEnabled = func() bool { return true }
	stubClipboard(c, noClipboardImage, func() (string, error) { return "hello\nworld", nil })

	ctx := &components.EventContext{}
	require.True(t, c.tryPasteClipboard(ctx))
	assert.True(t, ctx.Consume)
	assert.Equal(t, "hello\nworld", c.Chat.Value)
	assert.Equal(t, len("hello\nworld"), c.Chat.Cursor)
	assert.Empty(t, c.Chat.PendingImages)
}

// A text paste must not need image support: only an image clipboard earns the
// "model does not support images" warning.
func TestPasteKeyPastesTextWithoutImageSupport(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/tmp")
	bus := controller.NewBus(nil)
	c.bus = bus
	c.imageEnabled = func() bool { return false }
	stubClipboard(c, func() (imgutil.Result, error) {
		return imgutil.Result{Data: []byte("png"), MimeType: "image/png"}, nil
	}, func() (string, error) { return "just text", nil })

	ctx := &components.EventContext{}
	require.True(t, c.tryPasteClipboard(ctx))
	assert.Equal(t, "just text", c.Chat.Value)
	assert.Empty(t, c.Chat.PendingImages)
	assert.Empty(t, drainToast(t, bus), "text paste must not warn about images")
}

func TestPasteKeyPrefersClipboardImage(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/tmp")
	c.imageEnabled = func() bool { return true }
	stubClipboard(c, func() (imgutil.Result, error) {
		return imgutil.Result{Data: []byte("png"), MimeType: "image/png"}, nil
	}, func() (string, error) { return "ignored", nil })

	ctx := &components.EventContext{}
	require.True(t, c.tryPasteClipboard(ctx))
	require.Len(t, c.Chat.PendingImages, 1)
	assert.Empty(t, c.Chat.Value)
}

func TestPasteKeyFallsThroughWhenClipboardHoldsNothing(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/tmp")
	c.imageEnabled = func() bool { return true }
	stubClipboard(c, noClipboardImage, func() (string, error) { return "", clipboard.ErrEmpty })

	ctx := &components.EventContext{}
	require.False(t, c.tryPasteClipboard(ctx))
	assert.False(t, ctx.Consume)
	assert.Empty(t, c.Chat.Value)
}

func TestAcceptMentionImageBlockedWithoutModelSupport(t *testing.T) {
	dir := t.TempDir()
	png, err := base64.StdEncoding.DecodeString(png1x1Base64)
	require.NoError(t, err)
	path := filepath.Join(dir, "pixel.png")
	require.NoError(t, os.WriteFile(path, png, 0o644))

	c := NewComposerPane(components.DefaultTheme(), "m", dir)
	bus := controller.NewBus(nil)
	c.bus = bus
	c.imageEnabled = func() bool { return false }
	c.Chat.Value = "@pixel.png"
	c.Chat.Cursor = len(c.Chat.Value)

	c.acceptMention(mention.Item{Path: "pixel.png"})

	assert.Contains(t, drainToast(t, bus), "does not support images")
	assert.Empty(t, c.Chat.PendingImages)
	assert.Equal(t, "@pixel.png ", c.Chat.Value)
}

// drainToast returns the message of the last queued ToastMsg and empties the bus.
func drainToast(t *testing.T, bus *controller.Bus) string {
	t.Helper()
	var msg string
	for _, m := range bus.Drain() {
		if tm, ok := m.(controller.ToastMsg); ok {
			msg = tm.Message
		}
	}
	return msg
}

func TestAcceptMentionImageAttachesWhenSupported(t *testing.T) {
	dir := t.TempDir()
	png, err := base64.StdEncoding.DecodeString(png1x1Base64)
	require.NoError(t, err)
	path := filepath.Join(dir, "pixel.png")
	require.NoError(t, os.WriteFile(path, png, 0o644))

	c := NewComposerPane(components.DefaultTheme(), "m", dir)
	c.imageEnabled = func() bool { return true }
	c.Chat.Value = "@pixel.png"
	c.Chat.Cursor = len(c.Chat.Value)

	c.acceptMention(mention.Item{Path: "pixel.png"})

	require.Len(t, c.Chat.PendingImages, 1)
	assert.Equal(t, "pixel.png", c.Chat.PendingImages[0].Label)
	assert.Empty(t, c.Chat.Value)
}

func TestTopRightLabelPairsModelLeftOfThink(t *testing.T) {
	th := components.DefaultTheme()
	c := NewComposerPane(th, "sonnet", "/tmp")
	assert.Equal(t, "sonnet", c.Chat.TopRightLabel.Text)
	assert.Equal(t, th.IdentityOrSuccess(), c.Chat.TopRightLabel.Style)

	c.SetModelLabel("sonnet", "high")
	require.Len(t, c.Chat.TopRightLabel.Spans, 3)
	assert.Equal(t, "sonnet", c.Chat.TopRightLabel.Spans[0].Text)
	assert.Equal(t, th.IdentityOrSuccess(), c.Chat.TopRightLabel.Spans[0].Style)
	assert.Equal(t, "high", c.Chat.TopRightLabel.Spans[2].Text)
	assert.Equal(t, th.IdentityOrSuccess(), c.Chat.TopRightLabel.Spans[2].Style)

	c.SetModelLabel("sonnet", "off")
	assert.Equal(t, "sonnet", c.Chat.TopRightLabel.Text)
	assert.Equal(t, th.IdentityOrSuccess(), c.Chat.TopRightLabel.Style)
	assert.Empty(t, c.Chat.TopRightLabel.Spans)
}

func TestSlashTabCompletesWithoutSubmitting(t *testing.T) {
	c, bus := wiredComposer(t)
	openSlashPicker(t, c, "/ne", "ne")

	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyTab, Press: true})

	assert.Equal(t, "/new ", c.Chat.Value, "Tab should fill the command in the composer")
	assert.Empty(t, submittedText(t, bus), "Tab must not run the command")
	assert.False(t, c.slash.Open)
}

func TestSlashTabCompletesNeedsArgsCommand(t *testing.T) {
	c, bus := wiredComposer(t)
	openSlashPicker(t, c, "/di", "di")

	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyTab, Press: true})

	assert.Equal(t, "/diff ", c.Chat.Value, "Trailing space stays single")
	assert.Empty(t, submittedText(t, bus))
}

func TestSlashEnterStillSubmits(t *testing.T) {
	c, bus := wiredComposer(t)
	openSlashPicker(t, c, "/ne", "ne")

	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "/new", c.Chat.Value)
	assert.Equal(t, "/new", submittedText(t, bus), "Enter keeps running a no-arg command")
}

func TestMentionTabCompletesIntoComposer(t *testing.T) {
	c, bus := wiredComposer(t)
	c.mention.SetResults([]mention.Item{{Path: "go.mod"}}, "")
	c.mention.Show()
	c.Chat.MentionOpen = true
	c.Chat.Value = "@go"
	c.Chat.Cursor = len(c.Chat.Value)

	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyTab, Press: true})

	assert.Equal(t, "@go.mod ", c.Chat.Value)
	assert.Empty(t, submittedText(t, bus))
	assert.False(t, c.mention.Open)
}

// wiredComposer builds a composer wired like the app, with a small command
// registry: `new` (no args, runs on Enter) and `diff` (needs args).
func wiredComposer(t *testing.T) (*ComposerPane, *controller.Bus) {
	t.Helper()
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	bus := controller.NewBus(nil)
	reg := commands.NewCommandRegistry()
	reg.Register(commands.Command{Name: "new", Slash: true})
	reg.Register(commands.Command{Name: "diff", Slash: true, NeedsArgs: true})
	c.Wire(nil, nil, reg, c.cwd, bus, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	return c, bus
}

// openSlashPicker types value into the composer and opens the slash picker.
func openSlashPicker(t *testing.T, c *ComposerPane, value, query string) {
	t.Helper()
	c.Chat.Value = value
	c.Chat.Cursor = len(value)
	c.onSlashChange(true, query)
	require.True(t, c.slash.Open, "slash picker should be open")
}

// submittedText drains SubmitMsg texts published on the bus.
func submittedText(t *testing.T, bus *controller.Bus) string {
	t.Helper()
	out := ""
	for _, m := range bus.Drain() {
		if sm, ok := m.(controller.SubmitMsg); ok {
			out = sm.Text
		}
	}
	return out
}

func TestShowBranchListDrawsRowsAndRoutesAccept(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/repo")
	var accepted []string
	c.ShowBranchList(
		[]gitx.Branch{
			{
				Name: "main", Current: true, Upstream: "origin/main",
				Committed: "3 hours ago", Subject: "init",
			},
			{Name: "fix/x", Ahead: 1, Subject: "wip"},
		},
		[]string{"fix/x"},
		func(name string) { accepted = append(accepted, name) },
	)

	// 80 columns is the narrow end worth guarding: the branch name column must
	// leave the commit subject visible.
	overlay, ok := c.ListOverlay(components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	})
	require.True(t, ok)
	text := components.SurfaceText(overlay.Surface)
	assert.Contains(t, text, "Branches")
	assert.Contains(t, text, "fix/x")
	assert.Contains(t, text, "init", "the commit column is the second one")
	assert.NotContains(t, text, "current", "two columns only — no badge column")
	assert.NotContains(t, text, "origin/main", "no upstream column")

	// The picker opens on the current branch, so Enter on an untouched list is
	// the no-op the caller has to answer for.
	c.listPicker.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	assert.Equal(t, []string{"main"}, accepted)
}

func TestListAcceptHandlerIsNotSharedBetweenDomains(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", "/repo")
	var sessions, branches []string
	c.ShowList(nil, listpicker.ShowConfig{}, func(item listpicker.Item) { sessions = append(sessions, item.ID) })
	c.ShowBranchList([]gitx.Branch{{Name: "main", Current: true}}, nil, func(name string) {
		branches = append(branches, name)
	})

	c.listPicker.Items = []listpicker.Item{{ID: "main", Badge: "current"}}
	c.listPicker.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEnter})

	assert.Equal(t, []string{"main"}, branches)
	assert.Empty(t, sessions, "opening a second picker must replace the first accept path")
}
