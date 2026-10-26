package spec

import "strings"

// FromHelp builds a command tree out of a command's own --help output. It reads
// the one shape nearly every CLI prints: a flags section whose entries start
// with a dash, each followed by its description on the same or the next line.
//
// The result is what the tool says about itself, so it beats a hand-written
// spec for the version actually installed; anything it does not recognize is
// simply absent, and the caller keeps the embedded tree for the rest.
func FromHelp(name, output string) Command {
	root := Command{Name: name, Source: "help"}
	listing := false
	pending := -1
	wrapped := -1
	for raw := range strings.SplitSeq(output, "\n") {
		line := cleanHelpLine(raw)
		if line == "" {
			// A blank line ends whatever the previous one was doing: it is the
			// separator between sections, and between a flag and the prose
			// that follows the table.
			pending = -1
			continue
		}
		trimmed := trimLeftSpace(line)
		if isSectionHeader(trimmed) {
			// Sections either list flags or list the words a command takes;
			// both end the previous entry.
			listing = positionalHeader(trimmed)
			pending = -1
			continue
		}
		fields := listFields(trimmed)
		options, described := helpOptions(fields)
		if len(options) > 0 {
			root.Options = append(root.Options, options...)
			// A description wrapped onto the following line belongs to the
			// option just read; one already on the line has nothing pending.
			pending = -1
			if !described {
				pending = len(root.Options) - 1
			}
			// A description that ends mid-clause keeps going below the table:
			// remember the column it started in to recognize that indent.
			wrapped = -1
			if len(fields) > 1 {
				wrapped = fieldColumn(line, fields[len(fields)-1])
			}
			continue
		}
		if wrapped > 0 && indentOf(raw) >= wrapped && len(root.Options) > 0 {
			// The last option's description continued under its own column,
			// past the end of the listing.
			index := len(root.Options) - 1
			root.Options[index].Description = joinSpace(root.Options[index].Description, trimmed)
			continue
		}
		if pending >= 0 && indentOf(raw) > 0 {
			// A continuation line: the option's description kept wrapping.
			root.Options[pending].Description = joinSpace(root.Options[pending].Description, trimmed)
			continue
		}
		if pending >= 0 && indentOf(raw) == 0 {
			// The table ends here: an unindented line after an entry is prose,
			// not a wrapped description.
			pending = -1
		}
		if listing && indentOf(raw) > 0 {
			// Each entry of a listing section names one word the command takes.
			word, _, _ := cutField(trimmed)
			if word != "" && !startsDash(word) {
				root.Subcommands = append(root.Subcommands, Command{Name: word})
			}
		}
	}
	if len(root.Options) == 0 && len(root.Subcommands) == 0 {
		return Command{}
	}
	return root
}

// fieldColumn returns the byte column a field starts at in its line, so a
// wrapped description can be matched against the column it started in.
func fieldColumn(line, field string) int {
	for i := 0; i+len(field) <= len(line); i++ {
		if line[i:i+len(field)] == field {
			return i
		}
	}
	return -1
}

// cutSpace splits the first space-separated word off s.
func cutSpace(s string) (word, rest string, found bool) {
	for i := 0; i < len(s); i++ {
		if helpSpace(s[i]) {
			return s[:i], trimLeftSpace(s[i:]), true
		}
	}
	return s, "", false
}

// helpOptions reads the flags an entry line declares, plus the description the
// rest of the line carries. One line may list several spellings of the same
// flag ("-h, --help"), and the name may sit in its own column ("-config string")
// while the description follows after a gap, so fields are grouped by kind:
// dashes are spellings, a lone word right after a spelling is its value
// signature, and everything from the first description word on is prose.
func helpOptions(fields []string) (options []Option, described bool) {
	if len(fields) == 0 || !startsDash(fields[0]) {
		return nil, false
	}
	// A column holding one word per field spells out a value signature; a
	// column holding a sentence is the description. The two are told apart by
	// how many fields the first non-dash column has, so "-config string" and
	// "-h, --help   help for tea" both read right.
	signature := signatureColumn(fields)
	described = false
	var description string
	for i, field := range fields {
		switch {
		case startsDash(field):
			options = append(options, splitSpellings(field)...)
		case i < signature:
			options[len(options)-1].Args = append(options[len(options)-1].Args, Arg{Name: trimBrackets(field)})
		default:
			description = joinSpace(description, trimBrackets(field))
		}
	}
	if description != "" {
		for i := range options {
			options[i].Description = description
		}
		described = true
	}
	return options, described
}

// splitSpellings separates the spellings one column holds. A list stops at the
// first entry without a dash, and each entry loses the comma that joined it to
// the next.
func splitSpellings(column string) []Option {
	var options []Option
	for _, entry := range splitOnCommas(column) {
		entry = trimSpace(entry)
		if !startsDash(entry) {
			break
		}
		// The value signature sits in the same column as the spellings; it
		// belongs to the flag the column opened.
		name, rest, hasSpace := cutSpace(entry)
		options = append(options, newOption(name))
		if hasSpace && rest != "" {
			options[len(options)-1].Args = append(options[len(options)-1].Args, Arg{Name: trimBrackets(rest)})
		}
	}
	return options
}

