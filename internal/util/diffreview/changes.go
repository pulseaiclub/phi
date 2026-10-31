package diffreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// LoadChange bypasses command-line spec normalization: status paths may name
// deleted files, revisions, or glob characters and must stay literal paths.
func LoadChange(ctx context.Context, root string, change gitx.Change) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := []string{
		"--literal-pathspecs",
		"-c",
		"color.ui=never",
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"--src-prefix=a/",
		"--dst-prefix=b/",
		"--no-relative",
	}
	noIndex := change.Status == '?' || change.Status == 'U'
	if noIndex {
		info, err := os.Lstat(filepath.Join(root, change.Path))
		if err != nil {
			if change.Status == 'U' && errors.Is(err, os.ErrNotExist) {
				return changeMetadata(change.Path, "Unresolved conflict: file deleted from the working tree"), nil
			}
			return "", fmt.Errorf("read changed file %q: %w", change.Path, err)
		}
		if info.IsDir() {
			return changeMetadata(change.Path, "Directory or submodule changed; inspect changes inside it"), nil
		}
		if info.Mode().IsRegular() && info.Size() == 0 {
			return changeMetadata(change.Path, "Empty file"), nil
		}
		argv = append(argv, "--no-index", "--", "/dev/null", "./"+change.Path)
	} else {
		head := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", "HEAD")
		head.Dir = root
		if _, err := head.Output(); err != nil {
			if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
				return "", gitx.FormatError(head.Args, "", err)
			}
			// Before the first commit every existing file is an addition.
			if _, err := os.Lstat(filepath.Join(root, change.Path)); errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			change.Status = '?'
			return LoadChange(ctx, root, change)
		}
		argv = append(argv, "HEAD", "--submodule=short", "--ignore-submodules=none", "--", change.Path)
		if change.OldPath != "" {
			argv = append(argv, change.OldPath)
		}
	}
	cmd := exec.CommandContext(ctx, "git", argv...) //nolint:gosec // fixed git command, literal status paths
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); !noIndex || !ok || exit.ExitCode() != 1 {
			return "", gitx.FormatError(append([]string{"git"}, argv...), "", err)
		}
	}
	text := string(out)
	if noIndex && strings.TrimSpace(text) == "" {
		return changeMetadata(change.Path, "Empty file"), nil
	}
	return text, nil
}

func changeMetadata(path, message string) string {
	return "diff --git " + strconv.Quote("a/"+path) + " " + strconv.Quote("b/"+path) + "\n" + message + "\n"
}
