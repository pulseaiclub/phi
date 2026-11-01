package listrows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/session"
)

func TestFormatRelative(t *testing.T) {
	now := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	assert.Equal(t, "just now", FormatRelative(now.Add(-30*time.Second), now))
	assert.Equal(t, "5m ago", FormatRelative(now.Add(-5*time.Minute), now))
	assert.Equal(t, "3h ago", FormatRelative(now.Add(-3*time.Hour), now))
	assert.Equal(t, "yesterday", FormatRelative(now.Add(-25*time.Hour), now))
	assert.Equal(t, "3d ago", FormatRelative(now.Add(-3*24*time.Hour), now))
	assert.Equal(t, "Jan 2", FormatRelative(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), now))
	assert.Equal(t, "Dec 2025", FormatRelative(time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), now))
	assert.Equal(t, "—", FormatRelative(time.Time{}, now))
}

func TestSessionItemsMapsCurrentBadge(t *testing.T) {
	now := time.Now()
	items := SessionItems([]session.SessionMeta{
		{ID: "aaaaaaaa-1111", Preview: "one", Mtime: now},
		{ID: "bbbbbbbb-2222", Preview: "two", Mtime: now},
	}, "bbbbbbbb-2222", now)
	require.Len(t, items, 2)
	assert.Empty(t, items[0].Badge)
	assert.Equal(t, "current", items[1].Badge)
	assert.Equal(t, "bbbbbbbb", items[1].Primary)
}
