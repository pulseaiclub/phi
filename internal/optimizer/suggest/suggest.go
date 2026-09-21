package suggest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm/jev"
	"github.com/pulseaiclub/phi/internal/optimizer"
)

const (
	// defaultMinChars is how many characters the buffer needs before a
	// suggestion is worth a judgement call.
	defaultMinChars = 2
	// defaultLimit caps how many recent distinct commands one judgement sees.
	// The whole set has to fit in one request, so a long history is cut.
	defaultLimit = 100
	// maxCommandChars bounds one command sent to the judge: judgements read the
	// head, and an unbounded inline script costs tokens without changing which
	// command the user is completing.
	maxCommandChars = 240

	// completionKey asks which candidate the user is completing; gateKey asks
	// whether any candidate completes it at all. Both ride in one request.
	completionKey = "completion"
	gateKey       = "has_completion"
)

// DefaultGates are the shipped fuzzy-mode thresholds. Prefix mode does not
// consult them: a literal prefix match is an exact rule and needs no judged
// threshold to be believed.
var DefaultGates = Gates{Threshold: 0.5, MinScore: 0.3, StrongScore: 0.9}

// Mode describes how the candidates were chosen, and therefore how much the
// caller should trust the ranking.
type Mode string

const (
	// ModePrefix means at least one recent command literally starts with the
	// typed text, and only those were ranked. Case-sensitive, like a shell.
	ModePrefix Mode = "prefix"
	// ModeFuzzy means none did, so every recent command was ranked and the
	// answer must clear the gates before it is shown.
	ModeFuzzy Mode = "fuzzy"
)

// Gates are the fuzzy-mode thresholds that decide whether a ranking is worth
// showing.
type Gates struct {
	// Threshold is the minimum probability that some candidate completes the
	// input.
	Threshold float64
	// MinScore is the minimum probability of the top candidate.
	MinScore float64
	// StrongScore is a top score high enough to override a borderline
	// Threshold. The two signals are jagged in different places: on a tiny
	// history the gate under-fires for a real match while the choice is
	// decisive; on nonsense the choice spreads out while the gate is near zero.
	StrongScore float64
}

// candidate is one command a judgement may be asked about. ID identifies it in
// the question's criteria and in the returned probabilities.
type candidate struct {
	ID      string
	Command string
}

// Suggestion is one ranked candidate.
type Suggestion struct {
	Command string
	// Score is the probability the judge assigned to this candidate (0..1).
	Score float64
	// IsPrefix reports whether Command literally starts with the typed text.
	IsPrefix bool
}

// Result is one ranking. Typed is the text it was computed for: the caller
// compares it against the current buffer and discards a result that no longer
// matches, rather than showing a suggestion for what the user has since edited.
type Result struct {
	Typed         string
	Mode          Mode
	HasCompletion float64
	// Ranked holds every candidate, highest score first.
	Ranked []Suggestion
	Model  string
	Usage  jev.Usage
}

// Suggester ranks recent commands as completions for typed text.
type Suggester struct {
	backend optimizer.Backend
	// gates decide whether a fuzzy ranking is worth showing. See Pick.
	gates Gates
	// minChars is the buffer length below which no request is worth making.
	minChars int
	// limit caps how much history a judgement sees.
	limit int
	// prefixFilter narrows to literal prefix matches when any exist. Disabled,
	// the judge ranks every recent command, which asks more of it but can catch
	// a completion the prefix rule cannot see.
	prefixFilter bool
}

// Option adjusts suggester behavior.
type Option func(*Suggester) error

// WithGates sets the fuzzy-mode thresholds.
func WithGates(gates Gates) Option {
	return func(s *Suggester) error {
		for _, gate := range []struct {
			name  string
			value float64
		}{
			{name: "threshold", value: gates.Threshold},
			{name: "min score", value: gates.MinScore},
			{name: "strong score", value: gates.StrongScore},
		} {
			if gate.value <= 0 || gate.value > 1 {
				return fmt.Errorf("suggest: %s must be in (0, 1], got %v", gate.name, gate.value)
			}
		}
		s.gates = gates
		return nil
	}
}

