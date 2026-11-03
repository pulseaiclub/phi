//go:build linux

package clipboard

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
)

func isWaylandSession() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"
}

func readClipboardImagePlatform() (Image, error) {
	if os.Getenv("TERMUX_VERSION") != "" {
		return Image{}, ErrUnavailable
	}

	wayland := isWaylandSession()
	wsl := isWSL()

	if wayland || wsl {
		if img, ok := tryReadImage(readClipboardImageViaWlPaste); ok {
			return img, nil
		}
		if img, ok := tryReadImage(readClipboardImageViaXclip); ok {
			return img, nil
		}
	}
	if wsl {
		if img, ok := tryReadImage(readClipboardImageViaPowerShell); ok {
			return img, nil
		}
	}
	if !wayland {
		if img, ok := tryReadImage(readClipboardImageViaXclip); ok {
			return img, nil
		}
	}
	return Image{}, ErrUnavailable
}

func tryReadImage(read func() (Image, error)) (Image, bool) {
	img, err := read()
	if err != nil || len(img.Data) == 0 {
		return Image{}, false
	}
	return img, true
}

func readClipboardImageViaWlPaste() (Image, error) {
	typesOut, err := runCommandTimeout(defaultListTimeout, "wl-paste", "--list-types")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	selected := selectPreferredImageMimeType(splitLines(string(typesOut)))
	if selected == "" {
		return Image{}, ErrUnavailable
	}
	data, err := runCommandTimeout(defaultReadTimeout, "wl-paste", "--type", selected, "--no-newline")
	if err != nil || len(data) == 0 {
		return Image{}, ErrUnavailable
	}
	return Image{Data: data, MimeType: baseMimeType(selected)}, nil
}

func readClipboardImageViaXclip() (Image, error) {
	var candidateTypes []string
	if targets, err := runCommandTimeout(
		defaultListTimeout,
		"xclip",
		"-selection",
		"clipboard",
		"-t",
		"TARGETS",
		"-o",
	); err == nil {
		candidateTypes = splitLines(string(targets))
	}

	preferred := selectPreferredImageMimeType(candidateTypes)
	tryTypes := append([]string(nil), preferredImageMimes...)
	if preferred != "" {
		tryTypes = append([]string{preferred}, tryTypes...)
	}

	seen := make(map[string]struct{}, len(tryTypes))
	for _, mimeType := range tryTypes {
		mimeType = baseMimeType(mimeType)
		if mimeType == "" {
			continue
		}
		if _, ok := seen[mimeType]; ok {
			continue
		}
		seen[mimeType] = struct{}{}
		data, err := runCommandTimeout(defaultReadTimeout, "xclip", "-selection", "clipboard", "-t", mimeType, "-o")
		if err == nil && len(data) > 0 {
			return Image{Data: data, MimeType: mimeType}, nil
		}
	}
	return Image{}, ErrUnavailable
}

func readClipboardImageViaPowerShell() (Image, error) {
	tmpFile, err := os.CreateTemp("", "phi-wsl-clip-*.png")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	path := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(path)

	winPath, err := runCommandTimeout(defaultListTimeout, "wslpath", "-w", path)
	if err != nil {
		return Image{}, ErrUnavailable
	}
	winPathStr := strings.TrimSpace(string(winPath))
	if winPathStr == "" {
		return Image{}, ErrUnavailable
	}

	psQuoted := strings.ReplaceAll(winPathStr, "'", "''")

	psScript := fmt.Sprintf(
		"Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; "+
			"$path = '%s'; $img = [System.Windows.Forms.Clipboard]::GetImage(); "+
			"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }",
		psQuoted,
	)
	out, err := runCommandTimeout(5*time.Second, "powershell.exe", "-NoProfile", "-Command", psScript)
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

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSLENV") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(data), []byte("microsoft")) ||
		bytes.Contains(bytes.ToLower(data), []byte("wsl"))
}
