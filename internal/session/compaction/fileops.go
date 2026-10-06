package compaction

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

// FileOperation tracks the file paths read, written, or edited by assistant
// tool calls so compaction can persist them with the summary.
type FileOperation struct {
	read    []string
	written []string
	edited  []string
}

// extractPathsFromArgs lists the file paths a tool call touches. An edit call
// names its targets inside the sloppy payload.
func extractPathsFromArgs(name, args string) []string {
	if name == "edit" {
		var in struct {
			Payload string `json:"payload"`
			Patch   string `json:"patch"`
			Input   string `json:"input"`
		}
		if err := json.Unmarshal([]byte(args), &in); err != nil {
			return nil
		}
		payload := in.Payload
		if payload == "" {
			payload = in.Patch
		}
		if payload == "" {
			payload = in.Input
		}
		return sloppy.TargetPaths(payload)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil || m == nil {
		return nil
	}
	if p, ok := m["path"].(string); ok && p != "" {
		return []string{p}
	}
	if p, ok := m["file_path"].(string); ok && p != "" {
		return []string{p}
	}
	return nil
}

func (f *FileOperation) extractMessageContent(message llm.Message) {
	if message.Role != llm.RoleAssistant {
		return
	}
	if len(message.ToolCalls) == 0 {
		return
	}
	for _, toolCall := range message.ToolCalls {
		name := toolCall.Function.Name
		for _, path := range extractPathsFromArgs(name, toolCall.Function.Arguments) {
			switch name {
			case "read":
				f.read = append(f.read, path)
			case "write":
				f.written = append(f.written, path)
			case "edit":
				f.edited = append(f.edited, path)
			}
		}
	}
}

func extractFileOperations(
	messages []llm.Message,
	entries []session.MessageEntry,
	prevCompactionIndex int,
) *FileOperation {
	var prevRead, prevWritten []string
	if prevCompactionIndex >= 0 {
		d := entries[prevCompactionIndex].(session.CompactionEntry).Compaction.Details
		prevRead, prevWritten = d.ReadFiles, d.ModifiedFiles
	}
	extra := 0
	for _, msg := range messages {
		extra += len(msg.ToolCalls)
	}
	fileOps := &FileOperation{
		read:    make([]string, 0, len(prevRead)+extra),
		written: make([]string, 0, len(prevWritten)+extra),
		edited:  make([]string, 0, extra),
	}
	fileOps.read = append(fileOps.read, prevRead...)
	fileOps.written = append(fileOps.written, prevWritten...)
	for _, msg := range messages {
		fileOps.extractMessageContent(msg)
	}
	return fileOps
}

func formatFileOperations(readFiles, modifiedFiles []string) string {
	sections := []string{}
	if len(readFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(readFiles, "\n")+"\n</read-files>")
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(modifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

// Match the suffix reconstructed from the previous Details so similar text
// in older summaries is not mistaken for an appended file-operation block.
func stripFileOperations(summary, previousFileOperations string) string {
	if previousFileOperations != "" && strings.HasSuffix(summary, previousFileOperations) {
		return strings.TrimSuffix(summary, previousFileOperations)
	}
	return summary
}

func computeFileLists(fileOps *FileOperation) ([]string, []string) {
	modifiedSet := make(map[string]struct{})
	readFiles := []string{}
	modifiedFiles := []string{}

	for _, p := range fileOps.edited {
		modifiedSet[p] = struct{}{}
	}
	for _, p := range fileOps.written {
		modifiedSet[p] = struct{}{}
	}

	// A file is often read more than once, and a previous compaction hands its
	// reads back on every round, so collapse duplicates before listing them.
	readSet := make(map[string]struct{}, len(fileOps.read))
	for _, p := range fileOps.read {
		if _, modified := modifiedSet[p]; modified {
			continue
		}
		if _, dup := readSet[p]; dup {
			continue
		}
		readSet[p] = struct{}{}
		readFiles = append(readFiles, p)
	}

	for p := range modifiedSet {
		modifiedFiles = append(modifiedFiles, p)
	}

	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)

	return readFiles, modifiedFiles
}
