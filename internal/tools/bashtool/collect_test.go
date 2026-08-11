package bashtool

import (
	"strings"
	"sync"
	"testing"
)

func TestCappedBufferKeepsNewestTail(t *testing.T) {
	cb := newCappedBuffer(32)
	full := strings.Repeat("0123456789", 100)
	for range 100 {
		if _, err := cb.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	got := cb.String()
	if want := full[len(full)-32:]; got != want {
		t.Fatalf("want the newest 32 bytes %q, got %q", want, got)
	}
	if !cb.Truncated() {
		t.Fatal("want truncated")
	}
}

func TestBashOutputTailKeepsNewestLinesAndBytes(t *testing.T) {
	tail := NewBashOutputTail(3, 64)
	if _, err := tail.WriteString("one\ntwo\nthree\nfour\n"); err != nil {
		t.Fatal(err)
	}
	got, truncated := tail.Snapshot()
	if want := "two\nthree\nfour\n"; got != want || !truncated {
		t.Fatalf("got %q truncated=%v, want %q and truncation", got, truncated, want)
	}

	tail = NewBashOutputTail(100, 10)
	if _, err := tail.WriteString("0123456789ABC"); err != nil {
		t.Fatal(err)
	}
	got, truncated = tail.Snapshot()
	if got != "3456789ABC" || !truncated {
		t.Fatalf("got %q truncated=%v, want newest 10 bytes", got, truncated)
	}
}

func TestCappedBufferNoTruncationUnderLimit(t *testing.T) {
	cb := newCappedBuffer(64)
	data := "hello world"
	if _, err := cb.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if cb.String() != data || cb.Truncated() {
		t.Fatalf("got %q truncated=%v", cb.String(), cb.Truncated())
	}
}

func TestCappedBufferExactLimit(t *testing.T) {
	cb := newCappedBuffer(10)
	data := "0123456789"
	if _, err := cb.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if cb.String() != data || cb.Truncated() {
		t.Fatalf("got %q truncated=%v", cb.String(), cb.Truncated())
	}
}

func TestCappedBufferConcurrentWrites(t *testing.T) {
	// Preserve the collector's defensive concurrency contract even though the
	// current os/exec setup coalesces stdout and stderr onto one writer path.
	cb := newCappedBuffer(1024)
	var wg sync.WaitGroup
	for _, g := range []string{"a", "b"} {
		wg.Add(1)
		go func(g string) {
			defer wg.Done()
			for range 5000 {
				_, _ = cb.Write([]byte(g))
			}
		}(g)
	}
	wg.Wait()
	if len(cb.String()) != 1024 || !cb.Truncated() {
		t.Fatalf("len=%d truncated=%v", len(cb.String()), cb.Truncated())
	}
}
