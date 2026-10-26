package bashcomplete

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pulseaiclub/phi/internal/bashcomplete/spec"
)

// Source names where one completion came from. The picker shows it, so a
// literal history match reads differently from a word nobody typed yet.
type Source string

const (
	// SourceCommand completes the command word from executables on PATH.
	SourceCommand Source = "command"
	// SourceArgument completes an argument with words seen in the same position
	// of the same command in the project shell history.
	SourceArgument Source = "argument"
	// SourcePath completes a file or directory under the project directory.
	SourcePath Source = "path"
	// SourceFlag completes a flag from the flags the project ran the command
	// with, whatever position they sat in.
	SourceFlag Source = "flag"
	// SourceSpec completes a subcommand or flag from the embedded command
	// specs, before the project history knows the command.
	SourceSpec Source = "spec"
)

const (
	// wordLimit caps the rows any word-level source contributes. The picker
	// shows a scrolling window, not the whole list, so the cap only bounds the
	// request — not what the user can reach.
	wordLimit = 50
)

// Suggestion is one completion row. Command is the row the picker draws — the
// whole line for history rows, the completed word otherwise — and Insert is the
// text that replaces the word being typed (empty on history rows, which replace
// the whole line). Score only orders rows of one source; sources are already
// ordered by how literally they complete the typed text.
type Suggestion struct {
	Command string
	Insert  string
	Source  Source
	Score   float64
	// Description is the row's hint text: what the word does, for rows that
	// have a declared one. History rows have none.
	Description string
}

// Completer assembles "!" completions from its sources. A nil source is an
// empty one, so a caller can wire a completer before every source exists.
type Completer struct {
	// history returns project shell history, oldest first.
	history func() []string
	// commands returns executable names, as they would be typed.
	commands func() []string
	// cwd is the directory path words complete against.
	cwd string
	// specs is the command table, consulted before the sources that need
	// history to be useful. nil leaves those sources as the only ones.
	specs *spec.Table
	// discovery, when set, starts reading a command's --help once the command
	// word is complete and the table has nothing for it. It runs in the
	// background: the keystroke that triggers it answers from what is known.
	discovery *spec.Discoverer
	// values, when set, serves the rows a declared argument reads from the
	// project, such as the commits a `git log` word completes from.
	values *spec.Values
}

// New builds a completer over injected sources.
func New(history, commands func() []string, cwd string) *Completer {
	return &Completer{history: history, commands: commands, cwd: cwd, specs: spec.Builtin()}
}

// SetValues adds the reader cache that supplies positional argument values from
// the project, such as commit hashes and branch names.
func (c *Completer) SetValues(values *spec.Values) {
	if c == nil {
		return
	}
	c.values = values
}

// SetDiscovery adds help discovery to the completer: commands the table does
// not know get their --help read in the background, and their flags complete
// from the next keystroke on. The table the completer reads is the discoverer's,
// so the embedded specs stay the base of it.
func (c *Completer) SetDiscovery(discovery *spec.Discoverer) {
	if c == nil || discovery == nil {
		return
	}
	c.discovery = discovery
	c.specs = discovery.Table()
}

// Complete lists completions for typed, the "!" line text up to the cursor.
// Rows are ordered by how literally they complete the typed text: whole-line
// history matches first, then the source that fits the word being typed.
func (c *Completer) Complete(typed string) []Suggestion {
	if c == nil {
		return nil
	}
	// Only the command the cursor sits in is being typed: `cd doc && git sta`
	// completes git's arguments, and the words of every command before it are
	// already decided.
	segment, _ := Segment(typed)
	tokens := Tokenize(segment)
	word := tokens[len(tokens)-1]
	var suggestions []Suggestion
	command, hasCommand := CommandWord(tokens)
	switch {
	case pathWord(word.Text):
		// A path word completes against the directory it names, whatever
		// position it sits in: "./run.sh" is a first word, "doc/" is not.
		suggestions = append(suggestions, c.pathWords(word.Text)...)
	case !hasCommand || command == len(tokens)-1:
		// The command word itself completes from PATH. So does a line that
		// names no command yet, such as a lone launcher or assignment.
		suggestions = append(suggestions, c.commandWords(word.Text)...)
	case dashWord(word.Text):
		// A dash word is a flag being typed: it completes from the command's
		// declared flags first, then from the ones this project actually ran.
		// The spec knows the full set; the history knows what this checkout
		// uses.
		suggestions = append(suggestions, c.specWords(tokens, command, word.Text)...)
		if c.hasArgumentHistory(tokens, command) {
			suggestions = append(suggestions, c.flagWords(tokens, command)...)
		}
	case len(tokens)-1 == command+1:
		// The word right after the command name is its subcommand. A declared
		// subcommand leads; words the project already ran follow it, so a
		// command the table does not know still completes from history.
		suggestions = append(suggestions, c.specWords(tokens, command, word.Text)...)
		suggestions = append(suggestions, c.subcommandWords(tokens, command)...)
	default:
		// Deeper in the line the declared subcommands of the node the cursor
		// sits inside still complete (`git remote ` lists remote's), ahead of
		// the words history saw in this position and the values the argument
		// itself takes.
		suggestions = append(suggestions, c.specWords(tokens, command, word.Text)...)
		suggestions = append(suggestions, c.valueWords(tokens, command, word.Text)...)
		suggestions = append(suggestions, c.argumentWords(tokens, command)...)
	}
	if word.Text != "" && !pathWord(word.Text) {
		// A bare word can also name something in the project, where the shell
		// would take a file: `git add mai` completes main.go. Typed paths
		// already went through the first branch.
		suggestions = append(suggestions, c.pathWordsAt("", word.Text)...)
	}
	c.maybeDiscover(tokens, command, hasCommand)
	c.refreshSpecs()
	return dedupeSuggestions(suggestions)
}

