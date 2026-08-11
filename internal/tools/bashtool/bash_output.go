package bashtool

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// BashMaxOutputLines is the maximum number of lines kept in displayed bash output.
	BashMaxOutputLines = 1000
	// BashMaxOutputBytes is the maximum bytes kept in displayed bash output (50KB).
	BashMaxOutputBytes = 50 * 1024
	// BashMaxCollectBytes caps how much of a command's output is buffered in
	// memory (and retained in the temp-file dump) before display truncation.
	// Guards against runaway output (`cat /dev/urandom | base64`, `yes`):
	// the newest 8 MB is kept, the rest is dropped at the source.
	BashMaxCollectBytes = 8 * 1024 * 1024
)

// formatBashOutput keeps the last BashMaxOutputLines / BashMaxOutputBytes of
// real output. Collection metadata is appended afterward so it does not alter
// the display budget or reported line range.
func formatBashOutput(output string, collectionTruncated bool) string {
	label := "Full output"
	if collectionTruncated {
		label = "Retained output"
	}
	display, _ := truncateBashTail(output, BashMaxOutputLines, BashMaxOutputBytes, label)
	if collectionTruncated {
		display += collectTruncationNote
	}
	return display
}

func truncateBashTail(output string, maxLines, maxBytes int, outputLabel string) (display, fullPath string) {
	if maxLines <= 0 {
		maxLines = BashMaxOutputLines
	}
	if maxBytes <= 0 {
		maxBytes = BashMaxOutputBytes
	}
	totalBytes := len(output)
	totalLines := strings.Count(output, "\n")
	bodyEnd := len(output)
	if bodyEnd > 0 && output[bodyEnd-1] == '\n' {
		// A final newline terminates the last line; exclude only the empty
		// element that strings.Split would otherwise produce after it.
		bodyEnd--
	} else if bodyEnd > 0 {
		totalLines++
	}

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return output, ""
	}

	path, err := writeBashTempFile(output)
	if err != nil {
		path = ""
	}

	// Locate the display tail without splitting every retained line. Dense
	// output can contain millions of newlines even within the collection cap.
	tailStart := 0
	if totalLines > maxLines {
		cursor := bodyEnd
		for range maxLines {
			newline := strings.LastIndexByte(output[:cursor], '\n')
			if newline < 0 {
				cursor = 0
				break
			}
			cursor = newline
		}
		tailStart = cursor + 1
	}

	if bodyEnd-tailStart > maxBytes {
		minStart := bodyEnd - maxBytes
		searchStart := minStart - 1
		if searchStart < tailStart {
			searchStart = tailStart
		}
		if newline := strings.IndexByte(output[searchStart:bodyEnd], '\n'); newline >= 0 {
			// Prefer a line boundary, even when that keeps fewer than maxBytes.
			tailStart = searchStart + newline + 1
		} else {
			// The final line alone exceeds maxBytes; keep its byte tail.
			tailStart = minStart
		}
	}

	display = output[tailStart:bodyEnd]
	displayLines := strings.Count(display, "\n") + 1
	startLine := totalLines - displayLines + 1
	endLine := totalLines
	if path != "" {
		display += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. %s: %s]", startLine, endLine, totalLines, outputLabel, path)
	} else {
		display += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. %s unavailable]", startLine, endLine, totalLines, outputLabel)
	}
	return display, path
}

func writeBashTempFile(content string) (string, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("phi-bash-%s.log", hex.EncodeToString(id)))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
