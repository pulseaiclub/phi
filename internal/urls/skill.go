package urls

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm/skills"
)

type skillLocation struct {
	root, file string
}

type skillHandler struct {
	locations map[string]skillLocation
}

// NewSkillRouter snapshots skill names and paths. Nil entries are ignored and
// the first entry for a duplicate name wins, matching skill discovery order.
func NewSkillRouter(list []*skills.Skill) *Router {
	handler := &skillHandler{locations: make(map[string]skillLocation, len(list))}
	for _, skill := range list {
		if skill == nil {
			continue
		}
		if _, exists := handler.locations[skill.Name]; exists {
			continue
		}
		root, file := skill.Path, skill.SkillFilePath
		if root == "" && file != "" {
			root = filepath.Dir(file)
		}
		if file == "" && root != "" {
			file = filepath.Join(root, skills.SkillFileName)
		}
		handler.locations[skill.Name] = skillLocation{root: root, file: file}
	}
	return NewRouter(map[string]ProtocolHandler{"skill": handler, "skills": handler})
}

func (h *skillHandler) Locate(ctx context.Context, u InternalURL) (string, error) {
	name, err := url.PathUnescape(u.Host)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("skill URL requires a name: skill://<name>")
	}
	location, ok := h.locations[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	if location.root == "" || location.file == "" {
		return "", fmt.Errorf("skill %q has no local path", name)
	}
	target, err := skillTarget(location, u)
	if err != nil {
		return "", err
	}
	root, err := resolveRealPath(location.root)
	if err != nil {
		return "", fmt.Errorf("locate skill %q root: %w", name, err)
	}
	target, err = resolveRealPath(target)
	if err != nil {
		return "", fmt.Errorf("locate skill %q target: %w", name, err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("skill %q target resolves outside its root", name)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return target, nil
}

// skillTarget maps a skill URL path to the file it names: the SKILL.md itself
// when the URL carries no path, otherwise a file inside the skill root. Guards
// run before any filesystem access, so a rejected URL never depends on which
// files happen to exist.
func skillTarget(location skillLocation, u InternalURL) (string, error) {
	if u.Pathname == "" || u.Pathname == "/" {
		return location.file, nil
	}
	relative, err := url.PathUnescape(strings.TrimPrefix(u.Pathname, "/"))
	if err != nil {
		return "", err
	}
	// Reject cross-platform separators and drive forms even on Unix hosts.
	if !filepath.IsLocal(relative) || strings.ContainsAny(relative, "\\\x00:") {
		return "", fmt.Errorf("invalid relative skill path %q", relative)
	}
	// IsLocal rejects a ".." that escapes, such as "sub/../../outside", but it
	// cleans interior ones away: "a/../b" would pass. Reject every segment.
	if slices.Contains(strings.Split(relative, "/"), "..") {
		return "", fmt.Errorf("skill path traversal is not allowed: %q", relative)
	}
	return filepath.Join(location.root, relative), nil
}

// resolveRealPath returns the absolute, symlink-resolved form of path. Root and
// target both go through it because the containment check compares them with
// filepath.Rel: a symlink left on either side makes that comparison meaningless.
func resolveRealPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
