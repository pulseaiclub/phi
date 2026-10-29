package session

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// lockFile blocks until f's exclusive lock is held. LockFileEx locks byte
// ranges per handle, so two managers in one process contend like two phi
// processes do.
func lockFile(f *os.File) error {
	var overlapped syscall.Overlapped
	result, _, err := lockFileEx.Call(f.Fd(), 2, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) {
	var overlapped syscall.Overlapped
	_, _, _ = unlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
}
