package bashtool

// Shell resolution for the bash tool:
//
//   - Windows: Git Bash at known locations (%ProgramFiles%\Git\bin\bash.exe,
//     %ProgramFiles(x86)%\Git\bin\bash.exe, %LocalAppData%\Programs\Git\bin\bash.exe
//     for per-user installs), then bash.exe on PATH (Cygwin, MSYS2, WSL).
//   - WSL's legacy bash.exe shim (C:\Windows\System32\bash.exe) can't take
//     "-c" arguments, so commands are fed via stdin ("bash -s").
//   - Unix: /bin/bash, then bash on PATH, then fall back to sh.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// shellConfig describes how to launch the resolved shell.
type shellConfig struct {
	shell     string
	args      []string
	stdinMode bool // feed the command via stdin ("-s") instead of "-c <cmd>"
}

var (
	shellCfgMu  sync.Mutex
	shellCfg    shellConfig
	shellCfgSet bool
)

// resolveShellConfig returns the cached shell config. On failure nothing is
// cached, so installing Git Bash while phi runs takes effect on the next call.
func resolveShellConfig() (shellConfig, error) {
	shellCfgMu.Lock()
	defer shellCfgMu.Unlock()
	if shellCfgSet {
		return shellCfg, nil
	}
	cfg, err := resolveShellConfigUncached()
	if err == nil {
		shellCfg, shellCfgSet = cfg, true
	}
	return cfg, err
}

func resolveShellConfigUncached() (shellConfig, error) {
	if runtime.GOOS == "windows" {
		searched := gitBashPaths()
		for _, p := range searched {
			if isFile(p) {
				return configForShell(p), nil
			}
		}
		if p := findBashOnPath(); p != "" {
			return configForShell(p), nil
		}
		return shellConfig{}, fmt.Errorf(
			"no bash shell found: install Git for Windows (https://git-scm.com/download/win) "+
				"or add bash (Cygwin/MSYS2/WSL) to PATH; searched: %s",
			strings.Join(searched, ", "),
		)
	}
	if isFile("/bin/bash") {
		return configForShell("/bin/bash"), nil
	}
	if p := findBashOnPath(); p != "" {
		return configForShell(p), nil
	}
	return shellConfig{shell: "sh", args: []string{"-c"}}, nil
}

// gitBashPaths returns the Git Bash candidates in preference order: the
// machine-wide installs, then the per-user one, then whatever a Git on PATH
// points at. Without the last two a Git that is not under %ProgramFiles% is
// invisible here, and the fallback picks WSL's legacy bash.exe shim off PATH,
// which dies with "execvpe(/bin/bash): No such file or directory" when no
// distribution is installed.
func gitBashPaths() []string {
	var paths []string
	add := func(path string) {
		for _, seen := range paths {
			if samePath(seen, path) {
				return
			}
		}
		paths = append(paths, path)
	}
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if root := os.Getenv(key); root != "" {
			add(filepath.Join(root, "Git", "bin", "bash.exe"))
		}
	}
	if bash := gitBashFromGitOnPath(); bash != "" {
		add(bash)
	}
	return paths
}

// gitBashFromGitOnPath derives Git Bash from a Git on PATH. It is the only way
// a custom install root (D:\Git) shows up: cmd\git.exe and mingw64\bin\git.exe
// both sit two levels below the root, whose bin\bash.exe is the launcher phi
// wants. Shims (scoop, .cmd wrappers) derive a root without bash.exe and are
// dropped by the existence check.
func gitBashFromGitOnPath() string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	bash := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
	if !isFile(bash) {
		return ""
	}
	return bash
}

var wslBashRe = regexp.MustCompile(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

// isLegacyWslBashPath reports whether path is Windows' legacy WSL bash shim,
// which doesn't handle "-c" arguments well.
func isLegacyWslBashPath(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, "/", `\`))

	return wslBashRe.MatchString(normalized)
}

func configForShell(path string) shellConfig {
	if isLegacyWslBashPath(path) {
		return shellConfig{shell: path, args: []string{"-s"}, stdinMode: true}
	}
	return shellConfig{shell: path, args: []string{"-c"}}
}

// findBashOnPath locates bash via `where bash.exe` (Windows) / `which bash`
// (Unix). `where` can list paths that don't exist, so results are verified.
func findBashOnPath() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prog, arg := "which", "bash"
	if runtime.GOOS == "windows" {
		prog, arg = "where", "bash.exe"
	}
	out, err := exec.CommandContext(ctx, prog, arg).Output()
	if err != nil {
		return ""
	}
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if first == "" {
		return ""
	}
	if runtime.GOOS == "windows" && !isFile(first) {
		return ""
	}
	return first
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
