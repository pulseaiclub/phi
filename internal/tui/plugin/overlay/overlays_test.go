package overlay

import (
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func testPlugin(activity *controller.ActivityHandler) *Plugin {
	return New(components.DefaultTheme(), activity, nil, nil, nil, nil)
}
