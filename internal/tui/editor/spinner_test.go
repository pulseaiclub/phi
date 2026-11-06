package editor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestSpinnerAdvancesEveryFourthFrame(t *testing.T) {
	e := newTestEditor(t)
	e.activity.Apply(controller.ActivityStreaming)

	for range spinnerFrameDivisor - 1 {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame)

	frame(t, e)
	require.Equal(t, 1, e.spin.Frame)

	e.activity.Apply(controller.ActivityCancelled)
	require.Equal(t, 0, e.spin.Frame, "activity changes reset the frame")
	for range spinnerFrameDivisor {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame, "activities without spinner must not advance frames")
}

func TestActivityChangeRestartsSpinnerPace(t *testing.T) {
	e := newTestEditor(t)
	e.activity.Apply(controller.ActivityStreaming)
	for range 2 {
		e.advanceSpinner()
	}

	e.activity.Apply(controller.ActivityTools)
	require.Equal(t, 0, e.spin.Frame)
	require.Equal(t, 0, e.spinTick)

	for range spinnerFrameDivisor {
		e.advanceSpinner()
	}
	require.Equal(t, 1, e.spin.Frame)
}
