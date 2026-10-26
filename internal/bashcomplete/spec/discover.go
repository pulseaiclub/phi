package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// EnvHelp is the opt-in switch for reading command help. Discovery runs
// processes, so it stays off unless the switch says on: the embedded specs and
// history keep completing exactly as before while it is unset.
const EnvHelp = "PHI_COMPLETION_HELP"

// HelpEnabled reports whether help discovery should be wired up at all.
func HelpEnabled() bool {
	value := os.Getenv(EnvHelp)
	if value == "" {
		return false
	}
	switch foldName(trimSpace(value)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// DefaultCacheDir is where discovered trees are kept: ~/.phi/completion.
func DefaultCacheDir() string {
	if override := trimSpace(os.Getenv("PHI_COMPLETION_CACHE")); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".phi", "completion")
}

const (
	// HelpTimeout bounds one --help run. A command that has not printed its
	// usage by then is a command whose completion is not worth the wait.
	HelpTimeout = 3 * time.Second
	// helpOutputLimit bounds what is read back, so a command that streams
	// forever cannot grow the memory this process holds on every keystroke.
	helpOutputLimit = 256 << 10
	// cacheVersion guards the on-disk layout: a change here makes old files
	// stale rather than misread.
	cacheVersion = 1
)

// Discoverer reads command trees out of the commands' own --help output and
// remembers them on disk.
//
// Discovery never runs on the completion path. A keystroke asks for a table
// that is already in memory; when a command is missing it returns what it has
// and, with Warmed, starts a background run whose result lands in the table
// the next keystroke reads.
type Discoverer struct {
	// dir is where parsed trees are cached.
	dir string
	// Base is the table every discovery starts from: the embedded specs.
	Base *Table
	// Cached returns trees known before any discovery: the embedded specs.
	// nil means Builtin.
	Cached func() *Table
	// Lookup finds a command's path on disk; nil means exec.LookPath.
	Lookup func(string) (string, error)
	// Run executes one command and returns its help output; nil means a
	// bounded subprocess.
	Run func(ctx context.Context, path string) (string, error)
	// now is the clock for cache freshness; nil means time.Now.
	now func() time.Time
	// ttl is how long a cached tree is trusted before it is refreshed; zero
	// keeps every cache file forever.
	ttl time.Duration

	mu      sync.Mutex
	table   *Table
	loading map[string]bool
}

// NewDiscoverer builds a discoverer over a cache directory, starting from the
// embedded specs. A cache directory that cannot be written costs a
// re-discovery next session, never a completion.
func NewDiscoverer(dir string) *Discoverer {
	return &Discoverer{
		dir:     dir,
		Cached:  Builtin,
		now:     time.Now,
		ttl:     7 * 24 * time.Hour,
		loading: make(map[string]bool),
	}
}

// Table returns the table completions should read: the embedded specs plus
// every tree discovered so far. The first call reads the cache from disk, which
// is why callers build a discoverer before the first keystroke, not during it.
func (d *Discoverer) Table() *Table {
	if d == nil {
		return Builtin()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.table == nil {
		known := d.Base
		if known == nil {
			known = d.Cached()
		}
		d.table = Merge(known, nil)
	}
	return d.table
}

// Warm loads the cached trees for names into memory, stale ones included: a
// tree read last week still describes most flags, and Discover refreshes it in
// the background rather than making the user wait for a process. This is the
// one call worth making off the hot path.
func (d *Discoverer) Warm(names []string) {
	if d == nil || d.dir == "" || len(names) == 0 {
		return
	}
	extra := NewTable()
	for _, name := range names {
		if node, ok := d.cached(name); ok {
			extra.set(node)
		}
	}
	if len(extra.Names()) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.table = Merge(d.table, extra)
}

// Warmed reports whether the table already knows the command. Knowing it does
// not stop a refresh: Discover still checks freshness, so a stale tree is
// replaced in the background while it keeps answering.
func (d *Discoverer) Warmed(name string) bool {
	_, ok := d.Table().Lookup(name, nil)
	return ok
}

// Discover starts a background run of `name --help` unless one is already in
// flight or the cache holds a fresh answer. Run is the only entry point that
// spawns a process.
func (d *Discoverer) Discover(ctx context.Context, name string) {
	if d == nil || name == "" {
		return
	}
	if d.cachedFresh(name) {
		return
	}
	// A stale cache is still an answer: it loads, and the read below refreshes
	// it for the next session. The keystroke never waits either way.
	d.mu.Lock()
	if d.loading[name] {
		d.mu.Unlock()
		return
	}
	d.loading[name] = true
	d.mu.Unlock()

	go func() {
		defer func() {
			d.mu.Lock()
			delete(d.loading, name)
			d.mu.Unlock()
		}()
		// The discovery outlives the keystroke that started it: a cancelled
		// request would mean starting over on the next one.
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), HelpTimeout)
		defer cancel()
		node, err := d.discover(runCtx, name)
		if err != nil || node.Name == "" {
			return
		}
		d.store(node)
	}()
}

