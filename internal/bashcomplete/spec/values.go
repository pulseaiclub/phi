package spec

import (
	"context"
	"sync"
	"time"
)

// valuesTTL is how long one argument read is trusted. The values it lists are
// the repository's own state, and a commit made a minute ago is worth seeing;
// the read is cheap enough that a short window costs nothing.
const valuesTTL = 30 * time.Second

// Values serves the rows a declared argument generator produces, off the
// keystroke: the completion path takes what is cached and starts a read when
// the cache is empty, exactly as help discovery does for flags.
type Values struct {
	// dir is the directory argument reads run in, usually the project root.
	// A read in another directory is not cached.
	dir string
	mu  sync.Mutex
	// cache is keyed by generator name and the word being completed, so a
	// prefix that was typed before answers at once.
	cache map[valuesKey]valuesEntry
	// loading tracks the reads in flight, so two keystrokes are one process.
	loading map[valuesKey]bool
}

type valuesKey struct {
	generator string
	prefix    string
}

type valuesEntry struct {
	rows     []Candidate
	readAt   time.Time
	complete bool
}

// NewValues builds a reader cache over one directory.
func NewValues(dir string) *Values {
	return &Values{
		dir:     dir,
		cache:   make(map[valuesKey]valuesEntry),
		loading: make(map[valuesKey]bool),
	}
}

// Rows returns the cached rows for a generator and prefix that continue it. A
// short cache entry is read from a narrower one when available: the rows for
// "23" are the rows for "" whose names start with "23", so a keystroke does not
// wait for git just because the prefix grew.
func (v *Values) Rows(generator, prefix string) []Candidate {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if entry, ok := v.cache[valuesKey{generator, prefix}]; ok && time.Since(entry.readAt) <= valuesTTL {
		return entry.rows
	}
	// A shorter prefix answers this one without a read: narrowing a list that
	// already exists is what every keystroke after the first wants.
	for key, entry := range v.cache {
		if key.generator != generator || key.prefix == prefix {
			continue
		}
		if !literalPrefix(prefix, key.prefix) || key.prefix == prefix || !entry.complete {
			continue
		}
		if time.Since(entry.readAt) > valuesTTL {
			continue
		}
		return narrow(entry.rows, prefix)
	}
	return nil
}

// Refresh starts a background read for a generator and prefix, unless a read is
// already in flight or the cache is fresh. The keystroke that starts it answers
// from Rows.
func (v *Values) Refresh(ctx context.Context, generator, prefix string, read Generator) {
	if v == nil || read == nil {
		return
	}
	key := valuesKey{generator, prefix}
	v.mu.Lock()
	if entry, ok := v.cache[key]; ok && time.Since(entry.readAt) <= valuesTTL {
		v.mu.Unlock()
		return
	}
	if v.loading[key] {
		v.mu.Unlock()
		return
	}
	v.loading[key] = true
	v.mu.Unlock()

	go func() {
		defer func() {
			v.mu.Lock()
			delete(v.loading, key)
			v.mu.Unlock()
		}()
		// The read outlives the keystroke that asked for it; a cancelled
		// request would mean starting over on the next one.
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), generatorTimeout)
		defer cancel()
		// A read lists every value; narrowing happens on the way out, so one
		// read serves every prefix the user types next.
		rows := read(runCtx, v.dir, "")
		v.store(key, rows, true)
	}()
}

// Load reads synchronously, for tests and for callers that already have a
// reason to pay for it.
func (v *Values) Load(ctx context.Context, generator, prefix string, read Generator) []Candidate {
	if v == nil || read == nil {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, generatorTimeout)
	defer cancel()
	rows := read(runCtx, v.dir, prefix)
	// Only a read of the whole list can answer a narrower prefix later: a read
	// already narrowed to a prefix has nothing to give another one.
	v.store(valuesKey{generator, prefix}, rows, prefix == "")
	return rows
}

// store records one read, dropping entries old enough to be stale.
func (v *Values) store(key valuesKey, rows []Candidate, complete bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	for k, entry := range v.cache {
		if now.Sub(entry.readAt) > valuesTTL {
			delete(v.cache, k)
		}
	}
	v.cache[key] = valuesEntry{rows: rows, readAt: now, complete: complete}
}

// narrow keeps the rows whose name extends prefix, preserving their order.
func narrow(rows []Candidate, prefix string) []Candidate {
	out := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		if literalPrefix(row.Name, prefix) {
			out = append(out, row)
		}
	}
	return out
}
