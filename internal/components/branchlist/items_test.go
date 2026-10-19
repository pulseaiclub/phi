package branchlist

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

func TestItemsAnchorsCurrentThenRecentThenLocalThenRemote(t *testing.T) {
	branches := []gitx.Branch{
		{Name: "main", Current: true},
		{Name: "fix/agent-wait", Subject: "wip"},
		{Name: "feature/old"},
		{Name: "origin/main", Remote: true},
	}

	items := Items(branches, []string{"feature/old", "main"})

	assert.Equal(t, []string{"main", "feature/old", "fix/agent-wait", "origin/main"}, primaries(items),
		"current anchors the list, then recent, then local by commit date, remotes last")
	assert.Len(t, items, 4, "no branch is listed twice")
}

func TestItemsIgnoresRecentNamesThatNoLongerExist(t *testing.T) {
	branches := []gitx.Branch{{Name: "main", Current: true}}

	items := Items(branches, []string{"deleted-branch", "main"})

	assert.Equal(t, []string{"main"}, primaries(items))
}

func TestRowsAreNameAndLastCommitOnly(t *testing.T) {
	tests := []struct {
		name    string
		branch  gitx.Branch
		leading string
		detail  string
	}{
		{
			name: "current branch",
			branch: gitx.Branch{
				Name: "main", Current: true, Upstream: "origin/main",
				Ahead: 2, Behind: 3, Committed: "3 hours ago", Subject: "chore: bump",
			},
			leading: "●",
			detail:  "chore: bump",
		},
		{
			name:   "diverged branch keeps the counts out of the row",
			branch: gitx.Branch{Name: "fix/y", Upstream: "origin/fix/y", Ahead: 2, Behind: 3, Subject: "wip"},
			detail: "wip",
		},
		{
			name:   "deleted upstream keeps the badge out of the row",
			branch: gitx.Branch{Name: "stale", Upstream: "origin/stale", Gone: true, Subject: "old"},
			detail: "old",
		},
		{
			name:   "remote branch",
			branch: gitx.Branch{Name: "origin/main", Remote: true, Subject: "chore: bump"},
			detail: "chore: bump",
		},
		{
			name:   "no commits yet",
			branch: gitx.Branch{Name: "empty"},
			detail: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item := Items([]gitx.Branch{tc.branch}, nil)[0]
			assert.Equal(t, tc.branch.Name, item.ID,
				"the row's own ref is its identity, so a delete cannot hit another branch")
			assert.Equal(t, tc.branch.Name, item.Primary)
			assert.Equal(t, tc.leading, item.Leading)
			assert.Equal(t, tc.detail, item.Detail)
			assert.Empty(t, item.Badge, "two columns means no badge column")
			assert.Contains(t, item.Keywords, tc.branch.Name)
		})
	}
}

// A remote row keeps its own ref as ID: accepting maps it to the tracking
// branch downstream, while deleting it must never land on the local namesake.
func TestRemoteRowKeepsItsOwnIdentity(t *testing.T) {
	item := Items([]gitx.Branch{{Name: "origin/fix/one", Remote: true, Subject: "wip"}}, nil)[0]

	assert.Equal(t, "origin/fix/one", item.ID)
	assert.Equal(t, "origin/fix/one", item.Primary, "the row still says where it lives")
}

// The row carries the commit subject as a keyword, so a local branch whose last
// commit mentions origin/main used to sit above the branch named origin/main.
// Enter after typing the query is the regression: acceptance must land on the
// branch whose name is the query.
func TestFilterPutsTheBranchNamedOriginMainFirst(t *testing.T) {
	items := Items([]gitx.Branch{
		{Name: "main", Current: true, Subject: "merge origin/main into main"},
		{Name: "fix/guard", Subject: "wip"},
		{Name: "origin/main", Remote: true, Subject: "chore: bump"},
	}, nil)

	var got listpicker.Item
	p := &listpicker.Picker{
		Theme:    components.DefaultTheme(),
		OnAccept: func(item listpicker.Item) { got = item },
	}
	p.Show(items, Config())
	ctx := &components.EventContext{}
	for _, r := range "origin/main" {
		p.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
	p.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "origin/main", got.Primary)
}

func TestConfigKeepsBranchNamesReadable(t *testing.T) {
	cfg := Config()
	assert.Equal(t, "Branches", cfg.Title)
	assert.GreaterOrEqual(t, cfg.PrimaryWidth, len("fix/agent-wait-timeout"))
	assert.Contains(t, cfg.Hint, "switch")
	assert.Contains(t, cfg.Hint, "ctrl+x delete", "the picker advertises the delete path /sessions has")
	assert.NotEmpty(t, cfg.ConfirmMessage)
}

// primaries is the display order; IDs are the branch refs, asserted separately.
func primaries(items []listpicker.Item) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Primary)
	}
	return out
}