// WithMinChars sets how many characters the buffer needs before a judgement is
// requested.
func WithMinChars(minChars int) Option {
	return func(s *Suggester) error {
		if minChars < 1 {
			return fmt.Errorf("suggest: minimum characters must be positive, got %d", minChars)
		}
		s.minChars = minChars
		return nil
	}
}

// WithLimit sets how many recent distinct commands one judgement sees.
func WithLimit(limit int) Option {
	return func(s *Suggester) error {
		if limit < 1 {
			return fmt.Errorf("suggest: limit must be positive, got %d", limit)
		}
		s.limit = limit
		return nil
	}
}

// WithoutPrefixFilter ranks every recent command even when some literally start
// with the typed text.
func WithoutPrefixFilter() Option {
	return func(s *Suggester) error {
		s.prefixFilter = false
		return nil
	}
}

// New creates a suggester over backend.
func New(backend optimizer.Backend, opts ...Option) (*Suggester, error) {
	if backend == nil {
		return nil, errors.New("suggest: backend is required")
	}
	suggester := &Suggester{
		backend:      backend,
		gates:        DefaultGates,
		minChars:     defaultMinChars,
		limit:        defaultLimit,
		prefixFilter: true,
	}
	for _, opt := range opts {
		if err := opt(suggester); err != nil {
			return nil, err
		}
	}
	return suggester, nil
}

// Suggest ranks history as completions for typed.
//
// history is the command history oldest first, as a shell records it; duplicates
// keep their most recent position, so a command run twice ranks once, at its
// newest occurrence. A Result with no candidates means there was nothing to ask
// about — too few characters typed, an exhausted history, or the typed text
// being the only match — and no request was made.
//
// Cancel ctx when the buffer changes: a superseded judgement must not be applied
// to text the user has already moved on from.
func (s *Suggester) Suggest(ctx context.Context, typed string, history []string) (Result, error) {
	if s == nil || s.backend == nil {
		return Result{}, errors.New("suggest: suggester is not configured")
	}
	if len([]rune(strings.TrimSpace(typed))) < s.minChars {
		return Result{Typed: typed}, nil
	}
	commands := recent(history, s.limit)
	mode, candidates := selectCandidates(typed, commands, s.prefixFilter)
	if len(candidates) == 0 {
		return Result{Typed: typed, Mode: mode}, nil
	}
	// A single literal prefix match needs no judgement: there is exactly one
	// command that can complete the text, and the prefix rule is exact.
	if mode == ModePrefix && len(candidates) == 1 {
		return Result{
			Typed:         typed,
			Mode:          mode,
			HasCompletion: 1,
			Ranked:        []Suggestion{{Command: candidates[0].Command, Score: 1, IsPrefix: true}},
		}, nil
	}

	response, err := s.backend.Evaluate(ctx, requestState(typed, candidates), questions(candidates))
	if err != nil {
		return Result{}, fmt.Errorf("suggest: judgement failed: %w", err)
	}
	ranked, err := rank(response, candidates, typed)
	if err != nil {
		return Result{}, err
	}
	hasCompletion, err := gate(response)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Typed:         typed,
		Mode:          mode,
		HasCompletion: hasCompletion,
		Ranked:        ranked,
		Model:         response.Model,
		Usage:         response.Usage,
	}, nil
}

// Pick returns the suggestion to show for one result, judged by the gates this
// suggester was configured with. The judgement and the display decision are
// separate steps so a caller can buffer a result and show it later, once it has
// confirmed the text it was computed for is still the current one.
func (s *Suggester) Pick(result Result) (Suggestion, bool) {
	if s == nil {
		return PickSuggestion(result, DefaultGates)
	}
	return PickSuggestion(result, s.gates)
}

