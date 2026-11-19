package job

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	metaFile   = "meta.json"
	eventsFile = "events.jsonl"
	resultFile = "result.md"
)

type store interface {
	create(meta Meta) (Meta, error)
	listIDs() ([]string, error)
	readMeta(id string) (Meta, error)
	writeMeta(meta Meta) error
	appendEvent(meta Meta, msg string) error
	readEvents(meta Meta, limit int) ([]Event, error)
	writeResult(meta Meta, summary string) error
	readResult(meta Meta) (string, error)
}

type diskStore struct {
	root string
}

func newDiskStore(root string) (*diskStore, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: root directory is required", ErrInvalid)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &diskStore{root: root}, nil
}

func (s *diskStore) create(meta Meta) (Meta, error) {
	dir := filepath.Join(s.root, meta.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Meta{}, err
	}
	meta.Dir = dir
	meta.ResultPath = filepath.Join(dir, resultFile)
	meta.EventsPath = filepath.Join(dir, eventsFile)
	if err := s.writeMeta(meta); err != nil {
		return Meta{}, err
	}
	f, err := os.OpenFile(meta.EventsPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Meta{}, err
	}
	_ = f.Close()
	return meta, nil
}

func (*diskStore) writeMeta(meta Meta) error {
	path := filepath.Join(meta.Dir, metaFile)
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // G306: job meta is local tooling state
		return err
	}
	return os.Rename(tmp, path)
}

func (s *diskStore) readMeta(id string) (Meta, error) {
	path := filepath.Join(s.root, id, metaFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Meta{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return Meta{}, err
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

func (*diskStore) appendEvent(meta Meta, msg string) error {
	ev := Event{Time: time.Now().UTC(), Message: msg}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(meta.EventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (*diskStore) readEvents(meta Meta, limit int) ([]Event, error) {
	data, err := os.ReadFile(meta.EventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	out := make([]Event, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (*diskStore) writeResult(meta Meta, summary string) error {
	//nolint:gosec // G306: job results are local tooling state
	return os.WriteFile(meta.ResultPath, []byte(summary), 0o644)
}

func (*diskStore) readResult(meta Meta) (string, error) {
	data, err := os.ReadFile(meta.ResultPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (s *diskStore) listIDs() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

func newJobID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("job_%s_%s", time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(b[:])), nil
}
