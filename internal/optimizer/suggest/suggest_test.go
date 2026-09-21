package suggest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm/jev"
)

type request struct {
	state     any
	questions jev.Questions
}

// fakeBackend records every judgement so tests can assert on the request shape,
// and answers with whatever the test wired up.
type fakeBackend struct {
	requests []request
	reply    func(request) (jev.Response, error)
}

func (f *fakeBackend) Evaluate(_ context.Context, state any, questions jev.Questions) (jev.Response, error) {
	req := request{state: state, questions: questions}
	f.requests = append(f.requests, req)
	if f.reply != nil {
		return f.reply(req)
	}
	return jev.Response{Model: "jev-test", Answers: map[string]jev.Answer{}}, nil
}

// judgement answers both questions: the choice carries the probabilities that
// rank the candidates, and the choice it selected is deliberately not a real
// candidate, so a ranking that trusted the selection instead of the distribution
// would fail.
func judgement(probabilities map[string]float64, hasCompletion float64) func(request) (jev.Response, error) {
	return func(request) (jev.Response, error) {
		return jev.Response{
			Model: "jev-test",
			Answers: map[string]jev.Answer{
				completionKey: {Type: jev.AnswerChoice, Choice: "C99", Probabilities: probabilities},
				gateKey:       {Type: jev.AnswerNoul, Noul: hasCompletion},
			},
		}, nil
	}
}

func newSuggester(t *testing.T, backend *fakeBackend, opts ...Option) *Suggester {
	t.Helper()
	suggester, err := New(backend, opts...)
	require.NoError(t, err)
	return suggester
}

// stateOf returns the state one judgement saw as the suggest state.
func stateOf(t *testing.T, req request) suggestState {
	t.Helper()
	state, ok := req.state.(suggestState)
	require.True(t, ok, "the judgement state is the suggest state")
	return state
}

func candidateCommands(candidates []candidate) []string {
	commands := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		commands = append(commands, candidate.Command)
	}
	return commands
}

func suggestionCommands(suggestions []Suggestion) []string {
	commands := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		commands = append(commands, suggestion.Command)
	}
	return commands
}

func sortedKeys(criteria map[string]any) []string { return slices.Sorted(maps.Keys(criteria)) }

func TestRecentKeepsNewestDistinctCommandsWithinTheLimit(t *testing.T) {
	history := []string{"ls", "git status", "ls", "npm test", "git status"}

	assert.Equal(t, []string{"git status", "npm test", "ls"}, recent(history, 3))
	assert.Equal(t, []string{"git status", "npm test"}, recent(history, 2))
	assert.Empty(t, recent(history, 0))
	assert.Equal(t, []string{"ls"}, recent([]string{"ls", "", "  "}, 3), "a blank entry is not a command")
}

func TestSelectCandidatesNarrowsToLiteralPrefixMatches(t *testing.T) {
	mode, candidates := selectCandidates(
		"cd w",
		[]string{"cd amp-1", "cd work/amp-1", "cd", "cd work/blog"},
		true,
	)

	assert.Equal(t, ModePrefix, mode)
	assert.Equal(t, []candidate{
		{ID: "C00", Command: "cd work/amp-1"},
		{ID: "C01", Command: "cd work/blog"},
	}, candidates, "only the prefix matches are ranked, renumbered from C00")
}

func TestSelectCandidatesIsCaseSensitiveAndFallsBackToFuzzy(t *testing.T) {
	mode, candidates := selectCandidates("Cd w", []string{"cd work/amp-1", "ls"}, true)

	assert.Equal(t, ModeFuzzy, mode, "a shell prefix is exact, so a case mismatch is not a match")
	assert.Equal(t, []string{"cd work/amp-1", "ls"}, candidateCommands(candidates))
}

func TestSelectCandidatesDropsTheExactTypedCommand(t *testing.T) {
	history := []string{"ls", "git status", "ls -la"}

	_, prefixed := selectCandidates("ls", history, true)
	assert.Equal(t, []string{"ls -la"}, candidateCommands(prefixed), "`ls` is not a completion of `ls`")

	_, fuzzy := selectCandidates("ls", history, false)
	assert.Equal(t, []string{"git status", "ls -la"}, candidateCommands(fuzzy), "unfiltered keeps history order")
}

