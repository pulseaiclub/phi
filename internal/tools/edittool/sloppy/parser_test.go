package sloppy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Section
	}{
		{
			name:  "basic replace",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\nconst timeout = 1000;\n*** SM:PUT\nconst timeout = 5000;\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   2,
					Pattern: Pattern{
						Tokens: []PatternToken{
							{Kind: PatternTokenLiteral, Text: "const timeout = 1000;", Start: 0, End: 21},
						},
						Body: "const timeout = 1000;",
					},
					Rewrite: Rewrite{Kind: RewriteReplace, Text: "const timeout = 5000;"},
				}},
			}},
		},
		{
			name:  "empty put deletes",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\ndebugLog(request);\n*** SM:PUT\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   2,
					Pattern: Pattern{
						Tokens: []PatternToken{
							{Kind: PatternTokenLiteral, Text: "debugLog(request);", Start: 0, End: 18},
						},
						Body: "debugLog(request);",
					},
					Rewrite: Rewrite{Kind: RewriteReplace},
				}},
			}},
		},
		{
			name:  "op inherits the edit all flag",
			input: "*** SM:EDIT a.ts all\n*** SM:FIND\nlogger.debug(\n*** SM:PUT\nlogger.trace(\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   2,
					All:    true,
					Pattern: Pattern{
						Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "logger.debug(", Start: 0, End: 13}},
						Body:   "logger.debug(",
					},
					Rewrite: Rewrite{Kind: RewriteReplace, Text: "logger.trace("},
				}},
			}},
		},
		{
			name:  "bare edit header resets the all flag",
			input: "*** SM:EDIT a.ts all\n*** SM:FIND\nx\n*** SM:PUT\ny\n*** SM:EDIT\n*** SM:FIND\nz\n*** SM:PUT\nw\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{
					{
						Number: 1,
						Line:   2,
						All:    true,
						Pattern: Pattern{
							Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "x", Start: 0, End: 1}},
							Body:   "x",
						},
						Rewrite: Rewrite{Kind: RewriteReplace, Text: "y"},
					},
					{
						Number: 2,
						Line:   7,
						Pattern: Pattern{
							Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "z", Start: 0, End: 1}},
							Body:   "z",
						},
						Rewrite: Rewrite{Kind: RewriteReplace, Text: "w"},
					},
				},
			}},
		},
		{
			name: "coalesces sections in first-seen order",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n" +
				"*** SM:EDIT b.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n" +
				"*** SM:EDIT a.ts\n*** SM:FIND\nz\n*** SM:PUT\nw\n",
			want: []Section{
				{
					Path: "a.ts",
					Ops: []Operation{
						{
							Number: 1,
							Line:   2,
							Pattern: Pattern{
								Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "x", Start: 0, End: 1}},
								Body:   "x",
							},
							Rewrite: Rewrite{Kind: RewriteReplace, Text: "y"},
						},
						{
							Number: 3,
							Line:   12,
							Pattern: Pattern{
								Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "z", Start: 0, End: 1}},
								Body:   "z",
							},
							Rewrite: Rewrite{Kind: RewriteReplace, Text: "w"},
						},
					},
				},
				{
					Path: "b.ts",
					Ops: []Operation{{
						Number: 2,
						Line:   7,
						Pattern: Pattern{
							Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "x", Start: 0, End: 1}},
							Body:   "x",
						},
						Rewrite: Rewrite{Kind: RewriteReplace, Text: "y"},
					}},
				},
			},
		},
		{
			name:  "after inserts below the anchor",
			input: "*** SM:EDIT src/retry.ts\n*** SM:FIND\n\tlimit: number;\n*** SM:AFTER\n\tdelayMs: number;\n",
			want: []Section{{
				Path: "src/retry.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   2,
					Pattern: Pattern{
						Tokens: []PatternToken{
							{Kind: PatternTokenLiteral, Text: "\tlimit: number;", Start: 0, End: 15},
						},
						Body: "\tlimit: number;",
					},
					Rewrite: Rewrite{Kind: RewriteInsert, Text: "\tdelayMs: number;"},
				}},
			}},
		},
		{
			name:  "gap and selection tokens",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\ntimeout = …⟪1000│5000⟫…\nrun(timeout)\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   2,
					Pattern: Pattern{
						Tokens: []PatternToken{
							{Kind: PatternTokenLiteral, Text: "timeout = ", Start: 0, End: 10},
							{Kind: PatternTokenGap, Capture: 0, LineBounded: true, Start: 10, End: 13},
							{Kind: PatternTokenLiteral, Text: "1000", Start: 16, End: 20},
							{Kind: PatternTokenGap, Capture: 1, Start: 30, End: 33},
							{Kind: PatternTokenLiteral, Text: "\nrun(timeout)", Start: 33, End: 46},
						},
						Selections: []Selection{{Old: "1000", New: "5000", Start: 13, End: 30}},
						Body:       "timeout = …⟪1000│5000⟫…\nrun(timeout)",
					},
					Rewrite: Rewrite{Kind: RewriteInline},
				}},
			}},
		},
		{
			name: "drops read-output noise and line numbering",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\n" +
				"1| const timeout = 1000;\n" +
				"[Showing lines 1-3 of 3]\n" +
				"2-3: …\n" +
				"2| run(timeout);\n" +
				"[2 more lines in a.ts. use read to continue]\n" +
				"*** SM:PUT\n1| const timeout = 5000;\n2| run(timeout);\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   2,
					Pattern: Pattern{
						Tokens: []PatternToken{{
							Kind: PatternTokenLiteral,
							Text: "const timeout = 1000;\nrun(timeout);",
							End:  35,
						}},
						Body: "const timeout = 1000;\nrun(timeout);",
					},
					Rewrite: Rewrite{Kind: RewriteReplace, Text: "const timeout = 5000;\nrun(timeout);"},
				}},
			}},
		},
		{
			name:  "anchor-less put asserts desired content",
			input: "*** SM:EDIT a.ts\n*** SM:PUT\nfinal content\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{{
					Number:  1,
					Line:    2,
					Desired: true,
					Rewrite: Rewrite{Kind: RewriteReplace, Text: "final content"},
				}},
			}},
		},
		{
			name:  "quoted path with all flag",
			input: `*** SM:EDIT "my file all.ts" all` + "\n*** SM:FIND\nx\n*** SM:PUT\ny\n",
			want: []Section{{
				Path: "my file all.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   2,
					All:    true,
					Pattern: Pattern{
						Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "x", Start: 0, End: 1}},
						Body:   "x",
					},
					Rewrite: Rewrite{Kind: RewriteReplace, Text: "y"},
				}},
			}},
		},
		{
			// A fenced payload keeps its authored line numbers: the fence
			// line stays a blank line, so a diagnostic names the line the
			// model counted.
			name:  "strips an outer code fence",
			input: "```text\n*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n```\n",
			want: []Section{{
				Path: "a.ts",
				Ops: []Operation{{
					Number: 1,
					Line:   3,
					Pattern: Pattern{
						Tokens: []PatternToken{{Kind: PatternTokenLiteral, Text: "x", Start: 0, End: 1}},
						Body:   "x",
					},
					Rewrite: Rewrite{Kind: RewriteReplace, Text: "y"},
				}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			// A literal token must still be a verbatim slice of the cleaned body.
			for _, section := range got {
				for _, op := range section.Ops {
					for i, tok := range op.Pattern.Tokens {
						if tok.Kind == PatternTokenLiteral {
							assert.Equal(t, tok.Text, op.Pattern.Body[tok.Start:tok.End],
								"literal token %d does not match body[%d:%d]", i, tok.Start, tok.End)
						}
					}
				}
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		op    int
		want  string
	}{
		{
			name:  "prose without target",
			input: "just some prose\n",
			want:  "payload does not open with a file target",
		},
		{
			name:  "bare SM:EDIT without path",
			input: "*** SM:EDIT\n*** SM:FIND\nx\n*** SM:PUT\ny\n",
			want:  "*** SM:EDIT has no file path",
		},
		{
			name:  "find without action",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\nx\n",
			op:    1,
			want:  "*** SM:FIND body is never rewritten",
		},
		{
			name:  "after without anchor",
			input: "*** SM:EDIT a.ts\n*** SM:AFTER\ny\n",
			op:    1,
			want:  "*** SM:AFTER has no *** SM:FIND anchor",
		},
		{
			name:  "duplicate action header",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n*** SM:PUT\nz\n",
			op:    1,
			want:  "duplicate *** SM:PUT header",
		},
		{
			name:  "stray close marker",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\na ⟫ b\n*** SM:PUT\nc\n",
			op:    1,
			want:  "stray",
		},
		{
			name:  "unterminated selection",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\na ⟪b│c\n*** SM:PUT\nd\n",
			op:    1,
			want:  "unterminated",
		},
		{
			name:  "selection without divider",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\na ⟪b⟫ c\n*** SM:PUT\nd\n",
			op:    1,
			want:  "divider",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			var perr *ParseError
			require.ErrorAs(t, err, &perr)
			assert.Contains(t, perr.Msg, tt.want)
			assert.Equal(t, tt.op, perr.Op)
			if tt.op > 0 {
				assert.NotZero(t, perr.Line, "operation errors must carry a line number")
			}
		})
	}
}
