package job

import (
	"fmt"
	"strings"
)

// Role selects a sub-agent capability profile.
type Role string

const (
	// RoleExplore is codebase recon without write/edit (default); bash allowed.
	RoleExplore Role = "explore"
	// RoleWorker may read and write; for scoped, self-contained change blocks.
	RoleWorker Role = "worker"
	// RoleReview is diffs / checks without edits; same tools as explore.
	RoleReview Role = "review"
)

// ParseRole normalizes a role string. Empty → explore.
func ParseRole(s string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(RoleExplore):
		return RoleExplore, nil
	case string(RoleWorker):
		return RoleWorker, nil
	case string(RoleReview):
		return RoleReview, nil
	default:
		return "", fmt.Errorf("%w: unknown role %q (want explore|worker|review)", ErrInvalid, s)
	}
}

// NormalizeRole returns explore for empty/unknown without error (legacy meta).
func NormalizeRole(s string) Role {
	r, err := ParseRole(s)
	if err != nil {
		return RoleExplore
	}
	return r
}
