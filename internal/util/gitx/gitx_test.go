package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchesListsLocalAndRemote(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "branch", "fix/one")
	originRef(t, dir, "origin/main", "HEAD")
	gitRun(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)

	byName := map[string]Branch{}
	for _, b := range branches {
		byName[b.Name] = b
	}
	assert.ElementsMatch(t, []string{"fix/one", "main", "origin/main"}, branchNames(branches),
		"origin/HEAD is a symref and must not be listed")
	assert.True(t, byName["main"].Current)
	assert.False(t, byName["main"].Remote)
	assert.True(t, byName["origin/main"].Remote)
	assert.Equal(t, "init", byName["main"].Subject)
	assert.NotEmpty(t, byName["main"].Committed, "relative date is git's own, not ours")
}

func TestBranchesUpstreamState(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "branch", "tracked")
	originRef(t, dir, "origin/tracked", "HEAD")
	track(t, dir, "tracked")
	gitRun(t, dir, "checkout", "tracked")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "local work")

	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)

	tracked := findBranch(t, branches, "tracked")
	assert.Equal(t, "origin/tracked", tracked.Upstream)
	assert.Equal(t, 1, tracked.Ahead)
	assert.Zero(t, tracked.Behind)
	assert.False(t, tracked.Gone)
}

func TestBranchesGoneUpstream(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "branch", "stale")
	originRef(t, dir, "origin/stale", "HEAD")
	track(t, dir, "stale")
	gitRun(t, dir, "update-ref", "-d", "refs/remotes/origin/stale")

	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)

	assert.True(t, findBranch(t, branches, "stale").Gone, "deleted upstream surfaces as gone")
}

func TestBranchesOutsideRepo(t *testing.T) {
	requireGit(t)
	_, err := Branches(t.Context(), t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestRecentFollowsReflogOrder(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "branch", "first")
	gitRun(t, dir, "branch", "second")
	gitRun(t, dir, "checkout", "first")
	gitRun(t, dir, "checkout", "second")

	recent, err := Recent(t.Context(), dir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(recent), 2)
	assert.Equal(t, "second", recent[0], "newest checkout first")
	assert.Equal(t, "first", recent[1])
}

func TestRecentHidesDetachedTargets(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "--detach", "HEAD")

	recent, err := Recent(t.Context(), dir)
	require.NoError(t, err)
	for _, name := range recent {
		assert.False(t, isObjectID(name), "raw SHAs are not switchable by name")
	}
}

func TestPreflightReportsDirtyAndOperation(t *testing.T) {
	dir := newRepo(t)

	st, err := Preflight(t.Context(), dir)
	require.NoError(t, err)
	assert.True(t, st.Clean())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o600))
	st, err = Preflight(t.Context(), dir)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Dirty)
	assert.Empty(t, st.Op)

	// A real merge is slow to stage; MERGE_HEAD is the exact thing we stat.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte("x\n"), 0o600))
	st, err = Preflight(t.Context(), dir)
	require.NoError(t, err)
	assert.Equal(t, "merge", st.Op)
	assert.False(t, st.Clean())
}

func TestSwitchChangesCurrentBranch(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "branch", "next")

	require.NoError(t, Switch(t.Context(), dir, "next"))

	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)
	assert.True(t, findBranch(t, branches, "next").Current)
	assert.False(t, findBranch(t, branches, "main").Current)
}

func TestSwitchSurfacesGitRefusal(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "conflict.txt", "base\n")
	gitRun(t, dir, "add", "conflict.txt")
	gitRun(t, dir, "commit", "-m", "base")
	gitRun(t, dir, "checkout", "-b", "target")
	writeFile(t, dir, "conflict.txt", "target\n")
	gitRun(t, dir, "commit", "-am", "target side")
	gitRun(t, dir, "checkout", "main")
	writeFile(t, dir, "conflict.txt", "dirty\n")

	err := Switch(t.Context(), dir, "target")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git switch target")
	assert.Contains(t, err.Error(), "commit or stash first", "the hint names the next step")
}

