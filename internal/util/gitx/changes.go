package gitx

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Change describes a changed file. Paths are repository-relative.
type Change struct {
	Path    string
	OldPath string
	Status  byte
}

// ChangedFiles reads one entry per path, including partially staged files.
func ChangedFiles(ctx context.Context, cwd string) (string, []Change, error) {
	root, err := run(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil, err
	}
	root = strings.TrimSuffix(root, "\n")
	text, err := run(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return "", nil, err
	}
	changes, err := parseChanges(text)
	return root, changes, err
}

func parseChanges(text string) ([]Change, error) {
	var changes []Change
	for text != "" {
		record, rest, ok := strings.Cut(text, "\x00")
		if !ok || len(record) < 4 || record[2] != ' ' {
			return nil, fmt.Errorf("invalid git status record: %q", record)
		}
		text = rest
		x, y := record[0], record[1]
		change := Change{Path: record[3:], Status: x}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			change.OldPath, text, ok = strings.Cut(text, "\x00")
			if !ok || change.OldPath == "" {
				return nil, fmt.Errorf("missing original path for git status record: %q", record)
			}
		}
		switch record[:2] {
		case "??":
			change.Status = '?'
		case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
			change.Status = 'U'
		case "!!", "  ":
			continue
		default:
			if x == ' ' || y == 'D' {
				change.Status = y
			}
			// A rename belongs to the first changed side unless the worktree
			// itself renamed the file and the index has no change.
			if x != 'R' && x != 'C' && x != ' ' {
				change.OldPath = ""
			}
		}
		changes = append(changes, change)
	}
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}
