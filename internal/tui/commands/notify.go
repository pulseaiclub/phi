package commands

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// PublishToast sends a toast message via the bus. Command-domain plugins use
// it for feedback that lands on the next drain.
func PublishToast(bus *controller.Bus, msg string, kind toast.ToastKind, d time.Duration) {
	bus.Publish(controller.ToastMsg{Message: msg, Kind: kind, Duration: d})
}