// refreshSpecs picks up a table a discovery replaced: the completer reads its
// own field on the hot path, so the new table has to land there once, off the
// lock the discoverer holds while it reads.
func (c *Completer) refreshSpecs() {
	if c.discovery == nil {
		return
	}
	c.specs = c.discovery.Table()
}

// maybeDiscover starts a background --help read for a command the table does
// not know, once the command word itself is complete. Until the read lands the
// keystroke answers from history and PATH exactly as before.
func (c *Completer) maybeDiscover(tokens []Token, command int, hasCommand bool) {
	if c.discovery == nil || !hasCommand {
		return
	}
	name := wordOf(tokens, command)
	if name == "" || !pathFree(name) {
		return
	}
	// The word has to be finished: a half-typed command names nothing to run,
	// and the cursor sitting on the command word is exactly that.
	if command == len(tokens)-1 {
		return
	}
	if c.discovery.Warmed(name) {
		return
	}
	c.discovery.Discover(context.Background(), name)
}

// pathFree reports whether name is a bare command word rather than a path: a
// path is completed from the filesystem, never executed for its help.
func pathFree(name string) bool {
	return !pathWord(name)
}

// valueWords completes a positional argument from the values it takes: the
// commits of the repository for `git log`, the branches for `git switch`. The
// first keystroke in the slot starts the read and answers from history; the
// rows arrive on the next one.
func (c *Completer) valueWords(tokens []Token, command int, word string) []Suggestion {
	name := wordOf(tokens, command)
	if c.values == nil || c.specs == nil || name == "" {
		return nil
	}
	args := make([]string, 0, len(tokens))
	for _, token := range tokens[command+1:] {
		args = append(args, token.Text)
	}
	declared, read, ok := c.specs.Argument(name, args)
	if !ok {
		return nil
	}
	rows := c.values.Rows(declared.Generator, word)
	c.values.Refresh(context.Background(), declared.Generator, word, read)
	return candidateRows(rows)
}

// candidateRows turns spec candidates into suggestion rows.
func candidateRows(candidates []spec.Candidate) []Suggestion {
	suggestions := make([]Suggestion, 0, len(candidates))
	for _, candidate := range candidates {
		suggestions = append(suggestions, Suggestion{
			Command:     candidate.Name,
			Insert:      candidate.Insert,
			Source:      SourceSpec,
			Score:       1,
			Description: candidate.Description,
		})
	}
	return suggestions
}

// specWords completes a word from the embedded command specs: the command's
// declared subcommands and flags, including the description each carries. A
// command without a spec (or a build without one) contributes nothing.
func (c *Completer) specWords(tokens []Token, command int, word string) []Suggestion {
	name := wordOf(tokens, command)
	if c.specs == nil || name == "" {
		return nil
	}
	args := make([]string, 0, len(tokens))
	for _, token := range tokens[command+1:] {
		args = append(args, token.Text)
	}
	return candidateRows(c.specs.Complete(name, args, word))
}

// dashWord reports whether word is a flag being typed: a dash alone is one,
// unlike option, which a launcher's flag walk treats as not a flag at all.
func dashWord(word string) bool {
	return strings.HasPrefix(word, "-")
}

