package changelist

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/util/gitx"
)

func TestStatusLabel(t *testing.T) {
	tests := []struct {
		status byte
		want   string
	}{
		{'M', "Modified"},
		{'A', "Added"},
		{'D', "Deleted"},
		{'R', "Renamed"},
		{'C', "Copied"},
		{'T', "Type changed"},
		{'?', "New file"},
		{'U', "Changed"},
		{'X', "Changed"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, statusLabel(tt.status))
		})
	}
}

func TestDisplayPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			"plain path passes through",
			"internal/components/changelist/items.go",
			"internal/components/changelist/items.go",
		},
		{"empty path stays empty", "", ""},
		{"control character is quoted", "dir/\x01bad.go", `"dir/\x01bad.go"`},
		{"newline is quoted", "a\nb.go", `"a\nb.go"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, displayPath(tt.path))
		})
	}
}

func TestItems(t *testing.T) {
	changes := []gitx.Change{
		{Path: "modified.go", Status: 'M'},
		{Path: "added.go", Status: 'A'},
		{Path: "deleted.go", Status: 'D'},
		{Path: "renamed.go", OldPath: "old.go", Status: 'R'},
		{Path: "copied.go", OldPath: "source.go", Status: 'C'},
		{Path: "typed.go", Status: 'T'},
		{Path: "untracked.go", Status: '?'},
		{Path: "conflicted.go", Status: 'U'},
	}

	items := Items(changes)

	assert.Len(t, items, len(changes))
	for i, change := range changes {
		assert.Equal(t, change.Path, items[i].ID, "row ID is the current path")
		assert.Equal(t, change.Path, items[i].Primary)
	}

	assert.Equal(t, "Modified", items[0].Detail)
	assert.Equal(t, "Added", items[1].Detail)
	assert.Equal(t, "Deleted", items[2].Detail)
	assert.Equal(t, "Renamed from old.go", items[3].Detail)
	assert.Equal(t, "Copied from source.go", items[4].Detail)
	assert.Equal(t, "Type changed", items[5].Detail)
	assert.Equal(t, "New file", items[6].Detail)
	assert.Equal(t, "Unresolved conflict", items[7].Detail)
}

func TestItemsRenameShowsBothPaths(t *testing.T) {
	items := Items([]gitx.Change{{Path: "new name.go", OldPath: "old name.go", Status: 'R'}})

	assert.Len(t, items, 1)
	assert.Equal(t, "new name.go", items[0].Primary)
	assert.Equal(t, "Renamed from old name.go", items[0].Detail)
	assert.Equal(t, "new name.go old name.go", items[0].Keywords, "both sides stay searchable")
}

func TestItemsQuotesControlCharactersInOldPath(t *testing.T) {
	items := Items([]gitx.Change{{Path: "new.go", OldPath: "old/\x07bell.go", Status: 'R'}})

	assert.Len(t, items, 1)
	assert.Equal(t, "Renamed from \"old/\\abell.go\"", items[0].Detail)
	assert.Equal(t, "new.go old/\x07bell.go", items[0].Keywords, "keywords keep the raw path")
}

func TestItemsEmpty(t *testing.T) {
	items := Items(nil)

	assert.Empty(t, items)
	assert.NotNil(t, items)
}
