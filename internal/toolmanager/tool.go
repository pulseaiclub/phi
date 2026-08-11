// Package toolmanager provides functionality for downloading and managing
// external tools (e.g., ripgrep, fd) from GitHub releases.
package toolmanager

import (
	"fmt"
	"runtime"
)

// Platform constants for cross-platform tool downloads.
const (
	PlatformDarwin = "darwin"
	PlatformLinux  = "linux"
	PlatformWin32  = "win32"
)

// Architecture constants for tool downloads.
const (
	ArchAMD64 = "amd64"
	ArchARM64 = "arm64"
	ArchX86   = "x86"
)

var defaultArchMap = map[string]map[string]string{
	PlatformDarwin: {
		ArchARM64: "aarch64",
		ArchAMD64: "x86_64",
	},
	PlatformLinux: {
		ArchARM64: "aarch64",
		ArchAMD64: "x86_64",
	},
	PlatformWin32: {
		ArchARM64: "aarch64",
		ArchAMD64: "x86_64",
	},
}

// ToolConfig defines the configuration for a downloadable tool.
type ToolConfig struct {
	// Name is the display name of the tool.
	Name string
	// Repo is the GitHub repository in "owner/repo" format.
	Repo string
	// BinaryName is the name of the executable binary.
	BinaryName string
	// AssetNames builds release asset candidates for the current platform.
	AssetNames AssetName
}

// Tools is a registry of downloadable tool configurations.
// Each entry maps a tool identifier to its ToolConfig.
var Tools = map[string]ToolConfig{
	"fd": {
		Name:       "fd",
		Repo:       "sharkdp/fd",
		BinaryName: "fd",
		AssetNames: AssetName{
			toolName:       "fd",
			versionPrefix:  "v",
			archMap:        defaultArchMap,
			darwinSuffixes: []string{"-apple-darwin.tar.gz"},
			linuxSuffixes: []string{
				"-unknown-linux-gnu.tar.gz",
				"-unknown-linux-musl.tar.gz",
			},
			winSuffixes: []string{"-pc-windows-msvc.zip"},
		},
	},
	"rg": {
		Name:       "ripgrep",
		Repo:       "BurntSushi/ripgrep",
		BinaryName: "rg",
		AssetNames: AssetName{
			toolName:       "ripgrep",
			versionPrefix:  "",
			archMap:        defaultArchMap,
			darwinSuffixes: []string{"-apple-darwin.tar.gz"},
			linuxSuffixes: []string{
				"-unknown-linux-musl.tar.gz",
				"-unknown-linux-gnu.tar.gz",
			},
			winSuffixes: []string{"-pc-windows-msvc.zip"},
		},
	},
}

// archMapping maps architecture constants to platform-specific arch names.
type archMapping map[string]map[string]string

// AssetName builds the full asset filename for a tool release.
type AssetName struct {
	toolName       string
	versionPrefix  string
	archMap        archMapping
	darwinSuffixes []string
	linuxSuffixes  []string
	winSuffixes    []string
}

func normalizePlatform(goos string) string {
	switch goos {
	case "darwin":
		return PlatformDarwin
	case "linux":
		return PlatformLinux
	case "windows":
		return PlatformWin32
	default:
		return ""
	}
}

func normalizeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return ArchAMD64
	case "arm64":
		return ArchARM64
	case "386":
		return ArchX86
	default:
		return ""
	}
}

var (
	platform = normalizePlatform(runtime.GOOS)
	arch     = normalizeArch(runtime.GOARCH)
)

// GetAssetNames returns release asset candidates in preference order for the
// current platform and architecture.
func (a AssetName) GetAssetNames(version string) []string {
	return a.getAssetNames(version, platform, arch)
}

func (a AssetName) getAssetNames(version, targetPlatform, targetArch string) []string {
	if targetPlatform == "" || targetArch == "" {
		return nil
	}

	platformArchs, ok := a.archMap[targetPlatform]
	if !ok {
		return nil
	}
	archName, ok := platformArchs[targetArch]
	if !ok {
		return nil
	}

	fullVersion := version
	if a.versionPrefix != "" {
		fullVersion = a.versionPrefix + version
	}

	var suffixes []string
	switch targetPlatform {
	case PlatformDarwin:
		suffixes = a.darwinSuffixes
	case PlatformLinux:
		suffixes = a.linuxSuffixes
	case PlatformWin32:
		suffixes = a.winSuffixes
	default:
		return nil
	}

	names := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		names = append(names, fmt.Sprintf("%s-%s-%s%s", a.toolName, fullVersion, archName, suffix))
	}
	return names
}
