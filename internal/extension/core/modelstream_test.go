package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/extension/loader"
)

func testProc() *Proc {
	return &Proc{
		Manifest: loader.Manifest{Name: "x"},
		streams:  make(map[uint32]*modelStream),
	}
}

// collectStream reads frames until an error frame or a short deadline.
func collectStream(s *modelStream) []streamFrame {
	var out []streamFrame
	deadline := time.After(time.Second)
	for {
		select {
		case fr := <-s.ch:
			out = append(out, fr)
			if fr.err != nil || fr.ev.Type == "done" || fr.ev.Type == "error" {
				return out
			}
		case <-deadline:
			return out
		}
	}
}

// A malformed ModelStreamEvent must end the stream with an error frame and
// leave no registration behind: late frames for the same id are dropped
// quietly instead of panicking the read loop.
func TestDeliverStreamCorruptFrameEndsStream(t *testing.T) {
	p := testProc()
	s := newModelStream()
	p.streams[1] = s

	p.deliverStream(pxb.Frame{
		Header: pxb.Header{Flags: pxb.FlagHasID, ID: 1},
		Body:   []byte{0xff, 0x00, 0x00},
	})
	frames := collectStream(s)
	require.Len(t, frames, 1)
	require.Error(t, frames[0].err)
	assert.Contains(t, frames[0].err.Error(), "model stream event")

	_, stillRegistered := p.streams[1]
	assert.False(t, stillRegistered, "failed stream must be deregistered")

	valid := pxb.EncodeModelStreamEvent(pxb.ModelStreamEvent{Type: "delta", Content: "late"})
	assert.NotPanics(t, func() {
		p.deliverStream(pxb.Frame{Header: pxb.Header{Flags: pxb.FlagHasID, ID: 1}, Body: valid})
	})
}

// Abandoning a stream whose buffer is full must release the blocked sender
// via done instead of closing a channel under it.
func TestModelStreamAbandonWithFullBuffer(t *testing.T) {
	s := newModelStream()
	for range streamBuffer {
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta", Content: "x"}})
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		close(started)
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta", Content: "overflow"}})
		close(finished)
	}()
	<-started // the push is parked on the full buffer (or about to park)

	done := make(chan struct{})
	go func() {
		s.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close blocked behind a full buffer")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("parked push never released after close")
	}

	// Post-close pushes are dropped, never panic.
	assert.NotPanics(t, func() {
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta", Content: "late"}})
		s.close()
	})
}

// done and the terminal frame travel on separate channels, so a consumer that
// sees done first must still find the terminal frame in the buffer.
func TestModelStreamDoneFrameSurvivesClose(t *testing.T) {
	s := newModelStream()
	s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "done"}})
	s.close()

	frames := collectStream(s)
	require.Len(t, frames, 1)
	assert.Equal(t, "done", frames[0].ev.Type)
}

// failStreams must not deadlock when a consumer abandoned the stream and the
// buffer is full: detach under the lock, then close without pushing.
func TestFailStreamsDetachesBeforeClose(t *testing.T) {
	p := testProc()
	s := newModelStream()
	p.streams[7] = s
	for range streamBuffer {
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta"}})
	}

	done := make(chan struct{})
	go func() {
		p.failStreams()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failStreams deadlocked on a full buffer")
	}
	assert.Empty(t, p.streams)
}
