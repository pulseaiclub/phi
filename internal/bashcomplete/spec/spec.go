package spec

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sync"
)

//go:embed specs/*.json
var specFiles embed.FS

// Arg describes what an option takes, for the picker's hint text.
type Arg struct {
	// Name is how the value is drawn: "message", "file".
	Name string `json:"name"`
	// Optional marks a value that may be left out.
	Optional bool `json:"optional,omitempty"`
}

// Option is one flag of a command.
type Option struct {
	// Names are the spellings the user can type, longest first by convention:
	// "--message", "-m".
	Names       []string `json:"names"`
	Description string   `json:"description,omitempty"`
	Args        []Arg    `json:"args,omitempty"`
}

// Command is one node of a command tree: the command itself, a subcommand, or
// a nested subcommand.
type Command struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Options     []Option  `json:"options,omitempty"`
	Subcommands []Command `json:"subcommands,omitempty"`
	// NoOptionAfterArgument keeps completing options only before the first
	// positional argument, for commands where the argument list is open-ended.
	NoOptionAfterArgument bool `json:"noOptionAfterArgument,omitempty"`
	// Source records where the tree came from: empty for the embedded specs,
	// "help" for one read out of the command's own output.
	Source string `json:"source,omitempty"`
}

// Table is a set of command trees, keyed by command name.
//
// A table is immutable once built, so the completion path reads it without a
// lock while discovery appends to the shared one.
type Table struct {
	names map[string]string
	nodes map[string]Command
}

// NewTable returns an empty table.
func NewTable() *Table {
	return &Table{names: make(map[string]string), nodes: make(map[string]Command)}
}

var (
	loadOnce sync.Once
	loaded   *Table
)

// Builtin returns the embedded spec table. It is parsed once and shared: the
// completion path reads it on every keystroke, and the files never change at
// runtime.
func Builtin() *Table {
	loadOnce.Do(func() {
		table, err := load()
		if err != nil {
			// A broken embedded file is a build-time defect, and a completion
			// must not be the thing that reports it: an empty table degrades
			// to the history sources.
			loaded = NewTable()
			return
		}
		loaded = table
	})
	return loaded
}

// load parses every embedded spec file into one table.
func load() (*Table, error) {
	paths, err := fs.Glob(specFiles, "specs/*.json")
	if err != nil {
		return nil, fmt.Errorf("list command specs: %w", err)
	}
	table := NewTable()
	for _, file := range paths {
		data, err := specFiles.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read command spec %s: %w", file, err)
		}
		var command Command
		if err := json.Unmarshal(data, &command); err != nil {
			return nil, fmt.Errorf("parse command spec %s: %w", file, err)
		}
		name := command.Name
		if name == "" {
			// The file name carries the command when the body omits it.
			name = trimSuffix(path.Base(file), ".json")
		}
		if name == "" {
			return nil, fmt.Errorf("command spec %s names no command", file)
		}
		command.Name = name
		table.set(command)
	}
	return table, nil
}

// set stores one command tree under its name, folding case the way lookups do.
func (t *Table) set(command Command) {
	key := foldName(command.Name)
	t.names[key] = command.Name
	t.nodes[key] = command
}

// Lookup returns the command node a line is typing inside. args are the words
// after the command name, so a line past `git commit` resolves to the commit
// subcommand. ok is false when the command has no spec, which is the common
// case.
func (t *Table) Lookup(name string, args []string) (node Command, ok bool) {
	if t == nil || name == "" {
		return Command{}, false
	}
	root, known := t.nodes[foldName(name)]
	if !known {
		return Command{}, false
	}
	node = root
	for _, arg := range args {
		if arg == "" || option(arg) {
			continue
		}
		next, found := node.subcommand(arg)
		if !found {
			return node, true
		}
		node = next
	}
	return node, true
}

// Names lists the commands the table knows, as they are spelled.
func (t *Table) Names() []string {
	if t == nil {
		return nil
	}
	out := make([]string, 0, len(t.names))
	for _, name := range t.names {
		out = append(out, name)
	}
	return out
}

// subcommand resolves one word to a child of the node, matching the word as
// the shell would name it.
func (c Command) subcommand(word string) (Command, bool) {
	for _, child := range c.Subcommands {
		if foldEqual(child.Name, word) {
			return child, true
		}
	}
	return Command{}, false
}

// trimSuffix drops one trailing suffix, or returns s unchanged.
func trimSuffix(s, suffix string) string {
	if len(s) <= len(suffix) {
		return s
	}
	if s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// option reports whether word is a flag: a word to skip when walking a command
// tree, since options never name subcommands. A bare dash is not one.
func option(word string) bool {
	if len(word) < 2 || word[0] != '-' {
		return false
	}
	return true
}
