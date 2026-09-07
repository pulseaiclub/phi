package extension

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// installMetaFile is the sidecar written into a phi-managed extension dir.
// Its absence marks a manual install: update/remove refuse to touch it.
const installMetaFile = ".phi-install.json"

// Install source labels recorded in InstallMeta.
const (
	SourceRelease = "release"
	SourceGit     = "git"
)

// InstallMeta records where a phi-installed extension came from, so plugin
// update/list/remove can re-resolve the same GitHub source.
type InstallMeta struct {
	Spec        Spec      `json:"spec"`
	Source      string    `json:"source"`                // release | git
	ReleaseTag  string    `json:"release_tag,omitempty"` // installed tag when source=release
	InstalledAt time.Time `json:"installed_at"`
}

func readInstallMeta(dir string) (InstallMeta, error) {
	path := filepath.Join(dir, installMetaFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return InstallMeta{}, err
	}
	var m InstallMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return InstallMeta{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if m.Spec.Owner == "" || m.Spec.Repo == "" || m.Source == "" {
		return InstallMeta{}, fmt.Errorf("%s: missing owner/repo or source", path)
	}
	return m, nil
}

func writeInstallMeta(dir string, m InstallMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, installMetaFile), b, 0o600)
}

// Installed is one extension directory with its install metadata (when managed).
type Installed struct {
	ID       string
	Path     string
	Manifest Manifest
	Meta     InstallMeta
	Managed  bool // true when phi plugin install wrote install metadata
}

// ListInstalled returns extension directories under dir with their install
// metadata, sorted by ID. Dirs without a phi.yaml are skipped; hidden entries
// are ignored (same rules as discovery).
func ListInstalled(dir string) ([]Installed, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("extension: read dir %s: %w", dir, err)
	}
	var out []Installed
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		st, err := os.Stat(full)
		if err != nil || !st.IsDir() {
			continue
		}
		m, err := ReadManifest(full)
		if err != nil {
			continue
		}
		meta, metaErr := readInstallMeta(full)
		out = append(out, Installed{
			ID:       m.Name,
			Path:     full,
			Manifest: m,
			Meta:     meta,
			Managed:  metaErr == nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
