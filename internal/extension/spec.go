package extension

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Spec is a GitHub plugin install target: owner/repo with an optional ref (tag or branch).
type Spec struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Ref   string `json:"ref,omitempty"` // empty = remote default branch
}

// CloneURL returns the HTTPS git clone URL for the spec.
func (s Spec) CloneURL() string {
	return fmt.Sprintf("https://github.com/%s/%s.git", s.Owner, s.Repo)
}

// ID is the on-disk extension directory name (repo name).
func (s Spec) ID() string { return s.Repo }

// ParseSpec parses owner/repo[@ref], github.com/owner/repo[@ref], or
// https://github.com/owner/repo[.git][@ref]. SSH git@host URLs are rejected.
func ParseSpec(raw string) (Spec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Spec{}, errors.New("empty plugin spec")
	}
	if strings.HasPrefix(raw, "git@") {
		return Spec{}, errors.New("SSH git URLs are not supported; use owner/repo or https://github.com/owner/repo")
	}

	pathPart, ref := raw, ""
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		before, after := raw[:i], raw[i+1:]
		// ref must be a single path segment (tag/branch), not owner/repo.
		if after != "" && !strings.Contains(after, "/") && !strings.Contains(after, ":") {
			pathPart, ref = before, after
		}
	}

	owner, repo, err := splitOwnerRepo(pathPart)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Owner: owner, Repo: repo, Ref: ref}, nil
}

func splitOwnerRepo(s string) (owner, repo string, err error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".git")

	if strings.Contains(s, "://") {
		u, perr := url.Parse(s)
		if perr != nil {
			return "", "", fmt.Errorf("invalid plugin URL %q: %w", s, perr)
		}
		host := strings.ToLower(u.Host)
		if host != "github.com" && host != "www.github.com" {
			return "", "", fmt.Errorf("only github.com repos are supported (got %q)", u.Host)
		}
		s = strings.Trim(u.Path, "/")
	} else {
		s = strings.TrimPrefix(s, "github.com/")
		s = strings.TrimPrefix(s, "www.github.com/")
	}

	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("want owner/repo[@tag], got %q", s)
	}
	if strings.Contains(parts[0], "..") || strings.Contains(parts[1], "..") {
		return "", "", fmt.Errorf("invalid owner/repo %q", s)
	}
	return parts[0], parts[1], nil
}
