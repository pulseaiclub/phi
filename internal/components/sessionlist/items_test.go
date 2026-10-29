package sessionlist

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

func TestItemsMapsCurrentBadge(t *testing.T) {
	now := time.Now()
	items := Items([]session.SessionMeta{
		{ID: "aaaaaaaa-1111", Preview: "one", Mtime: now},
		{ID: "bbbbbbbb-2222", Preview: "two", Mtime: now},
	}, "bbbbbbbb-2222", now)
	require.Len(t, items, 2)
	assert.Empty(t, items[0].Badge)
	assert.Equal(t, "current", items[1].Badge)
	assert.Equal(t, "bbbbbbbb", items[1].Primary)
	assert.Equal(t, "two", items[1].Detail)
	assert.Empty(t, items[1].Edit, "an unnamed session opens the rename line blank")
}

// Named and unnamed sessions share the ID column; the title replaces the
// preview on screen while both remain searchable.
func TestItemsPrefersTitle(t *testing.T) {
	now := time.Now()
	items := Items([]session.SessionMeta{
		{ID: "aaaaaaaa-1111", Title: "fix resume leak", Preview: "look at Start", Mtime: now},
	}, "", now)
	require.Len(t, items, 1)

	assert.Equal(t, "aaaaaaaa", items[0].Primary)
	assert.Equal(t, "fix resume leak", items[0].Detail)
	assert.Equal(t, "fix resume leak", items[0].Edit, "rename pre-fills the full title")
	assert.Contains(t, items[0].Keywords, "aaaaaaaa-1111")
	assert.Contains(t, items[0].Keywords, "fix resume leak")
	assert.Contains(t, items[0].Keywords, "look at Start")
}
