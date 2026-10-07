//go:build windows

package clipboard

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	openClipboard    = user32.NewProc("OpenClipboard")
	closeClipboard   = user32.NewProc("CloseClipboard")
	emptyClipboard   = user32.NewProc("EmptyClipboard")
	setClipboardData = user32.NewProc("SetClipboardData")
	getClipboardData = user32.NewProc("GetClipboardData")
	globalAlloc      = kernel32.NewProc("GlobalAlloc")
	globalFree       = kernel32.NewProc("GlobalFree")
	globalLock       = kernel32.NewProc("GlobalLock")
	globalUnlock     = kernel32.NewProc("GlobalUnlock")
	globalSize       = kernel32.NewProc("GlobalSize")
	rtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")
)

func copyTextWindows(text string) error {
	utf16Text, err := encodeWindowsClipboardText(text)
	if err != nil {
		return fmt.Errorf("clipboard: encode text as UTF-16: %w", err)
	}

	if err := openWindowsClipboard(); err != nil {
		return err
	}
	defer closeClipboard.Call()

	if result, _, callErr := emptyClipboard.Call(); result == 0 {
		return fmt.Errorf("clipboard: empty Windows clipboard: %w", callErr)
	}

	hGlobal, _, callErr := globalAlloc.Call(gmemMoveable, uintptr(len(utf16Text))*2)
	if hGlobal == 0 {
		return fmt.Errorf("clipboard: allocate Windows clipboard text: %w", callErr)
	}

	locked, _, callErr := globalLock.Call(hGlobal)
	if locked == 0 {
		globalFree.Call(hGlobal)
		return fmt.Errorf("clipboard: lock Windows clipboard text: %w", callErr)
	}
	rtlMoveMemory.Call(locked, uintptr(unsafe.Pointer(&utf16Text[0])), uintptr(len(utf16Text))*2)
	runtime.KeepAlive(utf16Text)
	globalUnlock.Call(hGlobal)

	if result, _, callErr := setClipboardData.Call(cfUnicodeText, hGlobal); result == 0 {
		globalFree.Call(hGlobal)
		return fmt.Errorf("clipboard: set Windows Unicode text: %w", callErr)
	}
	// SetClipboardData owns hGlobal after a successful call.
	return nil
}

func encodeWindowsClipboardText(text string) ([]uint16, error) {
	return syscall.UTF16FromString(text)
}

func openWindowsClipboard() error {
	var lastErr error
	for range 5 {
		if result, _, callErr := openClipboard.Call(0); result != 0 {
			return nil
		} else {
			lastErr = callErr
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("clipboard: open Windows clipboard: %w", lastErr)
}
