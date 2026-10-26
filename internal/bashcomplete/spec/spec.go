// Package spec holds the declared command trees "!" completion completes from
// before the project history knows a command.
//
// The data is a trimmed copy of the widely published community command specs:
// names, subcommands, flags, and one-line descriptions, written as JSON and
// embedded in the binary. It is a cold-start seed, not an authority on what a
// command accepts, so an entry that is stale only costs a stale row; history
// still outranks nothing here and a command with no spec keeps completing from
// history exactly as before.
package spec

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
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
	// "-m", "--message".
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
}

// Table is the parsed spec set, keyed by command name.
type Table struct {
	commands map[string]Command
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
			loaded = &Table{commands: map[string]Command{}}
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
	table := &Table{commands: make(map[string]Command, len(paths))}
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
			name = strings.TrimSuffix(path.Base(file), ".json")
		}
		if name == "" {
			return nil, fmt.Errorf("command spec %s names no command", file)
		}
		table.commands[name] = command
	}
	return table, nil
}

// Lookup returns the command node a line is typing inside. tokens are the
// words after the command name, so a line past `git commit` resolves to the
// commit subcommand and a line past `git switch` to the switch subcommand.
// ok is false when the command has no spec, which is the common case.
func (t *Table) Lookup(name string, args []string) (node Command, ok bool) {
	if t == nil || name == "" {
		return Command{}, false
	}
	root, known := t.commands[strings.Map(lowerRune, name)]
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

// lowerRune folds ASCII letters to lower case and leaves every other rune
// alone, so a command name looks up the same way on every platform.
func lowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// option reports whether word is a flag: a word to skip when walking a command
// tree, since options never name subcommands.
func option(word string) bool {
	return strings.HasPrefix(word, "-") && word != "-"
}
