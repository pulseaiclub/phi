//go:build linux

package clipboard

// readClipboardTextPlatform reads the clipboard through the same tools
// CopyText writes with: wl-clipboard on Wayland, xclip on X11.
func readClipboardTextPlatform() (string, error) {
	if lookPath("wl-paste") {
		if out, err := runCommandTimeout(defaultReadTimeout, "wl-paste", "--no-newline"); err == nil {
			return string(out), nil
		}
	}
	if lookPath("xclip") {
		if out, err := runCommandTimeout(defaultReadTimeout, "xclip", "-selection", "clipboard", "-o"); err == nil {
			return string(out), nil
		}
	}
	return "", ErrUnavailable
}
