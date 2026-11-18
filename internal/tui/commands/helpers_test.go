package commands

import (
	"testing"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

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
