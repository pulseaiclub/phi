package spec

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHelp is a discoverer over an in-memory command set: nothing spawns a
// process, and the cache is a temporary directory.
type fakeHelp struct {
	*Discoverer
	outputs map[string]string
	mu      sync.Mutex
	ran     []string
}

func newFakeHelp(t *testing.T, outputs map[string]string) *fakeHelp {
	t.Helper()
	fake := &fakeHelp{outputs: outputs}
	fake.Discoverer = NewDiscoverer(t.TempDir())
	fake.Base = NewTable()
	fake.Cached = NewTable
	fake.Lookup = func(name string) (string, error) {
		if _, ok := outputs[name]; !ok {
			return "", errors.New("not found")
		}
		return "/bin/" + name, nil
	}
	fake.Run = func(_ context.Context, path string) (string, error) {
		name := trimPrefix(path, "/bin/")
		fake.mu.Lock()
		fake.ran = append(fake.ran, name)
		fake.mu.Unlock()
		return outputs[name], nil
	}
	return fake
}

// runs returns the commands whose help was read, safe against the background
// read appending while a test asks.
func (f *fakeHelp) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

func trimPrefix(s, prefix string) string {
	if startsWith(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

func TestDiscoverNowParsesAndStores(t *testing.T) {
	help := newFakeHelp(t, map[string]string{"tea": cobraUsage})

	node, err := help.DiscoverNow(t.Context(), "tea")

	require.NoError(t, err)
	assert.Equal(t, "tea", node.Name)
	assert.Equal(t, "help", node.Source)
	require.NotEmpty(t, node.Options)

	known, ok := help.Table().Lookup("tea", nil)
	assert.True(t, ok, "the discovered tree joins the table")
	assert.Equal(t, "tea", known.Name)
}

func TestDiscoverNowReportsACommandWithNoHelp(t *testing.T) {
	help := newFakeHelp(t, map[string]string{"echo": "echo a line\n"})

	_, err := help.DiscoverNow(t.Context(), "echo")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "printed no flags")
}

func TestDiscoverNowReportsAMissingCommand(t *testing.T) {
	help := newFakeHelp(t, nil)

	_, err := help.DiscoverNow(t.Context(), "nope")

	require.Error(t, err)
}

// The cache is what keeps discovery off the hot path: the second session reads
// the tree from disk and never runs the command.
func TestDiscovererReadsTheCacheWithoutRunning(t *testing.T) {
	dir := t.TempDir()
	first := newFakeHelp(t, map[string]string{"tea": cobraUsage})
	first.dir = dir
	_, err := first.DiscoverNow(t.Context(), "tea")
	require.NoError(t, err)
	require.Equal(t, []string{"tea"}, first.runs())

	second := newFakeHelp(t, map[string]string{"tea": cobraUsage})
	second.dir = dir
	second.Warm([]string{"tea"})

	_, ok := second.Table().Lookup("tea", nil)
	assert.True(t, ok, "a warm table knows the command")
	assert.Empty(t, second.runs(), "the cache answered, so nothing ran")
}

func TestDiscovererRejectsAStaleCache(t *testing.T) {
	dir := t.TempDir()
	first := newFakeHelp(t, map[string]string{"tea": cobraUsage})
	first.dir = dir
	_, err := first.DiscoverNow(t.Context(), "tea")
	require.NoError(t, err)

	second := newFakeHelp(t, map[string]string{"tea": cobraUsage})
	second.dir = dir
	second.now = func() time.Time { return time.Now().Add(30 * 24 * time.Hour) }
	second.Warm([]string{"tea"})

	_, ok := second.Table().Lookup("tea", nil)
	assert.True(t, ok, "a stale file still loads")
	assert.False(t, second.Warmed("tea-other"))
	assert.False(t, second.cachedFresh("tea"), "but it does not stop a refresh")
}

func TestDiscovererRejectsACorruptCache(t *testing.T) {
	dir := t.TempDir()
	discovery := NewDiscoverer(dir)
	discovery.Base = NewTable()
	discovery.Cached = NewTable
	require.NoError(t, os.WriteFile(discovery.cachePath("tea"), []byte("{not json"), 0o600))

	discovery.Warm([]string{"tea"})

	_, ok := discovery.Table().Lookup("tea", nil)
	assert.False(t, ok, "a corrupt file is an empty one")
}

// A command already known needs no discovery, which is the check that keeps a
// keystroke from spawning anything for git or go.
func TestWarmedReportsKnownCommands(t *testing.T) {
	discovery := NewDiscoverer(t.TempDir())

	assert.True(t, discovery.Warmed("git"), "the embedded specs count as warm")
	assert.False(t, discovery.Warmed("tea"))
}

func TestDiscoverRunsInTheBackground(t *testing.T) {
	help := newFakeHelp(t, map[string]string{"tea": cobraUsage})

	help.Discover(t.Context(), "tea")

	require.Eventually(t, func() bool {
		_, ok := help.Table().Lookup("tea", nil)
		return ok
	}, 2*time.Second, 10*time.Millisecond, "the discovery lands without the caller waiting")
}

// A discovery in flight is not started twice: two keystrokes in a row are one
// process.
func TestDiscoverWithAReadInFlight(t *testing.T) {
	help := newFakeHelp(t, map[string]string{"tea": cobraUsage})
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var runs int
	var mu sync.Mutex
	help.Run = func(context.Context, string) (string, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		started <- struct{}{}
		<-release
		return cobraUsage, nil
	}

	help.Discover(t.Context(), "tea")
	<-started
	help.Discover(t.Context(), "tea")
	close(release)

	require.Eventually(t, func() bool {
		_, ok := help.Table().Lookup("tea", nil)
		return ok
	}, 2*time.Second, 10*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, runs, "the second keystroke joined the read already in flight")
}

func TestHelpEnabledDefaultsOff(t *testing.T) {
	t.Setenv(EnvHelp, "")
	assert.False(t, HelpEnabled(), "reading help runs processes, so it stays off")

	for _, value := range []string{"1", "true", "on", "yes", "ON"} {
		t.Setenv(EnvHelp, value)
		assert.True(t, HelpEnabled(), value)
	}
	for _, value := range []string{"0", "off", "no", "maybe"} {
		t.Setenv(EnvHelp, value)
		assert.False(t, HelpEnabled(), value)
	}
}
