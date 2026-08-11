package tui

import (
	"testing"
	"time"
)

func TestBashLiveOutputPublishesTrailingUpdate(t *testing.T) {
	updates := make(chan string, 2)
	live := newBashLiveOutput(50*time.Millisecond, func(output string) {
		updates <- output
	})
	t.Cleanup(live.Close)

	live.Append("first")
	if got := <-updates; got != "first" {
		t.Fatalf("first update=%q", got)
	}
	live.Append("-second")

	select {
	case got := <-updates:
		if got != "first-second" {
			t.Fatalf("trailing update=%q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for trailing update")
	}
}

func TestBashLiveOutputCloseCancelsTrailingUpdate(t *testing.T) {
	updates := make(chan string, 2)
	live := newBashLiveOutput(time.Hour, func(output string) {
		updates <- output
	})

	live.Append("first")
	<-updates
	live.Append("-second")
	live.Close()

	live.mu.Lock()
	defer live.mu.Unlock()
	if !live.stopped || live.timer != nil {
		t.Fatalf("closed live output: stopped=%v timer=%v", live.stopped, live.timer)
	}
}
