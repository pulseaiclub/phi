package bashcomplete

import "strings"

// commonSubcommands lists the words users commonly type after popular
// commands, roughly by how often they are typed: with no history to rank by,
// order is the only ranking the table has. The list is deliberately small —
// it is a cold-start seed for a project that has not run the command yet, not
// a spec of what the command accepts.
var commonSubcommands = map[string][]string{
	"git": {
		"add", "checkout", "commit", "status", "push", "pull", "branch",
		"diff", "log", "merge", "rebase", "restore", "stash", "switch", "tag",
	},
	"docker": {
		"ps", "build", "run", "exec", "logs", "images", "pull", "push",
		"stop", "rm", "compose", "inspect", "start",
	},
	"npm": {
		"install", "run", "test", "start", "build", "ci", "update", "publish",
		"init", "link", "uninstall",
	},
	"go": {
		"test", "build", "run", "mod", "vet", "fmt", "generate", "install",
		"get", "doc", "clean", "tool",
	},
}

// commonWords completes an argument from the common-subcommand table. Rows
// keep the table's order, and the word already typed whole is never offered.
func commonWords(command, word string) []Suggestion {
	words, known := commonSubcommands[command]
	if !known {
		return nil
	}
	suggestions := make([]Suggestion, 0, min(wordLimit, len(words)))
	for _, candidate := range words {
		if candidate == word || (word != "" && !strings.HasPrefix(candidate, word)) {
			continue
		}
		suggestions = append(suggestions, Suggestion{
			Command: candidate,
			Insert:  candidate + " ",
			Source:  SourceCommon,
			Score:   1,
		})
		if len(suggestions) == wordLimit {
			break
		}
	}
	return suggestions
}
