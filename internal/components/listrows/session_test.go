package listrows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/session"
)

func TestSessionItemsKeepsListingOrder(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	items := SessionItems([]session.SessionMeta{
		// mtime order: newest first, with a fork between two strangers.
		{ID: "other-9999", Preview: "stranger", Mtime: now},
		{ID: "bbbbbbbb-2222", Parent: "aaaaaaaa-1111", Preview: "fork", Mtime: older.Add(time.Minute)},
		{ID: "aaaaaaaa-1111", Preview: "source", Mtime: older},
	})

	require.Len(t, items, 3)
	assert.Equal(t, "other-99", items[0].Primary, "rows keep the listing order")
	assert.Equal(t, "bbbbbbbb ↳ aaaaaaaa", items[1].Primary, "the fork marks its source")
	assert.Equal(t, "aaaaaaaa", items[2].Primary)
}

func TestSessionItemsKeepsForkOfMissingSource(t *testing.T) {
	items := SessionItems([]session.SessionMeta{
		{ID: "cccccccc-3333", Parent: "dddddddd-4444", Preview: "orphan", Mtime: time.Now()},
	})
	require.Len(t, items, 1)
	assert.Equal(t, "cccccccc ↳ dddddddd", items[0].Primary, "the lineage marker outlives the source")
}

func TestSessionItemsShowsForkParent(t *testing.T) {
	now := time.Now()
	items := SessionItems([]session.SessionMeta{
		{ID: "aaaaaaaa-1111", Preview: "source", Mtime: now},
		{ID: "bbbbbbbb-2222", Parent: "aaaaaaaa-1111", Preview: "fork", Mtime: now.Add(time.Minute)},
	})
	require.Len(t, items, 2)
	assert.Equal(t, "aaaaaaaa", items[0].Primary)
	assert.Equal(t, "bbbbbbbb ↳ aaaaaaaa", items[1].Primary)
	assert.Contains(t, items[1].Keywords, "aaaaaaaa-1111", "the parent id is filterable")
}

func TestSessionItemsUsesOnlyCoreColumns(t *testing.T) {
	now := time.Now()
	items := SessionItems([]session.SessionMeta{
		{ID: "aaaaaaaa-1111", Preview: "one", Mtime: now},
		{ID: "bbbbbbbb-2222", Preview: "two", Mtime: now},
	})
	require.Len(t, items, 2)
	for _, item := range items {
		assert.Empty(t, item.Leading)
		assert.NotEmpty(t, item.Primary)
		assert.NotEmpty(t, item.Detail)
	}
}
