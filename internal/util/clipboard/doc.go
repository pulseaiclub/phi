// Package clipboard reads and writes the system clipboard (plain text and images).
//
// Image reads use platform tools: wl-paste / xclip on Linux, osascript or
// pngpaste on macOS, PowerShell on Windows, with a WSL fallback to the Windows
// clipboard when Linux tools see no image data.
//
// Text reads use the platform API where there is one (CF_UNICODETEXT on
// Windows) and the same tools CopyText writes with elsewhere (pbpaste, wl-paste
// / xclip), so both directions agree on encoding.
package clipboard
