//go:build !linux && !darwin && !windows

package clipboard

func readClipboardTextPlatform() (string, error) {
	return "", ErrUnavailable
}
