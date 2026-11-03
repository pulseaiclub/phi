//go:build windows

package clipboard

import (
	"os"
	"strings"
)

func readClipboardImagePlatform() (Image, error) {
	tmpFile, err := os.CreateTemp("", "phi-clip-*.png")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	path := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(path)

	psQuoted := strings.ReplaceAll(path, "'", "''")

	psScript := "Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; " +
		"$path = '" + psQuoted + "'; $img = [System.Windows.Forms.Clipboard]::GetImage(); " +
		"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }"
	out, err := runCommandTimeout(5*defaultReadTimeout, "powershell", "-NoProfile", "-Command", psScript)
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
