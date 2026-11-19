package overlay

import (
	"github.com/pulseaiclub/phi/internal/components"
)

func testPlugin(activity ActivitySink) *Plugin {
	return New(components.DefaultTheme(), activity, nil, nil, nil, nil)
}