func TestSuggestSendsOneRequestWithBothQuestions(t *testing.T) {
	backend := &fakeBackend{reply: judgement(map[string]float64{"C00": 0.6, "C01": 0.4}, 0.9)}
	suggester := newSuggester(t, backend)

	// Oldest first, as a shell records it; the request renders it newest first.
	result, err := suggester.Suggest(t.Context(), "git s", []string{"git status", "git stash", "npm test"})
	require.NoError(t, err)

	require.Len(t, backend.requests, 1, "both questions ride in one request")
	state := stateOf(t, backend.requests[0])
	assert.Equal(t, "git s", state.TypedSoFar)
	assert.Equal(t, "C00| git stash\nC01| git status", state.RecentCommands)

	questions := backend.requests[0].questions
	require.Len(t, questions, 2)
	choice, ok := questions[completionKey].(jev.Choice)
	require.True(t, ok, "the completion question is a choice over the candidate ids")
	assert.Equal(t, []string{"C00", "C01"}, sortedKeys(choice.Criteria))
	assert.NotEmpty(t, choice.Instructions)

	gate, ok := questions[gateKey].(jev.Noul)
	require.True(t, ok, "the gate question is a yes/no question")
	require.NotNil(t, gate.Criteria, "the gate describes both outcomes")
	assert.NotEmpty(t, gate.Criteria.True)
	assert.NotEmpty(t, gate.Criteria.False)

	assert.Equal(t, ModePrefix, result.Mode)
	assert.Equal(t, "jev-test", result.Model)
	assert.InDelta(t, 0.9, result.HasCompletion, 0.0001)
	assert.Equal(t, []Suggestion{
		{Command: "git stash", Score: 0.6, IsPrefix: true},
		{Command: "git status", Score: 0.4, IsPrefix: true},
	}, result.Ranked)
}

func TestSuggestFlattensAndTruncatesCommandsInTheStateOnly(t *testing.T) {
	multiLine := "for f in *; do\n echo $f\ndone"
	long := strings.Repeat("x", maxCommandChars+100)
	backend := &fakeBackend{reply: judgement(nil, 0.9)}
	suggester := newSuggester(t, backend)

	result, err := suggester.Suggest(t.Context(), "aa", []string{long, multiLine})
	require.NoError(t, err)

	lines := strings.Split(stateOf(t, backend.requests[0]).RecentCommands, "\n")
	require.Len(t, lines, 2, "a multi-line command must stay on one tagged line")
	assert.Equal(t, `C00| for f in *; do\n echo $f\ndone`, lines[0])
	tagged := strings.TrimPrefix(lines[1], "C01| ")
	assert.Equal(t, maxCommandChars, utf8.RuneCountInString(tagged), "the command is cut to the bound")
	assert.True(t, strings.HasSuffix(tagged, "…"), "a truncated command is marked as truncated")

	assert.Equal(t, []string{multiLine, long}, suggestionCommands(result.Ranked), "the caller sees the full command")
}

func TestSuggestRanksByProbabilitiesNotHistoryOrder(t *testing.T) {
	backend := &fakeBackend{reply: judgement(map[string]float64{"C00": 0.1, "C01": 0.2, "C02": 0.7}, 0.93)}
	// `gsutil ls` is a literal prefix match, so the filter would drop the other
	// candidates before the judge ever saw them.
	suggester := newSuggester(t, backend, WithoutPrefixFilter())

	result, err := suggester.Suggest(t.Context(), "gs", []string{"git status", "gsutil ls", "ls"})
	require.NoError(t, err)

	// History order is ls, gsutil ls, git status, and the prefix rule would pick
	// only gsutil ls: the ranking follows neither.
	assert.Equal(t, ModeFuzzy, result.Mode)
	assert.InDelta(t, 0.93, result.HasCompletion, 0.0001)
	assert.Equal(t, []Suggestion{
		{Command: "git status", Score: 0.7},
		{Command: "gsutil ls", Score: 0.2, IsPrefix: true},
		{Command: "ls", Score: 0.1},
	}, result.Ranked)
}

func TestSuggestPrefixModeSendsOnlyThePrefixMatches(t *testing.T) {
	backend := &fakeBackend{reply: judgement(map[string]float64{"C00": 0.4, "C01": 0.6}, 0.99)}
	suggester := newSuggester(t, backend)

	result, err := suggester.Suggest(
		t.Context(),
		"amp --",
		[]string{"amp", "amp --no-tui", "ls", "amp --no-tui --dir blog"},
	)
	require.NoError(t, err)

	assert.Equal(t, ModePrefix, result.Mode)
	assert.Equal(t,
		"C00| amp --no-tui --dir blog\nC01| amp --no-tui",
		stateOf(t, backend.requests[0]).RecentCommands,
		"only the prefix matches are sent, newest first",
	)
	assert.Equal(t, []Suggestion{
		{Command: "amp --no-tui", Score: 0.6, IsPrefix: true},
		{Command: "amp --no-tui --dir blog", Score: 0.4, IsPrefix: true},
	}, result.Ranked)
}

