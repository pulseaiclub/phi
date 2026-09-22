package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// serverDef describes one language server we know how to drive. Nothing here is
// required for the package to work; a server is used only if its binary is
// found on PATH or in one of the folders installers commonly use (binDirs).
type serverDef struct {
	Name        string
	Lang        string // language name, for display
	Cmd         []string
	Exts        []string          // file extensions this server handles
	LangIDs     map[string]string // ext -> LSP languageId, when it differs
	DefaultLang string
	InitOptions map[string]any
}

// languageID is the LSP languageId for a file, chosen by extension.
func (d serverDef) languageID(rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	if id, ok := d.LangIDs[ext]; ok {
		return id
	}
	return d.DefaultLang
}

// serverDefs returns the known servers, best first: the first entry whose
// binary exists wins for a given extension, so a more capable server listed
// earlier takes precedence. The table is rebuilt per call so no caller can
// mutate what another one sees.
func serverDefs() []serverDef {
	return []serverDef{
		{
			Name: "gopls", Lang: "Go", Cmd: []string{"gopls"},
			Exts: []string{".go"}, DefaultLang: "go",
		},
		{
			Name: "rust-analyzer", Lang: "Rust", Cmd: []string{"rust-analyzer"},
			Exts: []string{".rs"}, DefaultLang: "rust",
		},
		{
			Name: "pyright", Lang: "Python", Cmd: []string{"pyright-langserver", "--stdio"},
			Exts: []string{".py", ".pyi"}, DefaultLang: "python",
		},
		{
			Name: "pylsp", Lang: "Python", Cmd: []string{"pylsp"},
			Exts: []string{".py", ".pyi"}, DefaultLang: "python",
		},
		{
			// Last for Python: it lints well but resolves too little.
			Name: "ruff", Lang: "Python", Cmd: []string{"ruff", "server"},
			Exts: []string{".py"}, DefaultLang: "python",
		},
		{
			Name:        "typescript",
			Lang:        "TypeScript and JavaScript",
			Cmd:         []string{"typescript-language-server", "--stdio"},
			Exts:        []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
			LangIDs:     map[string]string{".ts": "typescript", ".tsx": "typescriptreact", ".jsx": "javascriptreact"},
			DefaultLang: "javascript",
		},
		{
			Name: "clangd", Lang: "C and C++", Cmd: []string{"clangd", "--background-index"},
			Exts:        []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".m", ".mm"},
			LangIDs:     map[string]string{".c": "c", ".h": "c"},
			DefaultLang: "cpp",
		},
		{
			Name: "zls", Lang: "Zig", Cmd: []string{"zls"},
			Exts: []string{".zig"}, DefaultLang: "zig",
		},
		{
			Name: "lua", Lang: "Lua", Cmd: []string{"lua-language-server"},
			Exts: []string{".lua"}, DefaultLang: "lua",
		},
		{
			Name: "solargraph", Lang: "Ruby", Cmd: []string{"solargraph", "stdio"},
			Exts: []string{".rb"}, DefaultLang: "ruby",
		},
		{
			Name: "jdtls", Lang: "Java", Cmd: []string{"jdtls"},
			Exts: []string{".java"}, DefaultLang: "java",
		},
		{
			Name: "omnisharp", Lang: "C#", Cmd: []string{"omnisharp", "-lsp"},
			Exts: []string{".cs"}, DefaultLang: "csharp",
		},
		{
			Name: "texlab", Lang: "LaTeX", Cmd: []string{"texlab"},
			Exts: []string{".tex"}, DefaultLang: "latex",
		},
	}
}

// registryFor lists every known server for rel's extension, best first,
// installed or not.
func registryFor(rel string) []serverDef {
	ext := strings.ToLower(filepath.Ext(rel))
	var out []serverDef
	for _, def := range serverDefs() {
		if slices.Contains(def.Exts, ext) {
			out = append(out, def)
		}
	}
	return out
}

// indexServers resolves which known servers are installed into an
// extension -> server table, plus the names of the servers that claimed at
// least one extension. lookup reports the resolved binary for a command name;
// the first entry claiming an extension keeps it.
func indexServers(defs []serverDef, lookup func(name string) (string, bool)) (map[string]*serverDef, []string) {
	byExt := map[string]*serverDef{}
	var available []string
	for i := range defs {
		def := defs[i] // a copy: Cmd[0] becomes the resolved path
		bin, ok := lookup(def.Cmd[0])
		if !ok {
			continue
		}
		def.Cmd = append([]string{bin}, def.Cmd[1:]...)
		claimed := false
		for _, ext := range def.Exts {
			if byExt[ext] == nil {
				byExt[ext] = &def
				claimed = true
			}
		}
		if claimed {
			available = append(available, def.Name)
		}
	}
	return byExt, available
}

// binDirs lists folders installers put binaries in that are often missing from
// PATH, so a server installed by hand without a fresh shell is still found.
func binDirs() []string {
	var dirs []string
	add := func(elem ...string) { dirs = append(dirs, filepath.Join(elem...)) }
	if v := os.Getenv("GOBIN"); v != "" {
		add(v)
	}
	for _, p := range filepath.SplitList(os.Getenv("GOPATH")) {
		if p != "" {
			add(p, "bin")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home, "go", "bin")     // go install, default GOPATH
		add(home, ".cargo", "bin") // rustup, cargo install
		add(home, ".local", "bin") // pipx
	}
	// npm puts global packages beside its own executable unless the prefix was
	// moved.
	if npm, err := exec.LookPath("npm"); err == nil {
		add(filepath.Dir(npm))
	}
	switch runtime.GOOS {
	case "darwin":
		add("/opt/homebrew/bin")
		add("/usr/local/bin")
		// Homebrew's llvm is keg-only: clangd is installed but never linked.
		add("/opt/homebrew/opt/llvm/bin")
		add("/usr/local/opt/llvm/bin")
	case "windows":
		if v := os.Getenv("APPDATA"); v != "" {
			add(v, "npm")
		}
		if v := os.Getenv("ProgramFiles"); v != "" {
			add(v, "LLVM", "bin")
		}
	}
	return dirs
}

// lookPathIn finds a command on PATH, then in dirs. On Windows exec.LookPath
// also tries PATHEXT extensions for a joined path, so "npm" finds npm.cmd.
func lookPathIn(name string, dirs []string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, d := range dirs {
		if p, err := exec.LookPath(filepath.Join(d, name)); err == nil {
			return p, true
		}
	}
	return "", false
}
