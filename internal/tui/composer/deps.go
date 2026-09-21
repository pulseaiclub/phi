package composer

import (
	"context"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	imgutil "github.com/pulseaiclub/phi/internal/util/image"
)

// Input is the composer surface Submitter (and its BashRunner) use.
type Input interface {
	HideCompleters()
	ClearInput()
	SetInput(text string)
	PendingSkills() []string
	PendingImages() []imgutil.Attachment
	PendingRefs() []chat.Ref
	ClearPendingSkills()
	ClearPendingImages()
	ClearPendingRefs()
	SyncBashBorder(text string)
	CloseMentionSlash()
	SetBashBorderActive(active bool)
}

// BashPredictor ranks completions for text typed in "!" mode. It owns reading
// and caching the shell history behind that text; the composer only asks for a
// ranking and shows it.
// Predict is called off the UI goroutine. It must respect ctx — a new query
// cancels the one before it — and be safe against overlapping calls: the
// composer cancels the previous context but does not wait for it to return.
type BashPredictor interface {
	Predict(ctx context.Context, typed string) ([]controller.BashSuggestion, error)
}

// BusyChecker is the submit side of ComposerPane wiring (avoids composer→submit import).
type BusyChecker interface {
	RunningBash() bool
	IsBusy() bool
	SyncBashBorder(text string)
}

// OverlayComposer is the composer surface permission/continue overlays need.
type OverlayComposer interface {
	HideCompleters()
	HidePalette()
}

// LabelComposer receives the composer status slot (activity or token labels).
type LabelComposer interface {
	SetBottomLeftLabel(layout.BorderLabel)
	ClearBottomLeftLabel()
}

// PaletteComposer receives hook and builtin palette updates.
type PaletteComposer interface {
	SetPaletteCommands([]palette.PaletteCommand)
	PushPalette(title string, cmds []palette.PaletteCommand)
}

// WireKeyHandler handles overlay keyboard input.
type WireKeyHandler func(ctx *components.EventContext, e xui.KeyEvent) bool
