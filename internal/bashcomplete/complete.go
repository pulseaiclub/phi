package bashcomplete

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Source names where one completion came from. The picker shows it, so a
// literal history match reads differently from a word nobody typed yet.
type Source string

const (
	// SourceHistory completes the whole line from the project shell history.
	SourceHistory Source = "history"
	// SourceCommand completes the command word from executables on PATH.
	SourceCommand Source = "command"
	// SourceArgument completes an argument with words seen in the same position
	// of the same command in the project shell history.
	SourceArgument Source = "argument"
	// SourcePath completes a file or directory under the project directory.
	SourcePath Source = "path"
)

const (
	// historyLineLimit caps whole-line rows: they are the strongest rows, and
	// the word-level sources behind them still need room in the picker.
	historyLineLimit = 5
	// wordLimit caps the rows any word-level source contributes.
	wordLimit = 10
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
}

// New builds a completer over injected sources.
func New(history, commands func() []string, cwd string) *Completer {
	return &Completer{history: history, commands: commands, cwd: cwd}
}

// Complete lists completions for typed, the "!" line text up to the cursor.
// Rows are ordered by how literally they complete the typed text: whole-line
// history matches first, then the source that fits the word being typed.
func (c *Completer) Complete(typed string) []Suggestion {
	if c == nil {
		return nil
	}
	tokens := Tokenize(typed)
	word := tokens[len(tokens)-1]
	var suggestions []Suggestion
	suggestions = append(suggestions, c.historyLines(typed)...)
	switch {
	case pathWord(word.Text):
		// A path word completes against the directory it names, whatever
		// position it sits in: "./run.sh" is a first word, "doc/" is not.
		suggestions = append(suggestions, c.pathWords(word.Text)...)
	case len(tokens) == 1:
		suggestions = append(suggestions, c.commandWords(word.Text)...)
	default:
		suggestions = append(suggestions, c.argumentWords(tokens)...)
	}
	return dedupeSuggestions(suggestions)
}

// historyLines lists recent whole commands that literally start with the typed
// text, newest first. The line equal to the typed text is excluded — there is
// nothing left to complete — and duplicates keep their most recent position,
// matching what HIST_IGNORE_ALL_DUPS would show.
func (c *Completer) historyLines(typed string) []Suggestion {
	query := strings.TrimSpace(typed)
	if query == "" || c.history == nil {
		return nil
	}
	history := c.history()
	seen := make(map[string]struct{}, historyLineLimit)
	lines := make([]string, 0, historyLineLimit)
	for _, command := range slices.Backward(history) {
		if len(lines) == historyLineLimit {
			break
		}
		if command == query || !strings.HasPrefix(command, query) {
			continue
		}
		if _, duplicate := seen[command]; duplicate {
			continue
		}
		seen[command] = struct{}{}
		lines = append(lines, command)
	}
	suggestions := make([]Suggestion, len(lines))
	for i, line := range lines {
		suggestions[i] = Suggestion{Command: line, Source: SourceHistory, Score: recencyScore(i, len(lines))}
	}
	return suggestions
}

// commandWords completes the command word from executable names: a word the
// shell would run is a word the user can be typing. Names keep the order the
// source lists them, which for PATH is alphabetical.
func (c *Completer) commandWords(word string) []Suggestion {
	if word == "" || c.commands == nil {
		return nil
	}
	suggestions := make([]Suggestion, 0, wordLimit)
	for _, name := range c.commands() {
		if name == word || !strings.HasPrefix(name, word) {
			continue
		}
		suggestions = append(suggestions, Suggestion{
			Command: name,
			Insert:  name + " ",
			Source:  SourceCommand,
			Score:   1,
		})
		if len(suggestions) == wordLimit {
			break
		}
	}
	return suggestions
}

// argumentWords completes the word being typed with words seen in the same
// position of the same command in the project history: "git sta" lists the
// subcommands the project actually runs, with no spec table to maintain.
// Frequency ranks first and most recent use breaks ties, so a word used often
// beats a word used once and a newer use beats an older one.
func (c *Completer) argumentWords(tokens []Token) []Suggestion {
	if c.history == nil {
		return nil
	}
	word := tokens[len(tokens)-1].Text
	command := tokens[0].Text
	position := len(tokens) - 1
	counts := make(map[string]int)
	recent := make(map[string]int)
	history := c.history()
	for _, entry := range slices.Backward(history) {
		line := Tokenize(entry)
		if len(line) <= position || line[0].Text != command {
			continue
		}
		candidate := line[position].Text
		if candidate == "" || candidate == word {
			continue
		}
		if _, seen := recent[candidate]; !seen {
			recent[candidate] = len(recent)
		}
		counts[candidate]++
	}
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
	suggestions := make([]Suggestion, 0, min(wordLimit, len(words)))
	for _, candidate := range words {
		if word != "" && !strings.HasPrefix(candidate, word) {
			continue
		}
		suggestions = append(suggestions, Suggestion{
			Command: candidate,
			Insert:  candidate + " ",
			Source:  SourceArgument,
			Score:   float64(counts[candidate]) / float64(counts[words[0]]),
		})
		if len(suggestions) == wordLimit {
			break
		}
	}
	return suggestions
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
	dir, base := word[:sep+1], word[sep+1:]
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
		if strings.HasPrefix(name, base) && (strings.HasPrefix(base, ".") || !strings.HasPrefix(name, ".")) {
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

// recencyScore turns a 0-based newest-first index into a 0..1 weight: the most
// recent match scores 1, and each older one weighs less. The picker shows it so
// a fresher completion reads differently from a stale one.
func recencyScore(index, total int) float64 {
	if total <= 1 {
		return 1
	}
	return 1 - float64(index)/float64(total)
}
