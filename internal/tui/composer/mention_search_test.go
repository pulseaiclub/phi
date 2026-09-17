package composer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// Each keystroke must cancel the search in flight. Without this every
// keystroke leaves an fd process walking the tree, and on a large tree they
// pile up faster than they finish.
func TestScheduleMentionSearchCancelsPrevious(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())

	c.scheduleMentionSearch("a")
	require.NotNil(t, c.mentionCancel, "a search must be cancellable")

	// Capture the first search's context by canceling through the stored
	// func, then confirm a second schedule installs a different one.
	firstDone := make(chan struct{})
	first := c.mentionCancel
	c.mentionCancel = func() { close(firstDone); first() }

	c.scheduleMentionSearch("ab")

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		require.Fail(t, "scheduling a new search must cancel the previous one")
	}
}

// A superseded search must not publish, or a stale list can overwrite a newer
// one between the generation check and the update.
func TestSupersededSearchDoesNotPublish(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	bus := controller.NewBus(nil)
	c.bus = bus

	// Cancelled well inside the 100ms debounce, so no work should start.
	c.scheduleMentionSearch("first")
	c.scheduleMentionSearch("second")

	time.Sleep(500 * time.Millisecond)

	var queries []string
	for _, m := range bus.Drain() {
		if res, ok := m.(controller.MentionResultsMsg); ok {
			queries = append(queries, res.Query)
		}
	}
	assert.NotContains(t, queries, "first", "a cancelled search must stay quiet")
}

// A truncated result must say so. Otherwise a missing file reads as "not
// there" when it is really "past the limit".
func TestApplyMentionResultsReportsTruncation(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	c.mention.Show()

	paths := make([]string, mentionSearchLimit)
	for i := range paths {
		paths[i] = "file.go"
	}
	c.ApplyMentionResults(controller.MentionResultsMsg{
		Gen: c.mentionGen, Paths: paths, Truncated: true,
	})

	assert.Contains(t, c.mention.Status, "type more to narrow")
	assert.Len(t, c.mention.Items, mentionSearchLimit)
}

// A complete list must not claim to be partial.
func TestApplyMentionResultsQuietWhenComplete(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	c.mention.Show()

	c.ApplyMentionResults(controller.MentionResultsMsg{
		Gen: c.mentionGen, Paths: []string{"a.go", "b.go"},
	})

	assert.Empty(t, c.mention.Status, "a full result needs no warning")
	assert.Len(t, c.mention.Items, 2)
}

// Canceling the picker must stop the search, not leave it running.
func TestHideCompletersCancelsSearch(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())

	c.scheduleMentionSearch("q")
	require.NotNil(t, c.mentionCancel)

	done := make(chan struct{})
	inner := c.mentionCancel
	c.mentionCancel = func() { close(done); inner() }

	c.HideCompleters()

	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "closing the picker must cancel the search in flight")
	}
}

func TestApplyMentionResultsEmpty(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	c.mention.Show()

	c.ApplyMentionResults(controller.MentionResultsMsg{Gen: c.mentionGen})

	assert.Equal(t, "No matching files", c.mention.Status)
}

// The picker shows this text, so it must describe the search, not the child
// process. "fd: signal: killed" is not something a user can act on.
func TestTimeoutTextIsActionable(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	c.mention.Show()

	c.ApplyMentionResults(controller.MentionResultsMsg{
		Gen: c.mentionGen, ErrText: "Search timed out — type more of the path",
	})

	assert.NotContains(t, c.mention.Status, "signal:")
	assert.NotContains(t, c.mention.Status, "fd:")
	assert.Contains(t, c.mention.Status, "type more of the path")
}
