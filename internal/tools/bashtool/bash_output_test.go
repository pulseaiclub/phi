package bashtool

import (
	"os"
	"strings"
	"testing"
)

func TestBashOutputFormattingPreservesShortOutput(t *testing.T) {
	in := "a\nb\nc\n"
	got := formatBashOutput(in, false)
	if got != in {
		t.Fatalf("short output changed: %q", got)
	}
}

func TestBashOutputFormattingWritesTemp(t *testing.T) {
	var b strings.Builder
	for i := 0; i < BashMaxOutputLines+20; i++ {
		b.WriteString("line\n")
	}
	full := b.String()
	got := formatBashOutput(full, false)
	if !strings.Contains(got, "Full output:") {
		t.Fatalf("missing full-output notice: %q", got)
	}
	if !strings.Contains(got, "Showing lines") {
		t.Fatalf("missing range notice: %q", got)
	}
	// Extract path and confirm file exists with full content.
	idx := strings.Index(got, "Full output: ")
	rest := got[idx+len("Full output: "):]
	path := strings.TrimSpace(strings.Split(rest, "]")[0])
	t.Cleanup(func() { _ = os.Remove(path) })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != full {
		t.Fatalf("temp file content mismatch: got %d bytes want %d", len(data), len(full))
	}
}

func TestFormatCollectedBashOutputLabelsRetainedFile(t *testing.T) {
	var b strings.Builder
	for i := 0; i < BashMaxOutputLines+20; i++ {
		b.WriteString("line\n")
	}
	retained := b.String()

	got := formatBashOutput(retained, true)
	if !strings.Contains(got, "Retained output:") {
		t.Fatalf("missing retained-output notice: %q", got)
	}
	if strings.Contains(got, "Full output:") {
		t.Fatalf("collection-truncated output mislabeled as full: %q", got)
	}
	if !strings.Contains(got, "Showing lines 21-1020 of 1020") {
		t.Fatalf("collection notice changed real line range: %q", got)
	}
	if !strings.HasSuffix(got, collectTruncationNote) {
		t.Fatalf("missing collection truncation note: %q", got)
	}
	idx := strings.Index(got, "Retained output: ")
	path := strings.TrimSpace(strings.Split(got[idx+len("Retained output: "):], "]")[0])
	if path == "" {
		t.Fatal("missing retained output path")
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != retained {
		t.Fatalf("retained file contains display metadata: got %d bytes want %d", len(data), len(retained))
	}
}

func TestCollectionNoticeDoesNotConsumeDisplayBudget(t *testing.T) {
	output := strings.Repeat("x", BashMaxOutputBytes)
	got := formatBashOutput(output, true)
	if got != output+collectTruncationNote {
		t.Fatalf("collection notice altered output at display limit: got %d bytes want %d", len(got), len(output)+len(collectTruncationNote))
	}
	if strings.Contains(got, "Retained output:") || strings.Contains(got, "Showing lines") {
		t.Fatalf("collection notice caused an unnecessary temp dump: %q", got[len(output):])
	}
}

func TestTruncateBashTailPreservesTailSemantics(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		maxLines  int
		maxBytes  int
		wantTail  string
		wantRange string
	}{
		{
			name:      "line limit with trailing newline",
			output:    "one\ntwo\nthree\nfour\n",
			maxLines:  3,
			maxBytes:  64,
			wantTail:  "two\nthree\nfour",
			wantRange: "Showing lines 2-4 of 4",
		},
		{
			name:      "byte limit prefers complete lines",
			output:    "aaaa\nbbbb\ncccc",
			maxLines:  100,
			maxBytes:  8,
			wantTail:  "cccc",
			wantRange: "Showing lines 3-3 of 3",
		},
		{
			name:      "single long line keeps byte tail",
			output:    "0123456789ABC",
			maxLines:  100,
			maxBytes:  10,
			wantTail:  "3456789ABC",
			wantRange: "Showing lines 1-1 of 1",
		},
		{
			name:      "final empty line",
			output:    "one\n\n",
			maxLines:  1,
			maxBytes:  64,
			wantTail:  "",
			wantRange: "Showing lines 2-2 of 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			display, path := truncateBashTail(tc.output, tc.maxLines, tc.maxBytes, "Full output")
			if path == "" {
				t.Fatal("missing temp output path")
			}
			t.Cleanup(func() { _ = os.Remove(path) })
			if !strings.HasPrefix(display, tc.wantTail+"\n\n[") {
				t.Fatalf("display tail=%q, want prefix %q", display, tc.wantTail)
			}
			if !strings.Contains(display, tc.wantRange) {
				t.Fatalf("display=%q, want range %q", display, tc.wantRange)
			}
		})
	}
}
