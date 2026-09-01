package phi

import (
	"slices"

	"github.com/pulseaiclub/phi/ext"
)

// extensionUI implements ext.UI over PXB host requests / notify frames.
type extensionUI struct{ extensionAPI *ExtensionAPI }

func (u extensionUI) Notify(message, kind string) { u.extensionAPI.Notify(kind, message) }
func (u extensionUI) SetStatus(_, text string)    { u.extensionAPI.SetStatus(text) }
func (u extensionUI) Confirm(title, message string) bool {
	return u.extensionAPI.Confirm(title, message)
}

func (u extensionUI) ConfirmOpts(req ext.ConfirmRequest) ext.ConfirmReply {
	return u.extensionAPI.ConfirmOpts(req)
}

func appendUnique(xs []uint16, v uint16) []uint16 {
	if slices.Contains(xs, v) {
		return xs
	}
	return append(xs, v)
}

type unexpectedTypeError struct {
	want string
	got  uint16
}

func (e unexpectedTypeError) Error() string {
	return "sdk: expected " + e.want + " frame"
}

func errUnexpected(want string, got uint16) error {
	return unexpectedTypeError{want: want, got: got}
}
