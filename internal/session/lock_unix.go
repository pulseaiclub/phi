//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package session

import (
	"os"
	"syscall"
)

// lockFile blocks until f's exclusive advisory lock is held. flock is per open
// file description, so two managers in one process contend exactly like two
// phi processes do.
func lockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			return err
		}
	}
}

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
