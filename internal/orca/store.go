package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pulseaiclub/phi/internal/llm"
)

// credentialFileVersion guards the on-disk shape so a future change can detect
// and migrate an older file instead of misreading it.
const credentialFileVersion = 1

// credentialRecord is the persisted form. It deliberately holds only what the
// project already had a place for — one secret plus the account it belongs to.
type credentialRecord struct {
	Version     int    `json:"version"`
	APIKey      string `json:"api_key"`
	AccountID   string `json:"account_id,omitempty"`
	Source      string `json:"source,omitempty"`
	NeedsReauth bool   `json:"needs_reauth,omitempty"`
}

// Store persists the OrcaRouter credential and hands out the current one.
//
// It is the project's existing secret mechanism, not a new keychain: the value
// lands in one file under the phi home directory that already holds config,
// sessions, and jobs, written with owner-only permissions. It also fronts the
// ORCA_API_KEY environment override, so an environment-only setup never needs
// a file at all.
type Store struct {
	path string
	// generation increments on every successful acquisition. A rejected
	// request carries the generation that made it, so a late 401 from an old
	// request cannot mark a newer key broken.
	generation int

	mu sync.Mutex
	// cached is the last record read or written, so Status does no I/O.
	cached *credentialRecord
	// loaded records whether cached reflects the file.
	loaded bool
}

// NewStore returns a store backed by path. An empty path disables persistence
// (environment-only operation).
func NewStore(path string) *Store { return &Store{path: path} }

// DefaultStorePath is ~/.phi/orcarouter.json, beside the rest of the phi state.
func DefaultStorePath(phiHome string) string {
	if phiHome == "" {
		return ""
	}
	return filepath.Join(phiHome, "orcarouter.json")
}

// Getenv returns the API key from the environment override, or "".
func Getenv() string { return strings.TrimSpace(os.Getenv(EnvAPIKey)) }

// SetAPIKey stores a hand-pasted key. It is the API-key adapter's write path:
// the same durable record the PKCE adapter writes, so downstream code cannot
// tell which choice produced it.
func (s *Store) SetAPIKey(key, accountID string) (llm.Credential, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return llm.Credential{}, errors.New("OrcaRouter API key is empty")
	}
	return s.put(credentialRecord{
		Version:   credentialFileVersion,
		APIKey:    key,
		AccountID: strings.TrimSpace(accountID),
		Source:    string(llm.CredentialSourceAPIKey),
	})
}

// SetPKCEKey stores a key obtained through the authorization-code flow.
func (s *Store) SetPKCEKey(key, accountID string) (llm.Credential, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return llm.Credential{}, errors.New("OrcaRouter returned an empty key")
	}
	return s.put(credentialRecord{
		Version:   credentialFileVersion,
		APIKey:    key,
		AccountID: strings.TrimSpace(accountID),
		Source:    string(llm.CredentialSourcePKCE),
	})
}

func (s *Store) put(rec credentialRecord) (llm.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if err := writeRecord(s.path, rec); err != nil {
			return llm.Credential{}, err
		}
	}
	s.generation++
	s.cached = &rec
	s.loaded = true
	return s.credential(s.cached), nil
}

// Clear removes the stored credential. The environment override is not ours to
// unset, so it keeps working; the caller is told which of the two is still in
// play through Status.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.cached = &credentialRecord{Version: credentialFileVersion}
	s.loaded = true
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", s.path, err)
	}
	return nil
}

// Credential returns the current credential. It performs no network I/O: a
// hand-pasted key cannot be validated without spending a request, so validity
// stays unknown until the first real call, exactly as the project treats every
// other provider secret.
func (s *Store) Credential(_ context.Context) (llm.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.record()
	if err != nil {
		return llm.Credential{}, err
	}
	cred := s.credential(rec)
	if cred.APIKey == "" {
		return llm.Credential{}, ErrNoCredential
	}
	return cred, nil
}

// Status returns the redacted view for display.
func (s *Store) Status(_ context.Context) llm.CredentialStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.record()
	if err != nil {
		return llm.CredentialStatus{}
	}
	cred := s.credential(rec)
	if cred.APIKey == "" {
		return llm.CredentialStatus{}
	}
	return llm.CredentialStatus{
		Present:     true,
		Source:      cred.Source,
		AccountID:   cred.AccountID,
		Masked:      llm.MaskSecret(cred.APIKey),
		NeedsReauth: cred.NeedsReauth,
	}
}

// MarkUnauthorized records that the relay rejected generation gen with 401.
// Only that exact generation is affected: if a new login has already replaced
// the credential, the stale failure is dropped instead of marking the fresh
// key unusable. The stored secret is kept — a transient or misclassified
// failure must not destroy a credential the user cannot re-obtain offline.
func (s *Store) MarkUnauthorized(gen int) (marked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.generation || s.cached == nil || s.cached.APIKey == "" {
		return false
	}
	if s.cached.NeedsReauth {
		return false
	}
	s.cached.NeedsReauth = true
	if s.path != "" {
		// Best effort: the in-memory flag already blocks reuse.
		_ = writeRecord(s.path, *s.cached)
	}
	return true
}

// Generation reports the current credential generation.
func (s *Store) Generation() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// ErrNoCredential reports that the user has not supplied an OrcaRouter key.
var ErrNoCredential = errors.New(
	"no OrcaRouter credential: paste an API key or run 'phi auth login --orcarouter'")

func (s *Store) credential(rec *credentialRecord) llm.Credential {
	key := rec.APIKey
	source := llm.CredentialSource(rec.Source)
	// The environment override wins and is reported as the API-key source: it
	// is a key the user supplied, not one this process negotiated.
	if env := Getenv(); env != "" {
		key = env
		source = llm.CredentialSourceAPIKey
	}
	return llm.Credential{
		APIKey:      key,
		AccountID:   rec.AccountID,
		Source:      source,
		Generation:  s.generation,
		NeedsReauth: rec.NeedsReauth,
	}
}

// record returns the cached record, reading the file once on first use.
func (s *Store) record() (*credentialRecord, error) {
	if s.loaded {
		return s.cached, nil
	}
	s.loaded = true
	rec := &credentialRecord{Version: credentialFileVersion}
	s.cached = rec
	if s.path == "" {
		return rec, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return rec, nil
		}
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return rec, nil
	}
	var stored credentialRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		// A corrupt file must not silently become "no credential" or a crash
		// loop; it is reported and the caller can re-authenticate.
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if stored.APIKey != "" {
		s.generation++
	}
	*s.cached = stored
	return s.cached, nil
}

// writeRecord persists the record atomically with owner-only permissions, so a
// partially written file can never be read back as a credential.
func writeRecord(path string, rec credentialRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	//nolint:gosec // The credential store exists to persist the key; the file is written 0600.
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
