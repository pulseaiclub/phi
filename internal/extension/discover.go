package extension

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source labels where a discovered extension came from.
const (
	SourceUser    = "user"
	SourceProject = "project"
)

// EnvExtensions disables extension loading when set to "off".
const EnvExtensions = "PHI_EXTENSIONS"

// Warning is a non-fatal discovery or load problem.
type Warning struct {
	Path    string
	Message string
}

func (w Warning) String() string {
	if w.Path == "" {
		return w.Message
	}
	return w.Path + ": " + w.Message
}

// Discovered is one extension directory with a phi.yaml manifest.
type Discovered struct {
	ID       string // directory name / manifest name
	Path     string // absolute path to extension directory
	Manifest Manifest
	Source   string
}

// ExtensionsDisabled reports whether PHI_EXTENSIONS=off.
func ExtensionsDisabled() bool {
	v := strings.TrimSpace(os.Getenv(EnvExtensions))
	return strings.EqualFold(v, "off")
}

// Discover finds extension directories under userDir then projectDir.
// Same ID: project replaces user. Layout:
//
//	<dir>/<id>/phi.yaml   (exec points at a PXB binary)
func Discover(userDir, projectDir string) ([]Discovered, []Warning, error) {
	if ExtensionsDisabled() {
		return nil, nil, nil
	}

	byID := make(map[string]Discovered)
	var warnings []Warning

	load := func(dir, source string) error {
		if dir == "" {
			return nil
		}
		found, warns, err := scanDir(dir, source)
		warnings = append(warnings, warns...)
		if err != nil {
			return err
		}
		for _, d := range found {
			byID[d.ID] = d
		}
		return nil
	}

	if err := load(userDir, SourceUser); err != nil {
		return nil, warnings, err
	}
	if err := load(projectDir, SourceProject); err != nil {
		return nil, warnings, err
	}

	out := make([]Discovered, 0, len(byID))
	for _, d := range byID {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, warnings, nil
}

func scanDir(dir, source string) ([]Discovered, []Warning, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("extension: read dir %s: %w", dir, err)
	}

	var (
		out      []Discovered
		warnings []Warning
		seen     = make(map[string]string)
	)

	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		// Stat follows symlinks so `ln -s examples/foo .phi/extensions/foo` works.
		// ReadDir's IsDir is false for symlink entries on Unix.
		st, err := os.Stat(full)
		if err != nil || !st.IsDir() {
			continue
		}
		m, err := ReadManifest(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			warnings = append(warnings, Warning{Path: full, Message: err.Error()})
			continue
		}
		if !m.IsEnabled() {
			continue
		}
		id := m.Name
		if id == "" {
			id = name
		}
		if prev, dup := seen[id]; dup {
			warnings = append(warnings, Warning{
				Path:    full,
				Message: fmt.Sprintf("duplicate extension %q (already %s); skipped", id, prev),
			})
			continue
		}
		seen[id] = full
		out = append(out, Discovered{ID: id, Path: full, Manifest: m, Source: source})
	}
	return out, warnings, nil
}

// FormatDiscovered returns a one-line status for palette / logs.
func FormatDiscovered(d Discovered) string {
	return fmt.Sprintf("%s  [%s]  %s", d.ID, d.Source, d.Path)
}
