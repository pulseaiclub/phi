package compaction

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestExtractPathsFromArgs(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args string
		want []string
	}{
		{name: "path field", tool: "read", args: `{"path":"a/b.go"}`, want: []string{"a/b.go"}},
		{
			name: "file_path field",
			tool: "write",
			args: `{"file_path":"x/y.txt","content":""}`,
			want: []string{"x/y.txt"},
		},
		{
			name: "path takes precedence over file_path",
			tool: "read",
			args: `{"path":"p","file_path":"fp"}`,
			want: []string{"p"},
		},
		{name: "empty string", tool: "read", args: `""`, want: nil},
		{name: "invalid JSON", tool: "read", args: `{path}`, want: nil},
		{name: "empty object", tool: "read", args: `{}`, want: nil},
		{
			name: "edit payload targets",
			tool: "edit",
			args: `{"payload":"*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n*** SM:EDIT b.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
			want: []string{"a.ts", "b.ts"},
		},
		{name: "edit without payload", tool: "edit", args: `{}`, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathsFromArgs(tt.tool, tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFileOperation_extractMessageContent(t *testing.T) {
	t.Run("skip non-assistant messages", func(t *testing.T) {
		f := &FileOperation{}
		msg := llm.Message{
			Role: llm.RoleUser,
			ToolCalls: []llm.ToolCall{{
				Function: llm.Function{Name: "read", Arguments: `{"path":"x"}`},
			}},
		}
		f.extractMessageContent(msg)
		assert.Empty(t, f.read)
		assert.Empty(t, f.written)
		assert.Empty(t, f.edited)
	})

	t.Run("skip when no ToolCalls", func(t *testing.T) {
		f := &FileOperation{}
		msg := llm.Message{Role: llm.RoleAssistant, ToolCalls: nil}
		f.extractMessageContent(msg)
		assert.Empty(t, f.read)
	})

	t.Run("categorize paths by tool name", func(t *testing.T) {
		f := &FileOperation{}
		msg := llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{Function: llm.Function{Name: "read", Arguments: `{"path":"a.go"}`}},
				{Function: llm.Function{Name: "read", Arguments: `{"path":"b.go"}`}},
				{Function: llm.Function{Name: "write", Arguments: `{"file_path":"c.go","content":"x"}`}},
				{
					Function: llm.Function{
						Name:      "edit",
						Arguments: `{"payload":"*** SM:EDIT d.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
					},
				},
			},
		}
		f.extractMessageContent(msg)
		assert.Equal(t, []string{"a.go", "b.go"}, f.read)
		assert.Equal(t, []string{"c.go"}, f.written)
		assert.Equal(t, []string{"d.go"}, f.edited)
	})

	t.Run("skip tool calls without path", func(t *testing.T) {
		f := &FileOperation{}
		msg := llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{Function: llm.Function{Name: "read", Arguments: `{}`}},
				{Function: llm.Function{Name: "read", Arguments: `{"path":"ok.go"}`}},
			},
		}
		f.extractMessageContent(msg)
		assert.Equal(t, []string{"ok.go"}, f.read)
	})

	t.Run("skip non-file tools", func(t *testing.T) {
		f := &FileOperation{}
		msg := llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{Function: llm.Function{Name: "search", Arguments: `{"path":"x"}`}},
				{Function: llm.Function{Name: "bash", Arguments: `{}`}},
			},
		}
		f.extractMessageContent(msg)
		assert.Empty(t, f.read)
		assert.Empty(t, f.written)
		assert.Empty(t, f.edited)
	})

	t.Run("accumulate across multiple calls", func(t *testing.T) {
		f := &FileOperation{}
		f.extractMessageContent(llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{Function: llm.Function{Name: "read", Arguments: `{"path":"first.go"}`}},
			},
		})
		f.extractMessageContent(llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{Function: llm.Function{Name: "read", Arguments: `{"path":"second.go"}`}},
			},
		})
		assert.Equal(t, []string{"first.go", "second.go"}, f.read)
	})
}

