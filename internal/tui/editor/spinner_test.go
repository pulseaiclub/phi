package editor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestSpinnerAdvancesEveryFourthFrame(t *testing.T) {
	e := newTestEditor(t)
	e.activityPlugin.Apply(controller.ActivityStreaming)

	for range 3 {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame)

	frame(t, e)
	require.Equal(t, 1, e.spin.Frame)

	e.activityPlugin.Apply(controller.ActivityCancelled)
	require.Equal(t, 0, e.spin.Frame, "activity changes reset the frame")
	for range 4 {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame, "activities without spinner must not advance frames")
}

func TestActivityChangeRestartsSpinnerPace(t *testing.T) {
	e := newTestEditor(t)
	e.activityPlugin.Apply(controller.ActivityStreaming)
	frame(t, e)
	frame(t, e)

	e.activityPlugin.Apply(controller.ActivityTools)
	require.Equal(t, 0, e.spin.Frame)

	for range 4 {
		frame(t, e)
	}
	require.Equal(t, 1, e.spin.Frame)
}
