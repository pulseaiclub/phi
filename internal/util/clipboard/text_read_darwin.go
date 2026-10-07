//go:build darwin

package clipboard

// readClipboardTextPlatform reads the pasteboard with pbpaste, the macOS
// counterpart of the pbcopy in CopyText.
func readClipboardTextPlatform() (string, error) {
	if !lookPath("pbpaste") {
		return "", ErrUnavailable
	}
	out, err := runCommandTimeout(defaultReadTimeout, "pbpaste")
	if err != nil {
		return "", ErrUnavailable
	}
	return string(out), nil
}
