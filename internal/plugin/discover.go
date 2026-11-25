package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type descriptor struct {
	name    string
	version string
	entry   string
}

type manifest struct {
	Name          string `yaml:"name"`
	Version       string `yaml:"version"`
	Entry         string `yaml:"entry"`
	MinPhiVersion string `yaml:"min_phi_version"`
}

type numericVersion [3]int

// discover returns valid plugins in root order and directory order. A bad
// plugin is isolated so one broken manifest does not hide the remaining set.
func discover(currentVersion string, roots ...string) ([]descriptor, []error) {
	var (
		plugins []descriptor
		errs    []error
		seen    = make(map[string]struct{})
	)

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: read plugin root: %w", root, err))
			continue
		}

		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			pluginDir := filepath.Join(root, name)
			if !entry.IsDir() {
				continue
			}

			manifestPath := filepath.Join(pluginDir, "plugin.yaml")
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				if os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("%s: manifest does not exist", manifestPath))
					continue
				}
				errs = append(errs, fmt.Errorf("%s: read manifest: %w", manifestPath, err))
				continue
			}

			m, err := decodeManifest(data)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", manifestPath, err))
				continue
			}

			plugin, err := makeDescriptor(currentVersion, pluginDir, name, m)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", manifestPath, err))
				continue
			}
			if _, duplicate := seen[plugin.name]; duplicate {
				errs = append(errs, fmt.Errorf("%s: duplicate plugin name %q", manifestPath, plugin.name))
				continue
			}
			seen[plugin.name] = struct{}{}
			plugins = append(plugins, plugin)
		}
	}

	return plugins, errs
}

func decodeManifest(data []byte) (manifest, error) {
	var m manifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return manifest{}, errors.New("parse manifest: multiple YAML documents")
		}
		return manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

func makeDescriptor(currentVersion, pluginDir, dirName string, m manifest) (descriptor, error) {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = dirName
	}

	if m.MinPhiVersion != "" {
		minimum, err := parseVersion(m.MinPhiVersion, false)
		if err != nil {
			return descriptor{}, fmt.Errorf("invalid min_phi_version %q: %w", m.MinPhiVersion, err)
		}
		current, err := parseVersion(currentVersion, true)
		if err != nil {
			return descriptor{}, fmt.Errorf("invalid current phi version %q: %w", currentVersion, err)
		}
		if slices.Compare(current[:], minimum[:]) < 0 {
			return descriptor{}, fmt.Errorf("requires phi >= %s (current %s)", m.MinPhiVersion, currentVersion)
		}
	}

	entry := m.Entry
	if entry == "" {
		entry = "init.lua"
	}
	entryPath, err := validateEntry(pluginDir, entry)
	if err != nil {
		return descriptor{}, err
	}
	return descriptor{name: name, version: m.Version, entry: entryPath}, nil
}

func validateEntry(pluginDir, entry string) (string, error) {
	if filepath.IsAbs(entry) || !strings.HasSuffix(entry, ".lua") {
		return "", fmt.Errorf("entry %q must be a relative .lua path", entry)
	}

	pluginDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return "", fmt.Errorf("resolve plugin directory: %w", err)
	}
	pluginInfo, err := os.Lstat(pluginDir)
	if err != nil {
		return "", fmt.Errorf("resolve plugin directory: %w", err)
	}
	if pluginInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("plugin directory is a symlink")
	}
	entryPath := filepath.Clean(filepath.Join(pluginDir, entry))
	relative, err := filepath.Rel(pluginDir, entryPath)
	if err != nil {
		return "", fmt.Errorf("resolve entry %q: %w", entry, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entry %q escapes plugin directory", entry)
	}

	current := pluginDir
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("entry %q: %w", entry, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("entry %q uses symlink %s", entry, current)
		}
	}
	info, err := os.Stat(entryPath)
	if err != nil {
		return "", fmt.Errorf("entry %q: %w", entry, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("entry %q is not a regular file", entry)
	}
	return entryPath, nil
}

func parseVersion(value string, allowVPrefix bool) (numericVersion, error) {
	if allowVPrefix && strings.HasPrefix(value, "v") {
		value = value[1:]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return numericVersion{}, errors.New("must be numeric x.y.z")
	}
	var parsed numericVersion
	for i, part := range parts {
		if part == "" {
			return numericVersion{}, errors.New("must be numeric x.y.z")
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return numericVersion{}, errors.New("must be numeric x.y.z")
			}
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return numericVersion{}, fmt.Errorf("must be numeric x.y.z: %w", err)
		}
		parsed[i] = number
	}
	return parsed, nil
}