// DiscoverNow runs one discovery synchronously and folds the result into the
// table. Tests and the first-run path use it; the completion path does not.
func (d *Discoverer) DiscoverNow(ctx context.Context, name string) (Command, error) {
	if d == nil || name == "" {
		return Command{}, errors.New("no command to discover")
	}
	runCtx, cancel := context.WithTimeout(ctx, HelpTimeout)
	defer cancel()
	node, err := d.discover(runCtx, name)
	if err != nil {
		return Command{}, err
	}
	if node.Name == "" {
		return Command{}, fmt.Errorf("%s --help printed no flags", name)
	}
	d.store(node)
	return node, nil
}

// discover runs the command and parses what it printed.
func (d *Discoverer) discover(ctx context.Context, name string) (Command, error) {
	path, err := d.resolve(name)
	if err != nil {
		return Command{}, err
	}
	output, err := d.execute(ctx, path)
	if err != nil {
		return Command{}, err
	}
	node := FromHelp(name, output)
	if node.Name == "" {
		return Command{}, fmt.Errorf("%s --help printed no flags to complete", name)
	}
	node.Name = name
	return node, nil
}

// resolve finds the executable, preferring the caller's lookup.
func (d *Discoverer) resolve(name string) (string, error) {
	if d.Lookup != nil {
		return d.Lookup(name)
	}
	return exec.LookPath(name)
}

// execute runs one command's --help, bounded in time and in bytes read.
func (d *Discoverer) execute(ctx context.Context, path string) (string, error) {
	if d.Run != nil {
		return d.Run(ctx, path)
	}
	cmd := exec.CommandContext(ctx, path, "--help") //nolint:gosec // G204: the user typed the command name
	cmd.WaitDelay = time.Second
	out, err := limitedOutput(cmd)
	if err != nil && out == "" {
		// A tool that prints no usage and exits non-zero has nothing to say
		// about its flags.
		return "", err
	}
	return out, nil
}

// store writes the tree to the cache and into the live table.
func (d *Discoverer) store(node Command) {
	d.mu.Lock()
	d.table = Merge(d.table, tableOf(node))
	d.mu.Unlock()
	if err := d.writeCache(node); err != nil {
		// A cache that cannot be written costs a re-discovery next session,
		// never a completion.
		return
	}
}

// tableOf is a one-command table, so Merge can fold a single tree in.
func tableOf(node Command) *Table {
	table := NewTable()
	table.set(node)
	return table
}

// cachePath is where one command's tree is kept.
func (d *Discoverer) cachePath(name string) string {
	return filepath.Join(d.dir, "help-"+foldName(name)+".json")
}

// cachedFresh reports whether the cache holds a tree that is still trusted.
func (d *Discoverer) cachedFresh(name string) bool {
	if _, ok := d.cached(name); !ok {
		return false
	}
	if d.ttl <= 0 {
		return true
	}
	info, err := os.Stat(d.cachePath(name))
	if err != nil {
		return false
	}
	return d.now().Sub(info.ModTime()) <= d.ttl
}

// cached reads one command's tree from the cache directory.
func (d *Discoverer) cached(name string) (Command, bool) {
	if d.dir == "" {
		return Command{}, false
	}
	data, err := os.ReadFile(d.cachePath(name))
	if err != nil {
		return Command{}, false
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil || entry.Version != cacheVersion || entry.Command.Name == "" {
		return Command{}, false
	}
	return entry.Command, true
}

// writeCache stores one tree, atomically, so a crash mid-write leaves the old
// file rather than a truncated one.
func (d *Discoverer) writeCache(node Command) error {
	if d.dir == "" {
		return errors.New("no cache directory")
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return fmt.Errorf("create completion cache %s: %w", d.dir, err)
	}
	data, err := json.Marshal(cacheEntry{Version: cacheVersion, Command: node})
	if err != nil {
		return fmt.Errorf("encode command spec for %s: %w", node.Name, err)
	}
	path := d.cachePath(node.Name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write command spec for %s: %w", node.Name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("store command spec for %s: %w", node.Name, err)
	}
	return nil
}

// cacheEntry is the on-disk shape of one discovered tree.
type cacheEntry struct {
	Version int     `json:"version"`
	Command Command `json:"command"`
}
