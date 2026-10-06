package tooldef

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveToCwd returns a cleaned absolute path.
// Relative inputs are joined with the session cwd from ctx (WithCwd), falling
// back to the process working directory. FS operations and rg must use this
// result — rg --json echoes the search path as given, and a relative search
// dir yields relative match paths that break ReadFile.
func ResolveToCwd(ctx context.Context, filePath string) (string, error) {
	filePath = strings.TrimSpace(filePath)
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath), nil
	}
	cwd, err := Cwd(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, filePath), nil
}

// RelToCwd formats an absolute path for the model and UI: slash-separated and
// relative to session/process cwd when the path is inside it, otherwise
// absolute. Grep/find/read/edit headers must use this so @file path can be
// passed back to read/edit (which resolve against cwd, not a search subdirectory).
func RelToCwd(ctx context.Context, abs string) string {
	cwd, err := Cwd(ctx)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(abs))
	}
	return RelTo(cwd, abs)
}

// Cwd returns the session cwd from ctx, or the process working directory.
func Cwd(ctx context.Context) (string, error) {
	if cwd := cwdFrom(ctx); cwd != "" {
		if !filepath.IsAbs(cwd) {
			abs, err := filepath.Abs(cwd)
			if err != nil {
				return "", fmt.Errorf("failed to resolve working directory %q: %w", cwd, err)
			}
			return abs, nil
		}
		return filepath.Clean(cwd), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return wd, nil
}

// RelTo is RelToCwd against an explicit base directory.
func RelTo(cwd, abs string) string {
	cwd = filepath.Clean(cwd)
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return filepath.ToSlash(abs)
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}
