package spec

import (
	"sort"
	"strings"
)

// Candidate is one completion the spec table can offer at a position.
type Candidate struct {
	// Name is the text that would be typed: a subcommand name or a flag.
	Name        string
	Description string
	// Insert is what accepting the row puts in the composer. Flags end with a
	// space, subcommands do not: a subcommand usually takes arguments next.
	Insert string
	// Kind tells what the row is, for callers that rank it.
	Kind Kind
}

// Kind separates the two things a spec offers.
type Kind int

const (
	// KindSubcommand completes the word that selects a subcommand.
	KindSubcommand Kind = iota
	// KindOption completes a flag.
	KindOption
)

// Complete lists the spec candidates for the word being typed. name is the
// command word and args are the words already typed after it, so the table
// knows which subcommand the cursor is inside.
//
// Subcommands and options are both offered: at the first word a command's
// subcommands matter most, deeper in the line its flags do. Rows are ordered
// strongest first: literal prefixes above folded ones, then longer spellings,
// so "--message" leads and its "-m" alias follows.
func (t *Table) Complete(name string, args []string, word string) []Candidate {
	node, ok := t.Lookup(name, args)
	if !ok {
		return nil
	}
	// A dash word completes flags; any other word completes subcommands.
	// Mixing both would bury one under the other, and the word itself says
	// which of the two the user is typing.
	if strings.HasPrefix(word, "-") {
		// Options stay available past the first positional argument unless the
		// command takes positionals the shell would always finish for it, such
		// as a file path; offering flags there would bury the files.
		if node.NoOptionAfterArgument && len(positional(args)) > 0 {
			return nil
		}
		options := make([]Candidate, 0, len(node.Options))
		for _, opt := range node.Options {
			for _, spelling := range opt.Names {
				if !extends(spelling, word) {
					continue
				}
				options = append(options, Candidate{
					Name:        spelling,
					Description: optionDescription(opt),
					Insert:      spelling + " ",
					Kind:        KindOption,
				})
			}
		}
		rankRows(options, word)
		return options
	}
	candidates := make([]Candidate, 0, len(node.Subcommands))
	for _, child := range node.Subcommands {
		if !extends(child.Name, word) {
			continue
		}
		candidates = append(candidates, Candidate{
			Name:        child.Name,
			Description: child.Description,
			Insert:      child.Name,
			Kind:        KindSubcommand,
		})
	}
	rankRows(candidates, word)
	return candidates
}

// rankRows orders one kind of row, strongest first: a literal prefix above a
// folded one, then a longer spelling above its short alias, so "--message"
// leads and its "-m" alias follows.
func rankRows(rows []Candidate, word string) {
	sort.SliceStable(rows, func(a, b int) bool {
		aLiteral := literalPrefix(rows[a].Name, word)
		bLiteral := literalPrefix(rows[b].Name, word)
		if aLiteral != bLiteral {
			return aLiteral
		}
		return len(rows[a].Name) > len(rows[b].Name)
	})
}

// extends reports whether candidate continues word: a literal prefix, or a
// case-folded one. The word typed in full is not offered back — there is no
// rest of it to type — but a differently spelled match is offered, because
// accepting it corrects the spelling.
func extends(candidate, word string) bool {
	pos := 0
	for pos < len(word) && pos < len(candidate) && candidate[pos] == word[pos] {
		pos++
	}
	if pos == len(word) {
		return pos < len(candidate)
	}
	if pos == len(candidate) {
		return false
	}
	for i := 0; i < len(word) && i < len(candidate); i++ {
		if lowerASCII(candidate[i]) != lowerASCII(word[i]) {
			return false
		}
	}
	return len(candidate) >= len(word)
}

// literalPrefix reports whether candidate starts with word, byte for byte.
func literalPrefix(candidate, word string) bool {
	if len(candidate) < len(word) {
		return false
	}
	for i := 0; i < len(word); i++ {
		if candidate[i] != word[i] {
			return false
		}
	}
	return true
}

// foldEqual compares two words ignoring ASCII case. Folding by hand keeps the
// match independent of the platform's Unicode tables: command names and flags
// are ASCII.
func foldEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

// foldName is the lookup key for a command name: ASCII lower case, so `Git`
// and `git` name the same tree on every platform.
func foldName(name string) string {
	buf := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		buf[i] = lowerASCII(name[i])
	}
	return string(buf)
}

// Merge returns a table holding every command of the embedded specs and then
// every command of extra, which wins on a collision: a tree read out of the
// command's own output describes the installed version, not a hand-written
// approximation of it.
func Merge(base, extra *Table) *Table {
	merged := NewTable()
	for _, table := range []*Table{base, extra} {
		if table == nil {
			continue
		}
		for _, name := range table.Names() {
			node, ok := table.Lookup(name, nil)
			if !ok {
				continue
			}
			merged.set(node)
		}
	}
	return merged
}

// lowerASCII folds one ASCII byte, leaving everything else alone.
func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// optionDescription is the row hint for a flag: what it does, and what value
// it takes, so "--message" reads as "use the given text as the commit message"
// and "-d" as "<branch-name> · delete a merged branch".
func optionDescription(opt Option) string {
	if len(opt.Args) == 0 {
		return opt.Description
	}
	values := make([]string, 0, len(opt.Args))
	for _, arg := range opt.Args {
		if arg.Optional {
			values = append(values, "["+arg.Name+"]")
			continue
		}
		values = append(values, "<"+arg.Name+">")
	}
	value := joinSpaced(values)
	if opt.Description == "" {
		return value
	}
	return value + " · " + opt.Description
}

// joinSpaced joins words with single spaces.
func joinSpaced(words []string) string {
	return strings.Join(words, " ")
}

// positional keeps the words that are not flags.
func positional(args []string) []string {
	words := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "" && !option(arg) {
			words = append(words, arg)
		}
	}
	return words
}
