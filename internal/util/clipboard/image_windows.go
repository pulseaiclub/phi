//go:build windows

package clipboard

import (
	"os"
	"strings"

	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/util"
)

// psImageScript writes the clipboard image to $path (the first line of output
// is "ok:<source>:<formats>", or "empty:<formats>").
//
// Clipboard.GetImage covers the raw bitmaps and the registered formats .NET
// knows, but a browser that copies a JPEG or PNG puts a registered stream the
// WinForms conversion can miss, so the loop below reads those streams raw. A
// format probe over a fixed allowlist is not an option: the copy source picks
// the format, and missing one silently drops the paste.
const psImageScript = `Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$path = '` + psPathPlaceholder + `'
$data = [System.Windows.Forms.Clipboard]::GetDataObject()
$fmts = if ($data) { ($data.GetFormats() | Sort-Object) -join ';' } else { '' }
$src = 'GetImage'
$img = [System.Windows.Forms.Clipboard]::GetImage()
if (-not $img) {
  foreach ($fmt in @('PNG', 'image/png', 'JFIF', 'image/jpeg', 'GIF', 'image/gif', 'TIFF', 'image/tiff')) {
    try {
      $stream = $data.GetData($fmt)
      if ($stream -is [System.IO.Stream]) { $img = [System.Drawing.Image]::FromStream($stream); $src = $fmt; break }
    } catch { }
  }
}
if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output ('ok:' + $src + ':' + $fmts) }
else { Write-Output ('empty:' + $fmts) }`

// psPathPlaceholder is substituted with the quoted temp path.
const psPathPlaceholder = `@@PATH@@`

func readClipboardImagePlatform() (Image, error) {
	tmpFile, err := os.CreateTemp("", "phi-clip-*.png")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	path := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(path)

	psScript := util.ReplaceAll(psImageScript, psPathPlaceholder, util.ReplaceAll(path, "'", "''"))
	out, err := runCommandTimeout(5*defaultReadTimeout, "powershell", "-NoProfile", "-Command", psScript)
	if err != nil {
		debuglog.Logf("clipboard: windows image read: %v", err)
		return Image{}, ErrUnavailable
	}
	// The log line is the only way to see what the clipboard held: without it a
	// "Ctrl+V does nothing" report has no evidence behind it.
	line := strings.TrimSpace(string(out))
	debuglog.Logf("clipboard: windows image read: %s", line)
	if !strings.HasPrefix(line, "ok:") {
		return Image{}, ErrUnavailable
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return Image{}, ErrUnavailable
	}
	return Image{Data: data, MimeType: "image/png"}, nil
}
