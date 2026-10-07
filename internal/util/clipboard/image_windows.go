//go:build windows

package clipboard

import (
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/pulseaiclub/phi/internal/util"
)

// Clipboard formats that can carry a bitmap. Apps that copy a raw PNG register
// a format name instead (browsers use "PNG"), so those names are probed too.
const (
	cfBitmap = 2
	cfDib    = 5
	cfDibV5  = 17
)

var (
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	registerClipboardFormat    = user32.NewProc("RegisterClipboardFormatW")

	pngClipboardFormat     = sync.OnceValue(func() uintptr { return registeredFormat("PNG") })
	pngMimeClipboardFormat = sync.OnceValue(func() uintptr { return registeredFormat("image/png") })
)

// registeredFormat resolves a clipboard format name to its id (0 if unknown).
func registeredFormat(name string) uintptr {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	id, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(ptr)))
	return id
}

// clipboardHasImageFormat guards the PowerShell read below. Spawning PowerShell
// costs hundreds of milliseconds and briefly holds the clipboard, while this
// probe answers in microseconds: Ctrl+V on a text clipboard must not wait for
// it before falling back to pasting text.
func clipboardHasImageFormat() bool {
	for _, id := range []uintptr{cfBitmap, cfDib, cfDibV5, pngClipboardFormat(), pngMimeClipboardFormat()} {
		if id == 0 {
			continue
		}
		if ok, _, _ := isClipboardFormatAvailable.Call(id); ok != 0 {
			return true
		}
	}
	return false
}

func readClipboardImagePlatform() (Image, error) {
	if !clipboardHasImageFormat() {
		return Image{}, ErrUnavailable
	}
	tmpFile, err := os.CreateTemp("", "phi-clip-*.png")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	path := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(path)

	psQuoted := util.ReplaceAll(path, "'", "''")
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
