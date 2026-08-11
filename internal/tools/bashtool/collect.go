package bashtool

import (
	"bytes"
	"fmt"
	"sync"
)

// cappedBuffer collects command output into a bounded rolling tail.
//
// Once the cap is reached, the oldest bytes are dropped so memory stays
// bounded while the newest output is retained — the same "keep the tail"
// philosophy as the bash display limits. The truncation is
// reported via Truncated so callers can tell the user the output was cut
// at the source, not just at display time.
//
// Write is safe for concurrent use even though the current os/exec callers
// assign the same comparable writer to stdout and stderr, which os/exec
// coalesces onto one copy path.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

// BashOutputTail keeps a small, display-sized tail for live UI updates.
// Unlike cappedBuffer, it also limits the number of lines so rendering the
// live view cannot allocate a surface proportional to a runaway log.
type BashOutputTail struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	maxBytes  int
	maxLines  int
	truncated bool
}

// NewBashOutputTail creates a bounded tail using the bash display limits.
func NewBashOutputTail(maxLines, maxBytes int) *BashOutputTail {
	if maxLines <= 0 {
		maxLines = BashMaxOutputLines
	}
	if maxBytes <= 0 {
		maxBytes = BashMaxOutputBytes
	}
	return &BashOutputTail{maxLines: maxLines, maxBytes: maxBytes}
}

// Write implements io.Writer while retaining only the newest display-sized
// tail. It is safe for concurrent use.
func (t *BashOutputTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	t.trimLocked()
	return len(p), nil
}

// WriteString appends s to the bounded tail without an intermediate byte
// slice.
func (t *BashOutputTail) WriteString(s string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.WriteString(s)
	t.trimLocked()
	return len(s), nil
}

// Snapshot returns the current display tail and whether older output was
// discarded.
func (t *BashOutputTail) Snapshot() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String(), t.truncated
}

func (t *BashOutputTail) trimLocked() {
	if t.maxBytes > 0 && t.buf.Len() > t.maxBytes {
		t.buf.Next(t.buf.Len() - t.maxBytes)
		t.truncated = true
	}
	data := t.buf.Bytes()
	if len(data) == 0 || t.maxLines <= 0 {
		return
	}

	// A trailing newline terminates the last line; it should not create an
	// extra empty line in the same way truncateBashTail treats it.
	lineCount := 1
	if data[len(data)-1] == '\n' {
		lineCount = 0
	}
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] != '\n' {
			continue
		}
		lineCount++
		if lineCount > t.maxLines {
			t.buf.Next(i + 1)
			t.truncated = true
			return
		}
	}
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

// Write implements io.Writer. It never reports a short write even when bytes
// are dropped, so os/exec's copy machinery treats it as a plain sink.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Write(p)
	if c.buf.Len() > c.limit {
		// Drop the oldest bytes to keep a bounded rolling tail.
		c.buf.Next(c.buf.Len() - c.limit)
		c.truncated = true
	}
	return len(p), nil
}

// String returns the retained output (never more than limit bytes).
func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Truncated reports whether the cap was hit and bytes were dropped.
func (c *cappedBuffer) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.truncated
}

const collectTruncationMarker = "[output truncated:"

// collectTruncationNote is appended after display truncation so it does not
// consume the real output's line or byte budget.
var collectTruncationNote = fmt.Sprintf(
	"\n\n%s only the last %d MB was kept]",
	collectTruncationMarker,
	BashMaxCollectBytes/(1024*1024))
