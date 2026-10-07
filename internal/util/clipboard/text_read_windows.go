//go:build windows

package clipboard

import (
	"fmt"
	"unicode/utf16"
	"unsafe"
)

// readClipboardTextPlatform reads CF_UNICODETEXT through the Win32 API.
// `Get-Clipboard` (PowerShell) and `clip.exe` would decode through the console
// codepage and mangle UTF-8 on CJK systems — the same reason copyTextWindows
// writes UTF-16 by hand.
func readClipboardTextPlatform() (string, error) {
	if err := openWindowsClipboard(); err != nil {
		return "", err
	}
	defer closeClipboard.Call()

	h, _, _ := getClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil
	}
	size, _, _ := globalSize.Call(h)
	if size < 2 {
		return "", nil
	}
	ptr, _, callErr := globalLock.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("clipboard: lock Windows clipboard text: %w", callErr)
	}
	defer globalUnlock.Call(h)

	buf := make([]uint16, int(size)/2)
	rtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, size)
	return utf16StringZ(buf), nil
}

// utf16StringZ decodes a NUL-terminated UTF-16 buffer, tolerating a missing
// terminator (GlobalSize can be larger than the string it holds).
func utf16StringZ(buf []uint16) string {
	for i, c := range buf {
		if c == 0 {
			return string(utf16.Decode(buf[:i]))
		}
	}
	return string(utf16.Decode(buf))
}
