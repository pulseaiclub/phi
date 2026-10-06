// Command sloppy-demo parses a sloppy edit payload and prints its operation
// IR, so the lexer and parser stages can be inspected directly.
//
// Usage:
//
//	sloppy-demo              # run the built-in example
//	sloppy-demo < payload    # parse a payload from stdin
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

const demoPayload = `*** SM:EDIT a.ts
*** SM:FIND
timeout = …⟪1000│5000⟫…
run(timeout)
*** SM:EDIT src/retry.ts
*** SM:FIND
	limit: number;
*** SM:AFTER
	/** Delay between attempts in ms */
	delayMs: number;
`

func main() {
	input, err := readInput()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("== payload ==")
	fmt.Println(strings.TrimRight(input, "\n"))

	sections, err := sloppy.Parse(input)
	if err != nil {
		fmt.Printf("\n== error ==\n%v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n== parsed ==")
	for _, section := range sections {
		printSection(section)
	}
}

// readInput returns the payload from stdin, or the built-in example when
// stdin is a terminal or empty.
func readInput() (string, error) {
	if stat, err := os.Stdin.Stat(); err != nil || stat.Mode()&os.ModeCharDevice != 0 {
		return demoPayload, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return demoPayload, nil
	}
	return string(data), nil
}

func printSection(section sloppy.Section) {
	fmt.Printf("section %q  (%d ops)\n", section.Path, len(section.Ops))
	for _, op := range section.Ops {
		printOp(op)
	}
}

func printOp(op sloppy.Operation) {
	attrs := []string{fmt.Sprintf("line %d", op.Line)}
	if op.All {
		attrs = append(attrs, "all")
	}
	if op.Desired {
		attrs = append(attrs, "desired")
	}
	fmt.Printf("  op %d  %s\n", op.Number, strings.Join(attrs, ", "))
	if op.Desired {
		fmt.Printf("    desired content: %q\n", op.Rewrite.Text)
		return
	}

	pat := op.Pattern
	fmt.Printf("    body: %q\n", pat.Body)
	if pat.EdgeGaps.Leading || pat.EdgeGaps.Trailing {
		fmt.Printf("    edge gaps: leading=%v trailing=%v\n", pat.EdgeGaps.Leading, pat.EdgeGaps.Trailing)
	}
	for _, tok := range pat.Tokens {
		switch tok.Kind {
		case sloppy.PatternTokenLiteral:
			fmt.Printf("      literal  [%d,%d)  %q\n", tok.Start, tok.End, tok.Text)
		case sloppy.PatternTokenGap:
			scope := "spans lines"
			if tok.LineBounded {
				scope = "line-bounded"
			}
			fmt.Printf("      gap      [%d,%d)  capture=%d  %s\n", tok.Start, tok.End, tok.Capture, scope)
		}
	}
	for _, sel := range pat.Selections {
		fmt.Printf("      select   [%d,%d)  %q -> %q\n", sel.Start, sel.End, sel.Old, sel.New)
	}
	fmt.Printf("    rewrite (%s): %q\n", op.Rewrite.Kind, op.Rewrite.Text)
}
