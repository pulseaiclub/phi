package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

// SessionMeta is a lightweight listing row for persisted sessions.
type SessionMeta struct {
	ID        string
	File      string
	Timestamp string
	Cwd       string
	Mtime     time.Time
	Preview   string // truncated last user text
}

// ListSessions returns session files under dir, newest mtime first.
// Callers should pass a per-cwd directory (e.g. project.SessionDir()), not the
// global session base, so listings stay scoped to the current project.
func ListSessions(dir string) ([]SessionMeta, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]SessionMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		meta, err := readSessionMeta(path, e)
		if err != nil {
			continue // skip unreadable / malformed files in listings
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Mtime.After(out[j].Mtime)
	})
	return out, nil
}

func readSessionMeta(path string, e os.DirEntry) (SessionMeta, error) {
	info, err := e.Info()
	if err != nil {
		return SessionMeta{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return SessionMeta{}, err
	}
	defer f.Close()

	meta := SessionMeta{
		File:  path,
		Mtime: info.ModTime(),
	}
	sc := bufio.NewScanner(f)
	// Session files can grow; allow large lines for tool payloads.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		entry, err := decodeEntryLine(line, lineNo)
		if err != nil {
			if meta.ID == "" {
				return SessionMeta{}, err
			}
			break
		}
		switch e := entry.(type) {
		case SessionHeader:
			meta.ID = e.ID
			meta.Timestamp = e.Timestamp
			meta.Cwd = e.Cwd
		case SessionMessageEntry:
			if e.Message.Role == llm.RoleUser && strings.TrimSpace(e.Message.Content) != "" {
				meta.Preview = truncatePreview(e.Message.Content, 72)
			}
		}
	}
	if meta.ID != "" {
		return meta, nil
	}
	// Fall back to filename id when header missing.
	id, ok := sessionIDFromFilename(filepath.Base(path))
	if !ok {
		return SessionMeta{}, fmt.Errorf("session: no header in %s", path)
	}
	meta.ID = id
	return meta, nil
}

func truncatePreview(s string, n int) string {
	s = util.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FindSessionFile resolves id to a unique jsonl path under dir.
// id may be exact or a unique prefix of the session id.
func FindSessionFile(dir, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("session: empty id")
	}
	if filepath.IsAbs(id) || strings.HasSuffix(id, ".jsonl") {
		if _, err := os.Stat(id); err != nil {
			return "", fmt.Errorf("session: file %q: %w", id, err)
		}
		return id, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var exact, prefix []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		sid, ok := sessionIDFromFilename(e.Name())
		if !ok {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if sid == id {
			exact = append(exact, path)
		} else if strings.HasPrefix(sid, id) {
			prefix = append(prefix, path)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return "", fmt.Errorf("session: ambiguous id %q (%d matches)", id, len(exact))
	}
	if len(prefix) == 1 {
		return prefix[0], nil
	}
	if len(prefix) > 1 {
		return "", fmt.Errorf("session: ambiguous id prefix %q (%d matches)", id, len(prefix))
	}
	return "", fmt.Errorf("session: id %q not found in %s", id, dir)
}

func sessionIDFromFilename(name string) (string, bool) {
	base := strings.TrimSuffix(name, ".jsonl")
	i := strings.IndexByte(base, '_')
	if i < 0 || i+1 >= len(base) {
		return "", false
	}
	return base[i+1:], true
}

// OpenSession loads a JSONL session file and returns a Manager ready to append.
func OpenSession(path string) (*Manager, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		entries         []MessageEntry
		byIDs           = make(map[string]MessageEntry, 64)
		header          *SessionHeader
		leafID          *string
		hasAssistantMsg bool
		lineNo          int
	)

	for sc.Scan() {
		lineNo++
		raw := sc.Bytes()
		if strings.TrimSpace(string(raw)) == "" {
			continue
		}
		entry, err := decodeEntryLine(raw, lineNo)
		if err != nil {
			return nil, err
		}
		switch e := entry.(type) {
		case SessionHeader:
			if header != nil {
				return nil, fmt.Errorf("session: duplicate header at %s:%d", path, lineNo)
			}
			h := e
			header = &h
			entries = append(entries, h)
		default:
			if header == nil {
				return nil, fmt.Errorf("session: first entry must be session header at %s:%d", path, lineNo)
			}
			id := entry.GetID()
			byIDs[id] = entry
			entries = append(entries, entry)
			leafID = &id
			if msg, ok := entry.(SessionMessageEntry); ok && msg.Message.Role == llm.RoleAssistant {
				hasAssistantMsg = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("session: read %s: %w", path, err)
	}
	if header == nil {
		return nil, fmt.Errorf("session: missing header in %s", path)
	}

	parent := header.ParentSession
	return &Manager{
		cwd:         header.Cwd,
		entries:     entries,
		byIDs:       byIDs,
		sessionFile: path,
		leafID:      leafID,
		shouldFlush: true,
		flushed:     true,
		sessionID:   header.ID,
		config: ManagerConfig{
			sessionDir:  filepath.Dir(path),
			shouldFlush: true,
			parentID:    parent,
		},
		hasAssistantMsg: hasAssistantMsg,
	}, nil
}

func decodeEntryLine(raw []byte, lineNo int) (MessageEntry, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("session: line %d: %w", lineNo, err)
	}
	switch probe.Type {
	case EntrySession, "session":
		var h SessionHeader
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, fmt.Errorf("session: line %d header: %w", lineNo, err)
		}
		h.Type = EntrySession
		return h, nil
	case EntryMessage:
		var m SessionMessageEntry
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("session: line %d message: %w", lineNo, err)
		}
		return m, nil
	case EntryCompaction:
		var c CompactionEntry
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("session: line %d compaction: %w", lineNo, err)
		}
		return c, nil
	default:
		return nil, fmt.Errorf("session: line %d: unknown type %q", lineNo, probe.Type)
	}
}

// ID returns the session identifier from the header.
func (sm *Manager) ID() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.sessionID
}

// File returns the JSONL path, or empty when not persisting.
func (sm *Manager) File() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.sessionFile
}

// Cwd returns the session working directory recorded in the header.
func (sm *Manager) Cwd() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.cwd
}

// LeafID returns the current leaf entry id, or empty if none.
func (sm *Manager) LeafID() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.leafID == nil {
		return ""
	}
	return *sm.leafID
}
