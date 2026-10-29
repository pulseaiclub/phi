//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package session

import "os"

// Unsupported platforms cannot coordinate writers, so the lock is a no-op and
// only the manager's own mutex serializes appends. Refusing the write would
// cost the agent the whole session; a rare cross-process rename is the smaller
// risk.
func lockFile(_ *os.File) error { return nil }

func unlockFile(_ *os.File) {}