func TestExtractFileOperations(t *testing.T) {
	ts := time.Now()

	t.Run("no previous compaction", func(t *testing.T) {
		messages := []llm.Message{
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{Function: llm.Function{Name: "read", Arguments: `{"path":"a.go"}`}},
				},
			},
		}
		got := extractFileOperations(messages, nil, -1)
		assert.Equal(t, []string{"a.go"}, got.read)
		assert.Empty(t, got.written)
		assert.Empty(t, got.edited)
	})

	t.Run("merge from previous compaction details", func(t *testing.T) {
		entries := []session.MessageEntry{
			session.CompactionEntry{
				SessionBaseEntry: session.SessionBaseEntry{ID: "c1", Type: session.EntryCompaction, Timestamp: ts},
				Compaction: session.Compaction{
					Details: session.CompactionDetails{
						ReadFiles:     []string{"r1.go", "r2.go"},
						ModifiedFiles: []string{"w1.go"},
					},
				},
			},
		}
		got := extractFileOperations(nil, entries, 0)
		assert.Equal(t, []string{"r1.go", "r2.go"}, got.read)
		assert.Equal(t, []string{"w1.go"}, got.written)
		assert.Empty(t, got.edited)
	})

	t.Run("merge previous details and current messages", func(t *testing.T) {
		entries := []session.MessageEntry{
			session.CompactionEntry{
				SessionBaseEntry: session.SessionBaseEntry{ID: "c1", Type: session.EntryCompaction, Timestamp: ts},
				Compaction: session.Compaction{
					Details: session.CompactionDetails{
						ReadFiles:     []string{"prev_read.go"},
						ModifiedFiles: []string{"prev_written.go"},
					},
				},
			},
		}
		messages := []llm.Message{
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{Function: llm.Function{Name: "read", Arguments: `{"path":"msg_read.go"}`}},
					{
						Function: llm.Function{
							Name:      "edit",
							Arguments: `{"payload":"*** SM:EDIT msg_edit.go\n*** SM:FIND\nx\n*** SM:PUT\ny\n"}`,
						},
					},
				},
			},
		}
		got := extractFileOperations(messages, entries, 0)
		assert.Equal(t, []string{"prev_read.go", "msg_read.go"}, got.read)
		assert.Equal(t, []string{"prev_written.go"}, got.written)
		assert.Equal(t, []string{"msg_edit.go"}, got.edited)
	})

	t.Run("empty previous details is a no-op", func(t *testing.T) {
		entries := []session.MessageEntry{
			session.CompactionEntry{
				SessionBaseEntry: session.SessionBaseEntry{ID: "c1", Type: session.EntryCompaction, Timestamp: ts},
			},
		}
		got := extractFileOperations(nil, entries, 0)
		assert.Empty(t, got.read)
		assert.Empty(t, got.written)
	})
}

func TestComputeFileLists(t *testing.T) {
	t.Run("no overlap between read and modified", func(t *testing.T) {
		f := &FileOperation{
			read:    []string{"a.go", "b.go"},
			written: []string{"c.go"},
			edited:  []string{"d.go"},
		}

		readFiles, modifiedFiles := computeFileLists(f)

		assert.Equal(t, []string{"a.go", "b.go"}, readFiles)
		assert.Equal(t, []string{"c.go", "d.go"}, modifiedFiles)
	})

	t.Run("read files that are later modified are excluded from readFiles", func(t *testing.T) {
		f := &FileOperation{
			read:    []string{"a.go", "b.go", "c.go"},
			written: []string{"b.go"},
			edited:  []string{"d.go"},
		}

		readFiles, modifiedFiles := computeFileLists(f)

		assert.Equal(t, []string{"a.go", "c.go"}, readFiles)
		assert.Equal(t, []string{"b.go", "d.go"}, modifiedFiles)
	})

	t.Run("deduplicates modified files and sorts", func(t *testing.T) {
		f := &FileOperation{
			read:    []string{"z.go", "a.go"},
			written: []string{"b.go", "b.go"},
			edited:  []string{"a.go", "c.go"},
		}

		readFiles, modifiedFiles := computeFileLists(f)

		// a.go is modified, so it should not appear in readFiles
		assert.Equal(t, []string{"z.go"}, readFiles)
		assert.Equal(t, []string{"a.go", "b.go", "c.go"}, modifiedFiles)
	})

	t.Run("duplicate reads collapse", func(t *testing.T) {
		f := &FileOperation{
			read:    []string{"b.go", "a.go", "b.go"},
			written: nil,
		}

		readFiles, modifiedFiles := computeFileLists(f)

		assert.Equal(t, []string{"a.go", "b.go"}, readFiles)
		assert.Empty(t, modifiedFiles)
	})

	t.Run("empty FileOperation yields empty slices", func(t *testing.T) {
		f := &FileOperation{}

		readFiles, modifiedFiles := computeFileLists(f)

		assert.Empty(t, readFiles)
		assert.Empty(t, modifiedFiles)
	})
}

func TestStripFileOperations(t *testing.T) {
	const readBlock = "\n\n<read-files>\na.go\n</read-files>"
	const modifiedBlock = "\n\n<modified-files>\nb.go\n</modified-files>"
	for _, tt := range []struct {
		name                   string
		summary                string
		previousFileOperations string
		want                   string
	}{
		{"no files", "summary\n", "", "summary\n"},
		{"read only", "summary" + readBlock, readBlock, "summary"},
		{"modified only", "summary" + modifiedBlock, modifiedBlock, "summary"},
		{"both", "summary" + readBlock + modifiedBlock, readBlock + modifiedBlock, "summary"},
		{
			"embedded example", "example" + readBlock + "\nmore context" + modifiedBlock, modifiedBlock,
			"example" + readBlock + "\nmore context",
		},
		{"unknown suffix", "summary" + readBlock, modifiedBlock, "summary" + readBlock},
		{"no details", "summary" + readBlock, "", "summary" + readBlock},
		{
			"incomplete block", "summary\n\n<read-files>\na.go", readBlock,
			"summary\n\n<read-files>\na.go",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripFileOperations(tt.summary, tt.previousFileOperations))
		})
	}
}
