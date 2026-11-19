package overlay

import (
	"github.com/pulseaiclub/phi/internal/components"
)

func testPlugin(activity ActivitySink) *Plugin {
	return New(Deps{Theme: components.DefaultTheme(), Activity: activity})
}