// splitOnCommas splits a column on commas, the way a help listing joins two
// spellings of one flag. A space is not a separator: it sits inside the
// value signature of a spelling, as in "--output <FILE>".
func splitOnCommas(column string) []string {
	var out []string
	start := -1
	for i := 0; i < len(column); i++ {
		if column[i] == ',' {
			if start >= 0 {
				out = append(out, column[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, column[start:])
	}
	return out
}

// signatureColumn returns how many of the leading fields are spellings or
// value words, leaving the rest as the description: the value words are the
// plain fields before the first field that ends a word list, which is the one
// carrying punctuation or a sentence.
func signatureColumn(fields []string) int {
	for i, field := range fields {
		if startsDash(field) {
			continue
		}
		if sentenceWord(field) || len(helpWords(field)) > 1 {
			return i
		}
	}
	return len(fields)
}

// sentenceWord reports whether a field reads as prose rather than as one token
// of a value signature: it carries punctuation, or it ends the listing.
func sentenceWord(word string) bool {
	if len(word) > 1 && (word[len(word)-1] == ',' || word[len(word)-1] == '.') {
		return true
	}
	return false
}

// helpWords counts the single-space-separated words of a field: a value
// signature is one token ("string", "<FILE>"), a description is several.
func helpWords(field string) []string {
	var words []string
	start := -1
	for i := 0; i < len(field); i++ {
		if helpSpace(field[i]) {
			if start >= 0 {
				words = append(words, field[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		words = append(words, field[start:])
	}
	return words
}

// newOption builds one spelling, separating an attached value signature such
// as "--output=FILE" into the flag name and its argument.
func newOption(spelling string) Option {
	name, value, hasValue := cutByte(spelling, '=')
	option := Option{Names: []string{name}}
	if hasValue && value != "" {
		option.Args = []Arg{{Name: trimBrackets(value)}}
	}
	return option
}

// cleanHelpLine trims the raw line and drops the box-drawing indent a few
// tools print around their help.
func cleanHelpLine(line string) string {
	line = trimSpace(line)
	if line == "" {
		return ""
	}
	if startsWith(line, "|") || startsWith(line, "│") {
		line = trimSpace(line[len("│"):])
	}
	return line
}

// isSectionHeader reports whether a line names a section rather than an entry.
func isSectionHeader(line string) bool {
	return endsWith(line, ":") && !startsDash(line) && indentOf(line) == 0
}

// positionalHeader reports whether a section header lists words the command
// takes rather than flags.
func positionalHeader(line string) bool {
	header := foldName(line)
	for _, word := range []string{"command", "subcommand", "argument", "positional"} {
		if contains(header, word) {
			return true
		}
	}
	return false
}

// trimBrackets drops the angle, square, or brace brackets around a value
// signature, so "<FILE>" reads as FILE in the picker.
func trimBrackets(value string) string {
	value = trimRightSpace(value)
	for len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '<' && last == '>') || (first == '[' && last == ']') || (first == '{' && last == '}') {
			value = trimSpace(value[1 : len(value)-1])
			continue
		}
		break
	}
	return value
}

// indentOf counts the leading spaces and tabs of a raw line.
func indentOf(line string) int {
	width := 0
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' {
			width++
			continue
		}
		if line[i] == '\t' {
			width += 4
			continue
		}
		break
	}
	return width
}

// startsDash reports whether a word is a flag spelling.
func startsDash(word string) bool {
	return word != "" && word[0] == '-'
}

// cutField splits the first whitespace-delimited field off a line.
func cutField(line string) (field, sep, rest string) {
	i := 0
	for i < len(line) && !helpSpace(line[i]) {
		i++
	}
	field = line[:i]
	if i < len(line) {
		sep = line[i : i+1]
		rest = line[i+1:]
	}
	return field, sep, rest
}

// listFields splits help columns: two or more spaces start a new column, so
// the words of one description stay together. A single tab does the same.
func listFields(line string) []string {
	var fields []string
	start := -1
	run := 0
	for i := 0; i < len(line); i++ {
		b := line[i]
		if b == '\t' {
			if start >= 0 {
				fields = append(fields, line[start:i])
				start = -1
			}
			continue
		}
		if b == ' ' {
			run++
			continue
		}
		if run >= 2 && start >= 0 {
			fields = append(fields, line[start:i-run])
			start = -1
		}
		run = 0
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, line[start:])
	}
	return fields
}

// helpSpace reports whether b separates fields in help output.
func helpSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r'
}

// cutByte splits s at the first occurrence of b.
func cutByte(s string, b byte) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// joinSpace joins two descriptions with one space, skipping empty halves.
func joinSpace(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " " + b
}

// trimSpace drops leading and trailing ASCII whitespace.
func trimSpace(s string) string {
	return trimRightSpace(trimLeftSpace(s))
}

// trimLeftSpace drops leading ASCII whitespace.
func trimLeftSpace(s string) string {
	i := 0
	for i < len(s) && helpSpace(s[i]) {
		i++
	}
	return s[i:]
}

// trimRightSpace drops trailing ASCII whitespace.
func trimRightSpace(s string) string {
	i := len(s)
	for i > 0 && helpSpace(s[i-1]) {
		i--
	}
	return s[:i]
}

// startsWith is strings.HasPrefix over the bytes it needs.
func startsWith(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

// endsWith is strings.HasSuffix over the bytes it needs.
func endsWith(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	offset := len(s) - len(suffix)
	for i := 0; i < len(suffix); i++ {
		if s[offset+i] != suffix[i] {
			return false
		}
	}
	return true
}

// contains reports whether s holds sub, ignoring ASCII case.
func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if foldEqual(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}
