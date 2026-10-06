package edittool

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

// FuzzApply feeds adversarial payloads and file texts through parse and apply.
// The engine must fail closed with an error; it must never panic, whatever the
// byte-offset math does with degenerate input.
func FuzzApply(f *testing.F) {
	seeds := []struct{ payload, content string }{
		{"*** SM:EDIT a.ts\n*** SM:FIND\nrun();\n*** SM:PUT\ngo();\n", "run();\n"},
		{"*** SM:EDIT a.ts\n*** SM:FIND\nload(…);\n*** SM:PUT\nload(…);\n", "load(x, y);\n"},
		{"*** SM:EDIT a.ts\n*** SM:FIND\nx = …⟪1│2⟫…;\n", "x = 1;\n"},
		{"*** SM:EDIT a.ts\n*** SM:FIND\n…\nrun\n…\n*** SM:PUT\n…\nrun\n…\n", "a\nrun\nb\n"},
		{"*** SM:EDIT a.ts\n*** SM:FIND\n😀…😀\n*** SM:PUT\n…\n", "😀 x 😀\n"},
		{"*** SM:EDIT a.ts\n*** SM:PUT\n", ""},
		{"*** SM:EDIT a.ts all\n*** SM:FIND\n⟪│x⟫\n", "x\ny\n"},
	}
	for _, seed := range seeds {
		f.Add(seed.payload, seed.content)
	}
	f.Fuzz(func(_ *testing.T, payload, content string) {
		sections, err := sloppy.Parse(payload)
		if err != nil {
			return
		}
		for _, section := range sections {
			// Any outcome is acceptable; a panic is not.
			_, _ = Apply(content, section, true)
		}
	})
}

// TestApplyStressSurvivesDegenerateShapes pins the same property with a
// deterministic generator, so CI sees it without a fuzzing run.
func TestApplyStressSurvivesDegenerateShapes(t *testing.T) {
	fragments := []string{
		"a", "b", "…", "\n", "⟪", "⟫", "│", " ", "\t", "😀", "…", "run();", "\r\n",
		"⟪x│y⟫", "⟪…│z⟫", "⟪│q⟫", "***", "│", "\n\n", "0", "}", "…\n",
	}
	rng := rand.New(rand.NewPCG(1, 2)) // deterministic stress input
	for range 2000 {
		var payload strings.Builder
		payload.WriteString("*** SM:EDIT a.ts\n*** SM:FIND\n")
		for range rng.IntN(6) + 1 {
			payload.WriteString(fragments[rng.IntN(len(fragments))])
		}
		if rng.IntN(2) == 0 {
			payload.WriteString("\n*** SM:PUT\n")
			for range rng.IntN(4) {
				payload.WriteString(fragments[rng.IntN(len(fragments))])
			}
		}

		var content strings.Builder
		for range rng.IntN(8) + 1 {
			content.WriteString(fragments[rng.IntN(len(fragments))])
		}

		sections, err := sloppy.Parse(payload.String())
		if err != nil {
			continue
		}
		require.NotPanics(t, func() {
			for _, section := range sections {
				_, _ = Apply(content.String(), section, true)
			}
		})
	}
}
