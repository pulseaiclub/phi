package shellhist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// EnvPrompts is the opt-in switch for composer prompt recall:
	// PHI_PROMPT_HISTORY=on. Recording prompts writes a new file under the
	// user's home directory, so the feature stays off until the user asks for
	// it, and nothing is written on a plain run.
	EnvPrompts = "PHI_PROMPT_HISTORY"

	// maxPrompts is how many user inputs the composer can recall.
	maxPrompts = 10
	// maxPromptBytes caps a single recalled input. A longer prompt is truncated
	// rather than rejected: history must never fail a submit.
	maxPromptBytes = 64 << 10
	promptFile     = "prompts.json"
)

// PromptsEnabled reports whether prompt recall should be wired up at all. The
// switch is intentionally default-off: on is the only state that may create
// prompt history files.
func PromptsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvPrompts))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// promptDoc is the on-disk shape. A single JSON object keeps the replacement
// atomic and the file human-inspectable.
type promptDoc struct {
	Version int      `json:"v"`
	Prompts []string `json:"prompts"`
}

// PromptHistory stores the project's most recent user inputs so the composer
// can recall them with Up/Down. Slash commands and shell commands are filtered
// by the caller; this store only knows about text.
type PromptHistory struct {
	dir string
	mu  sync.Mutex
}

// NewPromptHistory stores prompt recall in the per-project session directory.
func NewPromptHistory(dir string) *PromptHistory { return &PromptHistory{dir: dir} }

// Entries returns the recalled inputs, oldest first. A missing or corrupt file
// yields nil: a damaged history must not block the composer.
func (p *PromptHistory) Entries() []string {
	var entries []string
	err := p.locked(func() error {
		var err error
		entries, err = p.read()
		return err
	})
	if err != nil {
		return nil
	}
	return entries
}

// Append records text as the newest input. Blank text and a repeat of the
// newest entry are ignored, so holding Enter on the same prompt does not fill
// the ring with duplicates.
func (p *PromptHistory) Append(text string) error {
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) {
		return nil
	}
	text = truncatePrompt(text)
	return p.locked(func() error {
		entries, err := p.read()
		if err != nil {
			return err
		}
		if len(entries) > 0 && entries[len(entries)-1] == text {
			return nil
		}
		entries = append(entries, text)
		if len(entries) > maxPrompts {
			entries = entries[len(entries)-maxPrompts:]
		}
		return p.write(entries)
	})
}

// locked runs fn holding both locks: p.mu serializes callers in this process,
// the file lock serializes other phi processes in the same project. The file
// lock has to span read+modify+write, otherwise two processes each read the
// same list and the later write silently drops the earlier append.
func (p *PromptHistory) locked(fn func() error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return fmt.Errorf("create prompt history directory: %w", err)
	}
	// #nosec G703 -- Fixed filename under the caller's canonical sessions directory.
	lock, err := os.OpenFile(filepath.Join(p.dir, promptFile+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open prompt history lock: %w", err)
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("lock prompt history: %w", err)
	}
	defer unlockFile(lock)
	return fn()
}

func (p *PromptHistory) read() ([]string, error) {
	// #nosec G703 -- The caller supplies the canonical project sessions directory.
	data, err := os.ReadFile(filepath.Join(p.dir, promptFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read prompt history: %w", err)
	}
	var doc promptDoc
	if err := json.Unmarshal(data, &doc); err != nil || doc.Version != 1 {
		// A hand-edited or truncated file is history, not state: drop it.
		return nil, nil
	}
	entries := make([]string, 0, len(doc.Prompts))
	for _, s := range doc.Prompts {
		if s = strings.TrimSpace(s); s != "" {
			entries = append(entries, s)
		}
	}
	if len(entries) > maxPrompts {
		entries = entries[len(entries)-maxPrompts:]
	}
	return entries, nil
}

// write replaces the file via a temp file + rename, so a crash mid-write leaves
// the previous history intact instead of a half-written list.
func (p *PromptHistory) write(entries []string) error {
	data, err := json.Marshal(promptDoc{Version: 1, Prompts: entries})
	if err != nil {
		return fmt.Errorf("encode prompt history: %w", err)
	}
	tmp, err := os.CreateTemp(p.dir, promptFile+".tmp*")
	if err != nil {
		return fmt.Errorf("create prompt history temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write prompt history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close prompt history: %w", err)
	}
	// #nosec G703 -- Both paths are fixed filenames under the caller's directory.
	if err := os.Rename(tmpName, filepath.Join(p.dir, promptFile)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace prompt history: %w", err)
	}
	return nil
}

// truncatePrompt cuts s to maxPromptBytes without splitting a rune.
func truncatePrompt(s string) string {
	if len(s) <= maxPromptBytes {
		return s
	}
	cut := maxPromptBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut])
}