// wordOf returns the text of the token at index, or the empty word for an
// index past the end: the word a launcher's command sits at.
func wordOf(tokens []Token, index int) string {
	if index < 0 || index >= len(tokens) {
		return ""
	}
	return tokens[index].Text
}

// commandWords completes the command word from executable names: a word the
// shell would run is a word the user can be typing. A literal prefix ranks
// above a case-folded one, so the cut at wordLimit never drops a row that
// extends what was typed exactly.
func (c *Completer) commandWords(word string) []Suggestion {
	if word == "" || c.commands == nil {
		return nil
	}
	names := c.commands()
	literal := make([]Suggestion, 0, wordLimit)
	folded := make([]Suggestion, 0, wordLimit)
	for _, name := range names {
		if name == word || !hasPrefixFold(name, word) {
			continue
		}
		row := Suggestion{
			Command: name,
			Insert:  name + " ",
			Source:  SourceCommand,
			Score:   1,
		}
		if strings.HasPrefix(name, word) {
			literal = append(literal, row)
		} else {
			folded = append(folded, row)
		}
	}
	return capRows(append(literal, folded...))
}

// argumentWords completes the word being typed with words seen in the same
// position of the same command in the project history: "git sta" lists the
// subcommands the project actually runs, with no spec table to maintain.
// Frequency ranks first and most recent use breaks ties, so a word used often
// beats a word used once and a newer use beats an older one.
func (c *Completer) argumentWords(tokens []Token, command int) []Suggestion {
	word := tokens[len(tokens)-1].Text
	position := len(tokens) - 1
	return c.positionWords(tokens, command, position, word, SourceArgument)
}

// subcommandWords completes the first argument from every word the project has
// ever used after the command, whatever position it occupied. A word that had
// its own arguments later (`git add -p`) still names a subcommand here, which
// is where a one-word history would otherwise hide it.
func (c *Completer) subcommandWords(tokens []Token, command int) []Suggestion {
	word := tokens[len(tokens)-1].Text
	return c.positionWords(tokens, command, -1, word, SourceArgument)
}

// flagWords completes a flag from the flags the project ran this command with,
// whatever position they sat in: `git --no-p` finds --no-pager after one use.
func (c *Completer) flagWords(tokens []Token, command int) []Suggestion {
	word := tokens[len(tokens)-1].Text
	rows := c.positionWords(tokens, command, -1, word, SourceFlag)
	flags := make([]Suggestion, 0, len(rows))
	for _, row := range rows {
		if option(row.Command) {
			flags = append(flags, row)
		}
	}
	return flags
}

// hasArgumentHistory reports whether the project has run the command with an
// argument before, which is what tells a flag word to complete from history
// rather than stay quiet.
func (c *Completer) hasArgumentHistory(tokens []Token, command int) bool {
	if c.history == nil {
		return false
	}
	name := wordOf(tokens, command)
	if name == "" {
		return false
	}
	key := foldKey(name)
	for _, entry := range slices.Backward(c.history()) {
		line := Tokenize(entry)
		index, ok := CommandWord(line)
		if !ok || foldKey(line[index].Text) != key {
			continue
		}
		for i := index + 1; i < len(line); i++ {
			if line[i].Text != "" {
				return true
			}
		}
	}
	return false
}

// positionWords ranks words seen after the command in the project history,
// counting one position (position >= 0) or all of them (-1), and keeping only
// the ones that extend word. Rows ordered by frequency then recency; the sole
// candidate scores 1, so a word completed once reads as strongly as one whose
// count is the maximum.
func (c *Completer) positionWords(tokens []Token, command, position int, word string, source Source) []Suggestion {
	if c.history == nil {
		return nil
	}
	name := wordOf(tokens, command)
	if name == "" {
		return nil
	}
	key := foldKey(name)
	counts := make(map[string]int)
	recent := make(map[string]int)
	for _, entry := range slices.Backward(c.history()) {
		line := Tokenize(entry)
		index, ok := CommandWord(line)
		if !ok || foldKey(line[index].Text) != key {
			continue
		}
		if position >= 0 {
			if position <= index || position >= len(line) {
				continue
			}
		}
		start := index + 1
		end := len(line)
		if position >= 0 {
			start, end = position, position+1
		}
		for i := start; i < end; i++ {
			candidate := line[i].Text
			if candidate == "" || candidate == word {
				continue
			}
			if !hasPrefixFold(candidate, word) {
				continue
			}
			if _, seen := recent[candidate]; !seen {
				recent[candidate] = len(recent)
			}
			counts[candidate]++
		}
	}
	// The typed word itself is never a candidate: it is what the user wants
	// continued, not repeated. The counts above already skip it.
	words := make([]string, 0, len(counts))
	for candidate := range counts {
		words = append(words, candidate)
	}
	sort.Slice(words, func(a, b int) bool {
		if counts[words[a]] != counts[words[b]] {
			return counts[words[a]] > counts[words[b]]
		}
		return recent[words[a]] < recent[words[b]]
	})
	top := 1
	if len(words) > 0 {
		top = counts[words[0]]
	}
	suggestions := make([]Suggestion, 0, min(wordLimit, len(words)))
	for _, candidate := range words {
		suggestions = append(suggestions, Suggestion{
			Command: candidate,
			Insert:  candidate + " ",
			Source:  source,
			Score:   float64(counts[candidate]) / float64(top),
		})
	}
	return capRows(suggestions)
}

