package spec

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// generatorTimeout bounds one value read. A repository the user is typing in is
// a local read, and a slow one is worse than no row.
const generatorTimeout = 2 * time.Second

// generatorLineLimit bounds how many values one read contributes: the picker
// scrolls, but a long log is not a completion list.
const generatorLineLimit = 50

// Generator reads the values a positional argument takes, such as the commits
// in the repository the completer is working in.
type Generator func(ctx context.Context, dir, prefix string) []Candidate

// generators maps the name a spec declares to the reader that knows how to get
// it. A name the binary does not know reads nothing, which is what keeps a spec
// file from naming a program to run.
var generators = map[string]Generator{
	"commits":  commits,
	"branches": branches,
}

// GeneratorByName returns the reader a spec name refers to, so a caller outside
// this package can prime a cache or test one reader directly.
func GeneratorByName(name string) (Generator, bool) {
	read := generator(name)
	return read, read != nil
}

// generator returns the reader for a declared name.
func generator(name string) Generator {
	if name == "" {
		return nil
	}
	return generators[name]
}

// commits lists the repository's recent commits, newest first, each row drawn
// as its short hash and its subject: `git log 2343` completes 2343 and says
// what that commit is.
func commits(ctx context.Context, dir, prefix string) []Candidate {
	out, ok := runGit(ctx, dir, "log", "--max-count="+itoa(generatorLineLimit), "--all",
		"--format=%h%x09%s")
	if !ok {
		return nil
	}
	return parseRows(out, prefix, func(hash string) string { return hash + " " })
}

// branches lists local branch names, the ones a checkout or a switch would
// accept.
func branches(ctx context.Context, dir, prefix string) []Candidate {
	out, ok := runGit(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if !ok {
		return nil
	}
	return parseRows(out, prefix, func(name string) string { return name + " " })
}

// parseRows turns git's tab-separated output into candidates that extend prefix.
// insert turns the matched word into what accepting the row types, which is the
// word plus a space for both of these sources.
func parseRows(output, prefix string, insert func(string) string) []Candidate {
	var candidates []Candidate
	for line := range strings.SplitSeq(output, "\n") {
		name, description, _ := strings.Cut(line, "\t")
		if name == "" || !literalPrefix(name, prefix) {
			continue
		}
		candidates = append(candidates, Candidate{
			Name:        name,
			Description: description,
			Insert:      insert(name),
			Kind:        KindArgument,
		})
		if len(candidates) == generatorLineLimit {
			break
		}
	}
	return candidates
}

// runGit runs one read-only git command in dir. A missing git, a directory that
// is not a repository, and a command that hangs all look the same here: no
// rows.
func runGit(ctx context.Context, dir string, args ...string) (string, bool) {
	runCtx, cancel := context.WithTimeout(ctx, generatorTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...) //nolint:gosec // G204: fixed argv, no user text
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// itoa renders a small non-negative integer without pulling in formatting.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [8]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