func TestValidRefRejectsOptionLikeNames(t *testing.T) {
	assert.True(t, ValidRef("fix/one"))
	assert.True(t, ValidRef("origin/main"))
	assert.False(t, ValidRef(""))
	assert.False(t, ValidRef("-f"))
	assert.False(t, ValidRef("--detach"))
	assert.False(t, ValidRef("main; rm -rf /"))
	assert.False(t, ValidRef("main\t--force"))
}

func TestSwitchValidatesBeforeSpawning(t *testing.T) {
	dir := newRepo(t)
	err := Switch(t.Context(), dir, "-f")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid branch name")
}

func branchNames(branches []Branch) []string {
	names := make([]string, 0, len(branches))
	for _, b := range branches {
		names = append(names, b.Name)
	}
	return names
}

func findBranch(t *testing.T, branches []Branch, name string) Branch {
	t.Helper()
	for _, b := range branches {
		if b.Name == name {
			return b
		}
	}
	require.FailNow(t, "branch not listed", name)
	return Branch{}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// newRepo builds a throwaway repo with one commit on main. The remote.origin
// refspec is declared up front so track() can give a branch an upstream
// without standing up a real remote.
func newRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.email", "phi@example.com")
	gitRun(t, dir, "config", "user.name", "phi tests")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	gitRun(t, dir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

// originRef fabricates a remote-tracking ref pointing at rev.
func originRef(t *testing.T, dir, name, rev string) {
	t.Helper()
	gitRun(t, dir, "update-ref", "refs/remotes/"+name, rev)
}

// track makes branch's upstream origin/<branch>, the shape a pushed branch has.
func track(t *testing.T, dir, branch string) {
	t.Helper()
	gitRun(t, dir, "config", "branch."+branch+".remote", "origin")
	gitRun(t, dir, "config", "branch."+branch+".merge", "refs/heads/"+branch)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=phi tests", "GIT_AUTHOR_EMAIL=phi@example.com",
		"GIT_COMMITTER_NAME=phi tests", "GIT_COMMITTER_EMAIL=phi@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func TestCreateBranchesOffHEADAndSwitches(t *testing.T) {
	dir := newRepo(t)

	require.NoError(t, Create(t.Context(), dir, "new-work"))

	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)
	created := findBranch(t, branches, "new-work")
	assert.True(t, created.Current)
	assert.Equal(t, "init", created.Subject, "created at HEAD")
}

func TestCreateRejectsOptionLikeNames(t *testing.T) {
	dir := newRepo(t)

	err := Create(t.Context(), dir, "-c")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid branch name")
}

func TestCreateSurfacesGitRefusal(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "branch", "taken")

	err := Create(t.Context(), dir, "taken")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "git switch -c taken")
}

func TestDeleteRemovesTheBranch(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "branch", "doomed")

	require.NoError(t, Delete(t.Context(), dir, "doomed"))

	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)
	assert.NotContains(t, branchNames(branches), "doomed")
}

// -d and not -D: unmerged commits must survive a mistyped delete.
func TestDeleteRefusesUnmergedCommits(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-b", "unmerged")
	writeFile(t, dir, "extra.txt", "work\n")
	gitRun(t, dir, "add", "extra.txt")
	gitRun(t, dir, "commit", "-m", "unmerged work")
	gitRun(t, dir, "checkout", "main")

	err := Delete(t.Context(), dir, "unmerged")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not fully merged")
	assert.Contains(t, err.Error(), "git branch -D", "the hint names the deliberate override")
}

func TestDeleteRejectsOptionLikeNames(t *testing.T) {
	dir := newRepo(t)

	err := Delete(t.Context(), dir, "-D")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid branch name")
}

func TestLocalName(t *testing.T) {
	remotes := []struct{ name, want string }{
		{"origin/main", "main"},
		{"origin/fix/one", "fix/one"},
		{"upstream/readme", "readme"},
	}
	for _, tc := range remotes {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Branch{Name: tc.name, Remote: true}.LocalName())
		})
	}
	for _, name := range []string{"main", "fix/one"} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, name, Branch{Name: name}.LocalName(),
				"a local branch is its own name")
		})
	}
}
