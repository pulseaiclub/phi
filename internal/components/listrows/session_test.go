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

func TestSessionItemsGroupsForksUnderTheirSource(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	items := SessionItems([]session.SessionMeta{
		// mtime order: newest first, and the fork sits between two strangers.
		{ID: "other-9999", Preview: "stranger", Mtime: now},
		{ID: "bbbbbbbb-2222", Parent: "aaaaaaaa-1111", Preview: "fork", Mtime: older.Add(time.Minute)},
		{ID: "aaaaaaaa-1111", Preview: "source", Mtime: older},
	}, "", now)

	require.Len(t, items, 3)
	assert.Equal(t, "other-99", items[0].Primary, "strangers keep their mtime slot")
	assert.Equal(t, "aaaaaaaa", items[1].Primary)
	assert.Equal(t, "bbbbbbbb ↳ aaaaaaaa", items[2].Primary, "the fork follows its source")
}

func TestSessionItemsKeepsForkOfMissingSource(t *testing.T) {
	items := SessionItems([]session.SessionMeta{
		{ID: "cccccccc-3333", Parent: "dddddddd-4444", Preview: "orphan", Mtime: time.Now()},
	}, "", time.Now())
	require.Len(t, items, 1)
	assert.Equal(t, "cccccccc ↳ dddddddd", items[0].Primary, "the lineage marker outlives the source")
}

func TestSessionItemsShowsForkParent(t *testing.T) {
	now := time.Now()
	items := SessionItems([]session.SessionMeta{
		{ID: "aaaaaaaa-1111", Preview: "source", Mtime: now},
		{ID: "bbbbbbbb-2222", Parent: "aaaaaaaa-1111", Preview: "fork", Mtime: now.Add(time.Minute)},
	}, "aaaaaaaa-1111", now)
	require.Len(t, items, 2)
	assert.Equal(t, "aaaaaaaa", items[0].Primary)
	assert.Equal(t, "bbbbbbbb ↳ aaaaaaaa", items[1].Primary)
	assert.Contains(t, items[1].Keywords, "aaaaaaaa-1111", "the parent id is filterable")
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