func TestSuggestAnswersASinglePrefixMatchWithoutARequest(t *testing.T) {
	backend := &fakeBackend{}
	suggester := newSuggester(t, backend)

	result, err := suggester.Suggest(t.Context(), "cd w", []string{"cd amp-1", "cd work/amp-1", "ls"})
	require.NoError(t, err)

	assert.Empty(t, backend.requests, "one possible completion leaves nothing to judge")
	assert.Equal(t, ModePrefix, result.Mode)
	assert.InDelta(t, 1.0, result.HasCompletion, 0.0001)
	assert.Equal(t, []Suggestion{{Command: "cd work/amp-1", Score: 1, IsPrefix: true}}, result.Ranked)
}

func TestSuggestMakesNoRequestWhenNothingCanCompleteTheTypedText(t *testing.T) {
	backend := &fakeBackend{}
	suggester := newSuggester(t, backend)

	result, err := suggester.Suggest(t.Context(), "ls", []string{"ls"})
	require.NoError(t, err)

	assert.Empty(t, backend.requests)
	assert.Empty(t, result.Ranked)
	assert.Zero(t, result.HasCompletion)
	assert.Equal(t, ModeFuzzy, result.Mode, "an emptied history leaves the fuzzy path")
}

func TestSuggestSkipsBuffersWithNothingToComplete(t *testing.T) {
	backend := &fakeBackend{}
	suggester := newSuggester(t, backend)

	for _, typed := range []string{"", " ", "\n\t", "l"} {
		result, err := suggester.Suggest(t.Context(), typed, []string{"git status"})
		require.NoError(t, err)
		assert.Equal(t, Result{Typed: typed}, result, "a buffer this short is not worth a request")
	}
	assert.Empty(t, backend.requests)

	// One character is enough when the caller opts into it.
	backend.reply = judgement(map[string]float64{"C00": 0.6, "C01": 0.4}, 0.9)
	short := newSuggester(t, backend, WithMinChars(1))
	_, err := short.Suggest(t.Context(), "l", []string{"ls", "lsof", "git status"})
	require.NoError(t, err)
	assert.Len(t, backend.requests, 1)
}

func TestSuggestCarriesTheTypedTextForStalenessChecks(t *testing.T) {
	backend := &fakeBackend{reply: judgement(map[string]float64{"C00": 0.6, "C01": 0.4}, 0.9)}
	suggester := newSuggester(t, backend)

	result, err := suggester.Suggest(t.Context(), "git s", []string{"git status", "git stash"})
	require.NoError(t, err)

	assert.Equal(t, "git s", result.Typed, "a caller can drop a result the buffer has moved past")
}

