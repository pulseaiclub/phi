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

// CommandArg declares one positional argument of a command: what it is called,
// and where its values can be read from when the user starts typing one.
type CommandArg struct {
	// Name is how the argument is drawn in a hint: "commit", "branch".
	Name string `json:"name"`
	// Generator names a value source the binary knows how to read, such as the
	// commits in this repository. It is a lookup key, never a command: a spec
	// file cannot make phi run arbitrary programs.
	Generator string `json:"generator,omitempty"`
}

// Command is one node of a command tree: the command itself, a subcommand, or
// a nested subcommand.
type Command struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Options     []Option  `json:"options,omitempty"`
	Subcommands []Command `json:"subcommands,omitempty"`
	// Args declares the positional arguments in order, so a word in one of
	// their slots can complete from the values that argument takes.
	Args []CommandArg `json:"args,omitempty"`
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

// subcommandWords returns how many of args name a subcommand: the walk down the
// tree ends at the node the arguments belong to.
func (t *Table) subcommandWords(name string, args []string) int {
	if t == nil || name == "" {
		return 0
	}
	node, known := t.nodes[foldName(name)]
	if !known {
		return 0
	}
	used := 0
	for _, arg := range args {
		if arg == "" || option(arg) {
			continue
		}
		next, found := node.subcommand(arg)
		if !found {
			break
		}
		node = next
		used++
	}
	return used
}

// Argument returns the declared argument the cursor sits in, and the reader
// that supplies its values. args are the words after the command name, the last
// of which is the word being typed.
//
// A value already typed next to a flag belongs to that flag, not to the
// positional list (`git log -n 5`), so those words are not counted.
func (t *Table) Argument(name string, args []string) (CommandArg, Generator, bool) {
	// The words that select subcommands are part of the address, not positional
	// arguments: `git log 23` starts counting at the word after log.
	start := t.subcommandWords(name, args)
	node, ok := t.Lookup(name, args)
	if !ok || len(node.Args) == 0 || start > len(args) {
		return CommandArg{}, nil, false
	}
	position := 0
	skipNext := false
	for i, arg := range args {
		if i < start {
			continue
		}
		last := i == len(args)-1
		if skipNext {
			skipNext = false
			continue
		}
		if option(arg) {
			// A flag may take the next word as its value, which is not a
			// positional argument.
			skipNext = !last
			continue
		}
		if arg == "" {
			// The word under the cursor changes nothing about which slot it
			// occupies.
			continue
		}
		if !last {
			position++
		}
	}
	if position >= len(node.Args) {
		// Past the declared arguments: the command takes no more of them.
		return CommandArg{}, nil, false
	}
	declared := node.Args[position]
	read := generator(declared.Generator)
	if read == nil {
		return CommandArg{}, nil, false
	}
	return declared, read, true
}
