package suggest

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm/jev"
)

// Question prose lives in static files: it is model input, and the exact wording
// was benchmarked on the reference implementation, so it is ported verbatim
// rather than restated.
var (
	//go:embed completion-question.md
	completionInstructions string
	//go:embed gate-question.md
	gateInstructions string
	//go:embed gate-criteria.txt
	gateCriteriaText string
)

// gateCriteria describes both outcomes of the gate question. It is parsed once:
// a missing side would leave the judge guessing what counts as a completion.
var gateCriteria = parseGateCriteria(gateCriteriaText)

// suggestState is the shared context one judgement sees. Field names are part of
// the question prose, so they are fixed wire names rather than Go style.
type suggestState struct {
	TypedSoFar     string `json:"typed_so_far"`
	RecentCommands string `json:"recent_commands"`
}

// questions builds the two questions asked in one request. The choice's criteria
// are the candidate ids, so its probability distribution ranks every candidate;
// the gate exists because those probabilities always sum to one, which would
// otherwise let the closest irrelevant command always win.
func questions(candidates []candidate) jev.Questions {
	criteria := make(map[string]any, len(candidates))
	for _, candidate := range candidates {
		criteria[candidate.ID] = nil
	}
	return jev.Questions{
		completionKey: jev.Choice{
			Instructions: strings.TrimSpace(completionInstructions),
			Criteria:     criteria,
		},
		gateKey: jev.Noul{
			Instructions: strings.TrimSpace(gateInstructions),
			Criteria:     &jev.NoulCriteria{True: gateCriteria.True, False: gateCriteria.False},
		},
	}
}

// requestState renders the candidates as the tagged one-per-line list the
// question prose promises: `<id>| <command>`, most recent first.
func requestState(typed string, candidates []candidate) suggestState {
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		lines = append(lines, candidate.ID+"| "+displayCommand(candidate.Command))
	}
	return suggestState{TypedSoFar: typed, RecentCommands: strings.Join(lines, "\n")}
}

// displayCommand flattens a command onto one tagged line and cuts it to the
// judged length. Multi-line commands would otherwise break the one-per-line
// contract the question prose relies on.
func displayCommand(command string) string {
	flat := strings.ReplaceAll(command, "\n", `\n`)
	runes := []rune(flat)
	if len(runes) <= maxCommandChars {
		return flat
	}
	return string(runes[:maxCommandChars-1]) + "…"
}

// parseGateCriteria reads `true: description` and `false: description` lines.
// Descriptions contain colons of their own, so the split is on the first
// separator only.
func parseGateCriteria(text string) jev.NoulCriteria {
	criteria := jev.NoulCriteria{}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, description, found := strings.Cut(line, ":")
		if !found {
			panic(fmt.Sprintf("suggest: gate criteria line %q has no %q separator", line, ":"))
		}
		switch strings.TrimSpace(key) {
		case "true":
			criteria.True = strings.TrimSpace(description)
		case "false":
			criteria.False = strings.TrimSpace(description)
		default:
			panic(fmt.Sprintf("suggest: gate criteria line %q is neither %q nor %q", line, "true", "false"))
		}
	}
	if criteria.True == nil || criteria.False == nil {
		panic("suggest: gate criteria must describe both outcomes")
	}
	return criteria
}
