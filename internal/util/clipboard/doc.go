// Package clipboard reads image data from the system clipboard.
//
// Image reads use platform tools: wl-paste / xclip on Linux, osascript or
// pngpaste on macOS, PowerShell on Windows, with a WSL fallback to the Windows
// clipboard when Linux tools see no image data.
//
// Text copy is not here: the UI layer writes text through xui.CopyToClipboard,
// which also speaks OSC 52 — a copy can reach the local clipboard even over
// ssh, when the terminal honors OSC 52 and no platform tool is available.
package clipboard