// capRows keeps the strongest wordLimit rows: the caller hands them over
// strongest first, so the cut drops only the tail.
func capRows(rows []Suggestion) []Suggestion {
	if len(rows) > wordLimit {
		return rows[:wordLimit]
	}
	return rows
}

// pathWord reports whether word names a path rather than a bare word: it
// already carries a separator, or is anchored at the root, the home directory,
// or the current directory.
func pathWord(word string) bool {
	switch {
	case word == "." || word == "..":
		return true
	case strings.HasPrefix(word, "/"),
		strings.HasPrefix(word, "./"),
		strings.HasPrefix(word, "../"),
		strings.HasPrefix(word, "~"):
		return true
	case strings.Contains(word, "/"), strings.Contains(word, `\`):
		// Backslash too: on Windows it is the separator users type.
		return true
	}
	return false
}

// pathWords completes a path word against the directory it names: entries of
// that directory that start with the typed base. Directories complete with a
// trailing separator so the next completion continues inside them; files
// complete with a space, ready for the next argument. Dotfiles are skipped
// unless the typed base itself starts with a dot, like a shell.
func (c *Completer) pathWords(word string) []Suggestion {
	sep := strings.LastIndexAny(word, `/\`)
	if sep < 0 {
		return nil
	}
	return c.pathWordsAt(word[:sep+1], word[sep+1:])
}

// pathWordsAt completes entries of dir that start with base, drawing each row
// with dir in front so it reads the way the line was started. An empty dir
// lists the project directory itself: that is the bare-name completion a word
// gets where a shell would take a file.
func (c *Completer) pathWordsAt(dir, base string) []Suggestion {
	root, err := c.resolveDir(dir)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		// An unreadable directory is an empty one: no rows, not an error to
		// surface on a keystroke.
		return nil
	}
	suggestions := make([]Suggestion, 0, wordLimit)
	for _, entry := range entries {
		name := entry.Name()
		if (entry.IsDir() || !option(base)) &&
			hasPrefixFold(name, base) && (strings.HasPrefix(base, ".") || !strings.HasPrefix(name, ".")) {
			// The typed directory prefix is kept as typed, so the insert reads
			// the way the line was started.
			text := dir + name
			insert := text + " "
			if entry.IsDir() {
				text, insert = text+"/", text+"/"
			}
			suggestions = append(suggestions, Suggestion{
				Command: text,
				Insert:  insert,
				Source:  SourcePath,
				Score:   1,
			})
			if len(suggestions) == wordLimit {
				break
			}
		}
	}
	return suggestions
}

// resolveDir expands the directory part of a path word to a directory the
// filesystem understands: "~" to the user's home directory, a relative path to
// the project directory. The result is cleaned only after the prefix the user
// typed is decided, so what lands in the composer keeps that prefix.
func (c *Completer) resolveDir(dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if dir == "~" {
			return home, nil
		}
		return filepath.Join(home, dir[2:]), nil
	}
	if filepath.IsAbs(dir) || c.cwd == "" {
		return filepath.Clean(dir), nil
	}
	return filepath.Join(c.cwd, dir), nil
}

// dedupeSuggestions drops rows that draw the same line, keeping the first
// occurrence: sources are ordered by strength, so a history row wins over a
// command row for the same word.
func dedupeSuggestions(suggestions []Suggestion) []Suggestion {
	if len(suggestions) < 2 {
		return suggestions
	}
	seen := make(map[string]struct{}, len(suggestions))
	out := suggestions[:0]
	for _, suggestion := range suggestions {
		if _, duplicate := seen[suggestion.Command]; duplicate {
			continue
		}
		seen[suggestion.Command] = struct{}{}
		out = append(out, suggestion)
	}
	return out
}
