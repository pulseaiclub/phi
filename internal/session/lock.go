package session

import (
	"fmt"
	"os"
)

// sessionLockPath is the sibling file a session's writers lock. The lock must
// not live on the JSONL itself: a rewrite replaces that file through
// os.Rename, so a waiter that had already opened the old inode would take its
// lock after the swap and append into a file nothing points at any more.
func sessionLockPath(path string) string { return path + ".lock" }

// withSessionLock runs fn while holding the exclusive lock for path.
//
// Every writer takes it — appends, header rewrites, deletes — because a rename
// stages a copy of the file and then replaces it. Without the lock, an append
// that lands between the copy and the rename is silently dropped: both sides
// report success, and the message is gone. The manager's own mutex cannot cover
// this, since `phi sessions rename` writes the file from a different process.
func withSessionLock(path string, fn func() error) error {
	f, err := os.OpenFile(sessionLockPath(path), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlockFile(f)
	return fn()
}
