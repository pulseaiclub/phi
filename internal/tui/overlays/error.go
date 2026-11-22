package overlays

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/ask"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// errorOptions is the error dialog's answer set; the index order is the
// ErrorReply mapping in errorReplyFor.
var errorOptions = []ask.Option{
	{Label: "Retry", Keys: "r"},
	{Label: "Copy to Clipboard", Keys: "cy"},
	{Label: "Dismiss", Keys: "dn"},
}

const (
	errOptRetry = iota // index into errorOptions
	errOptCopy
	errOptDismiss
)

func errorDialog(text string) *ask.Dialog {
	return &ask.Dialog{
		Title:   "Error",
		Message: text,
		Hint:    chrome.AskHint("↑↓ move", "select", "dismiss"),
		Options: errorOptions,
	}
}

func (o *Overlays) beginErrorAsk(msg controller.OverlayMsg) {
	if o.err != nil {
		o.resolveError(controller.ErrorReply{})
	}
	if o.confirm != nil {
		o.resolveExtConfirm(controller.ExtConfirmReply{})
	}
	if o.composer != nil {
		o.composer.HideCompleters()
		o.composer.HidePalette()
	}
	o.err = errorDialog(msg.ErrorText)
	// The turn is over — this dialog is the aftermath, not an in-flight ask.
	o.activity.Apply(controller.ActivityIdle)
	if o.focusEditor != nil {
		o.focusEditor()
	}
}

func (o *Overlays) dismissError() {
	was := o.err != nil
	o.err = nil
	if !was {
		return
	}
	if o.activity != nil && o.activity.Current == controller.ActivityAwaitingApproval {
		o.activity.Apply(controller.ActivityIdle)
	}
	if o.focusChat != nil {
		o.focusChat()
	}
}

// resolveError clears the overlay and delivers the reply directly to the
// onResolve callback: nobody waits on this dialog, so unlike the ask
// overlays there is no reply chan.
func (o *Overlays) resolveError(r controller.ErrorReply) {
	st := o.err
	if st == nil {
		return
	}
	text := st.Message
	o.dismissError()
	if o.onErrorResolve != nil {
		o.onErrorResolve(r, text)
	}
}

// ResolveError sends an error-dialog reply and clears the overlay.
func (o *Overlays) ResolveError(r controller.ErrorReply) {
	o.resolveError(r)
}

// handleErrorKey forwards keys to the dialog; a committed option maps to the
// shell reply in errorReplyFor.
func (o *Overlays) handleErrorKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	st := o.err
	if st == nil || !e.Press {
		return false
	}
	if pick, chosen := st.HandleKey(e); chosen {
		o.resolveError(errorReplyFor(pick))
	}
	ctx.ConsumeAndRedraw()
	return true
}

// errorReplyFor maps a committed option index to the reply the shell acts on.
// Esc (pick -1) and Dismiss both resolve empty.
func errorReplyFor(pick int) controller.ErrorReply {
	switch pick {
	case errOptRetry:
		return controller.ErrorReply{Retry: true}
	case errOptCopy:
		return controller.ErrorReply{Copy: true}
	default:
		return controller.ErrorReply{}
	}
}