// PickSuggestion returns the suggestion to show, or false when nothing clears
// the gates. Prefix mode always suggests the top candidate; fuzzy mode needs a
// confident enough top score, plus either the gate or a decisive top score.
func PickSuggestion(result Result, gates Gates) (Suggestion, bool) {
	if len(result.Ranked) == 0 {
		return Suggestion{}, false
	}
	top := result.Ranked[0]
	if result.Mode == ModePrefix {
		return top, true
	}
	if top.Score < gates.MinScore {
		return Suggestion{}, false
	}
	if result.HasCompletion >= gates.Threshold || top.Score >= gates.StrongScore {
		return top, true
	}
	return Suggestion{}, false
}

// recent returns the most recent limit distinct commands, newest first.
// Duplicates keep their most recent position, matching what HIST_IGNORE_ALL_DUPS
// would show.
func recent(history []string, limit int) []string {
	if limit < 1 {
		return nil
	}
	seen := make(map[string]struct{}, min(limit, len(history)))
	commands := make([]string, 0, min(limit, len(history)))
	for index := len(history) - 1; index >= 0 && len(commands) < limit; index-- {
		command := history[index]
		if strings.TrimSpace(command) == "" {
			continue
		}
		if _, duplicate := seen[command]; duplicate {
			continue
		}
		seen[command] = struct{}{}
		commands = append(commands, command)
	}
	return commands
}

// selectCandidates chooses which recent commands a judgement ranks. Literal
// prefix matching is exact, so code applies it: when any command starts with the
// typed text, only those are candidates. The command equal to the typed text is
// never a candidate — there is nothing left to complete.
func selectCandidates(typed string, commands []string, prefixFilter bool) (Mode, []candidate) {
	rest := make([]string, 0, len(commands))
	for _, command := range commands {
		if command != typed {
			rest = append(rest, command)
		}
	}
	// Without the filter every command is a candidate, which is the fuzzy path:
	// nothing has been narrowed by an exact rule, so the gates must still apply.
	var prefixed []string
	if prefixFilter {
		prefixed = make([]string, 0, len(rest))
		for _, command := range rest {
			if strings.HasPrefix(command, typed) {
				prefixed = append(prefixed, command)
			}
		}
	}
	chosen, mode := rest, ModeFuzzy
	if len(prefixed) > 0 {
		chosen, mode = prefixed, ModePrefix
	}
	candidates := make([]candidate, 0, len(chosen))
	for index, command := range chosen {
		candidates = append(candidates, candidate{ID: candidateID(index), Command: command})
	}
	return mode, candidates
}

// candidateID names the index-th candidate. Padded so ids sort and read as a
// list the judge can reference unambiguously.
func candidateID(index int) string { return fmt.Sprintf("C%02d", index) }

// rank turns the judge's probability distribution into a ranked list. The
// distribution, not the selected choice, is the ranking: every candidate gets
// its own score, and a candidate the judge left out scores zero.
func rank(response jev.Response, candidates []candidate, typed string) ([]Suggestion, error) {
	answer, ok := response.Answers[completionKey]
	if !ok {
		return nil, fmt.Errorf("suggest: judgement left %q unanswered", completionKey)
	}
	if answer.Type != jev.AnswerChoice {
		return nil, fmt.Errorf(
			"suggest: judge answered %q with type %q, want %q",
			completionKey,
			answer.Type,
			jev.AnswerChoice,
		)
	}
	ranked := make([]Suggestion, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, Suggestion{
			Command:  candidate.Command,
			Score:    answer.Probabilities[candidate.ID],
			IsPrefix: strings.HasPrefix(candidate.Command, typed),
		})
	}
	// Stable, so candidates the judge scored equally keep history order.
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].Score > ranked[b].Score })
	return ranked, nil
}

// gate reads the probability that any candidate completes the typed text.
func gate(response jev.Response) (float64, error) {
	answer, ok := response.Answers[gateKey]
	if !ok {
		return 0, fmt.Errorf("suggest: judgement left %q unanswered", gateKey)
	}
	if answer.Type != jev.AnswerNoul {
		return 0, fmt.Errorf(
			"suggest: judge answered %q with type %q, want %q",
			gateKey,
			answer.Type,
			jev.AnswerNoul,
		)
	}
	return answer.Noul, nil
}
