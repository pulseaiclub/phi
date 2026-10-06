package sloppy

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// benchPayload builds a payload with ops anchored edits across files, mixing
// the common forms: gap-capturing replacement and inline selection.
func benchPayload(ops int) string {
	var b strings.Builder
	for i := range ops {
		b.WriteString("*** SM:EDIT src/file")
		b.WriteString(strconv.Itoa(i % 7))
		b.WriteString(".ts\n*** SM:FIND\nfunction step")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("(input) {\n  const value = …;\n  return ")
		if i%2 == 0 {
			b.WriteString("value;\n}\n*** SM:PUT\nfunction step")
			b.WriteString(strconv.Itoa(i))
			b.WriteString("(input) {\n  const value = …;\n  return check(value);\n}\n")
		} else {
			b.WriteString("⟪value│check(value)⟫;\n}\n")
		}
	}
	return b.String()
}

func BenchmarkParse(b *testing.B) {
	payload := benchPayload(100)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := Parse(payload)
		require.NoError(b, err)
	}
}

func BenchmarkLex(b *testing.B) {
	payload := benchPayload(100)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		lx := NewLexer(payload)
		for lx.Next() {
			_ = lx.Token()
		}
	}
}