func TestSuggestRejectsAnswersItCannotUse(t *testing.T) {
	tests := []struct {
		name   string
		reply  func(request) (jev.Response, error)
		phrase string
	}{
		{
			name: "completion unanswered",
			reply: func(request) (jev.Response, error) {
				return jev.Response{Answers: map[string]jev.Answer{
					gateKey: {Type: jev.AnswerNoul, Noul: 0.9},
				}}, nil
			},
			phrase: `left "completion" unanswered`,
		},
		{
			name: "gate unanswered",
			reply: func(request) (jev.Response, error) {
				return jev.Response{Answers: map[string]jev.Answer{
					completionKey: {Type: jev.AnswerChoice, Probabilities: map[string]float64{}},
				}}, nil
			},
			phrase: `left "has_completion" unanswered`,
		},
		{
			name: "completion answered with the wrong type",
			reply: func(request) (jev.Response, error) {
				return jev.Response{Answers: map[string]jev.Answer{
					completionKey: {Type: jev.AnswerNoul, Noul: 0.9},
					gateKey:       {Type: jev.AnswerNoul, Noul: 0.9},
				}}, nil
			},
			phrase: `answered "completion" with type "noul"`,
		},
		{
			name: "gate answered with the wrong type",
			reply: func(request) (jev.Response, error) {
				return jev.Response{Answers: map[string]jev.Answer{
					completionKey: {Type: jev.AnswerChoice, Probabilities: map[string]float64{}},
					gateKey:       {Type: jev.AnswerChoice},
				}}, nil
			},
			phrase: `answered "has_completion" with type "choice"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggester := newSuggester(t, &fakeBackend{reply: tt.reply})

			result, err := suggester.Suggest(t.Context(), "git s", []string{"git status", "git stash"})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.phrase)
			assert.Equal(t, Result{}, result, "an unusable answer must not become a suggestion")
		})
	}
}

func TestSuggestSurfacesBackendFailureWithoutASuggestion(t *testing.T) {
	backend := &fakeBackend{reply: func(request) (jev.Response, error) {
		return jev.Response{}, errors.New("typesafe API error (500): boom")
	}}
	suggester := newSuggester(t, backend)

	result, err := suggester.Suggest(t.Context(), "git s", []string{"git status", "git stash"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, Result{}, result)
}

func TestPickSuggestionAlwaysShowsTheTopPrefixCandidate(t *testing.T) {
	result := Result{
		Mode:          ModePrefix,
		HasCompletion: 0.01,
		Ranked:        []Suggestion{{Command: "cmd0", Score: 0.2}, {Command: "cmd1", Score: 0.1}},
	}

	got, ok := PickSuggestion(result, DefaultGates)

	require.True(t, ok, "a literal prefix match needs no threshold to be believed")
	assert.Equal(t, "cmd0", got.Command)
}

func TestPickSuggestionGatesFuzzyMode(t *testing.T) {
	fuzzy := func(hasCompletion float64, scores ...float64) Result {
		ranked := make([]Suggestion, 0, len(scores))
		for index, score := range scores {
			ranked = append(ranked, Suggestion{Command: fmt.Sprintf("cmd%d", index), Score: score})
		}
		return Result{Mode: ModeFuzzy, HasCompletion: hasCompletion, Ranked: ranked}
	}

	tests := []struct {
		name   string
		result Result
		gates  Gates
		want   string
	}{
		{"both exactly at the gates", fuzzy(0.5, 0.3, 0.2), DefaultGates, "cmd0"},
		{"gate just under", fuzzy(0.49, 0.3), DefaultGates, ""},
		{"gate over-generous but the choice is flat", fuzzy(0.8, 0.29, 0.28), DefaultGates, ""},
		{"a decisive choice overrides a borderline gate", fuzzy(0.48, 0.97, 0.01), DefaultGates, "cmd0"},
		{"top score exactly at the strong score", fuzzy(0.07, 0.9), DefaultGates, "cmd0"},
		{"top score just under the strong score", fuzzy(0.07, 0.89), DefaultGates, ""},
		{
			"a raised min score still gates",
			fuzzy(0.99, 0.1),
			Gates{Threshold: 0.5, MinScore: 0.3, StrongScore: 0.05},
			"",
		},
		{"nothing ranked", fuzzy(1), DefaultGates, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PickSuggestion(tt.result, tt.gates)

			if tt.want == "" {
				assert.False(t, ok)
				assert.Equal(t, Suggestion{}, got)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tt.want, got.Command)
		})
	}
}

func TestPickUsesTheGatesTheSuggesterWasBuiltWith(t *testing.T) {
	result := Result{
		Mode:          ModeFuzzy,
		HasCompletion: 0.6,
		Ranked:        []Suggestion{{Command: "cmd0", Score: 0.4}},
	}

	// The gate signal alone would carry this ranking, so only a raised gate and
	// a raised strong score can withhold it.
	strict, err := New(&fakeBackend{}, WithGates(Gates{Threshold: 0.9, MinScore: 0.3, StrongScore: 0.99}))
	require.NoError(t, err)
	_, ok := strict.Pick(result)
	assert.False(t, ok, "neither signal clears the raised gates")

	loose, err := New(&fakeBackend{})
	require.NoError(t, err)
	got, ok := loose.Pick(result)
	require.True(t, ok)
	assert.Equal(t, "cmd0", got.Command)

	var unconfigured *Suggester
	got, ok = unconfigured.Pick(result)
	require.True(t, ok, "an unconfigured suggester falls back to the shipped gates")
	assert.Equal(t, "cmd0", got.Command)
}

func TestSuggestRejectsAnUnconfiguredSuggester(t *testing.T) {
	var suggester *Suggester

	_, err := suggester.Suggest(t.Context(), "gi", []string{"git status"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestNewValidatesBackendAndOptions(t *testing.T) {
	_, err := New(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend is required")

	backend := &fakeBackend{}
	for _, tt := range []struct {
		name string
		opt  Option
	}{
		{"zero min chars", WithMinChars(0)},
		{"negative min chars", WithMinChars(-1)},
		{"zero limit", WithLimit(0)},
		{"negative limit", WithLimit(-1)},
		{"zero threshold", WithGates(Gates{Threshold: 0, MinScore: 0.3, StrongScore: 0.9})},
		{"min score above one", WithGates(Gates{Threshold: 0.5, MinScore: 1.5, StrongScore: 0.9})},
		{"strong score above one", WithGates(Gates{Threshold: 0.5, MinScore: 0.3, StrongScore: 2})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(backend, tt.opt)
			require.Error(t, err)
		})
	}
}
