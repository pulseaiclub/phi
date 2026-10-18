//go:build darwin

package clipboard

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func readClipboardImagePlatform() (Image, error) {
	if lookPath("pngpaste") {
		if img, err := readClipboardImageViaPNGPaste(); err == nil {
			return img, nil
		}
	}
	return readClipboardImageViaOsascript()
}

func readClipboardImageViaPNGPaste() (Image, error) {
	tmpFile, err := os.CreateTemp("", "phi-clip-*.png")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	path := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(path)

	if _, err := runCommandTimeout(defaultReadTimeout, "pngpaste", path); err != nil {
		return Image{}, ErrUnavailable
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return Image{}, ErrUnavailable
	}
	return Image{Data: data, MimeType: "image/png"}, nil
}

func readClipboardImageViaOsascript() (Image, error) {
	tmpFile, err := os.CreateTemp("", "phi-clip-*.png")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	path := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(path)

	script := fmt.Sprintf(`try
  set pngData to the clipboard as «class PNGf»
  if pngData is missing value then return "empty"
  set f to open for access POSIX file %q with write permission
  write pngData to f
  close access f
  return "ok"
on error
  return "empty"
end try`, path)
	out, err := runCommandTimeout(defaultReadTimeout, "osascript", "-e", script)
	if err != nil {
		return Image{}, ErrUnavailable
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return Image{}, ErrUnavailable
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return Image{}, ErrUnavailable
	}
	return Image{Data: data, MimeType: "image/png"}, nil
}
