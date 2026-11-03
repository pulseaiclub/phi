package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

// This file runs the agent loop off the UI goroutine. Every event hops onto
// the bus; generation numbers decide which loop is still allowed to speak
// after the user cancels or submits again.

// StartPrompt launches one agent run, superseding any in-flight stream.
func (c *EngineController) StartPrompt(text string, pendingSkills []string, images []llm.Image) {
	ctx, cancel := context.WithCancel(context.Background())
	c.streamMu.Lock()
	if c.streamCancel != nil {
		c.streamCancel()
	}
	c.streamCancel = cancel
	c.streamGen++
	gen := c.streamGen
	c.streamMu.Unlock()

	go c.runLoop(ctx, gen, text, pendingSkills, images)
}

// Cancel aborts the in-flight stream, if any.
func (c *EngineController) Cancel() {
	c.streamMu.Lock()
	cancel := c.streamCancel
	c.streamMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *EngineController) alive(gen int) bool {
	c.streamMu.Lock()
	ok := c.streamGen == gen
	c.streamMu.Unlock()
	return ok
}

// waitOrDone sleeps d, but wakes early on cancellation. It reports whether
// the generation still owns the stream.
func (c *EngineController) waitOrDone(ctx context.Context, gen int, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	return c.alive(gen)
}

func (c *EngineController) publishLoopError(gen int, errText string) {
	if !c.alive(gen) {
		return
	}
	c.publish(SessionEventMsg{Event: session.AssistantMessageUpdate{Message: session.Message{
		ID:    fmt.Sprintf("assistant-error-%d", time.Now().UnixNano()),
		State: session.StateError,
		Text:  errText,
		Content: []session.ContentBlock{
			{Type: session.BlockText, Text: errText},
		},
	}}})
}

func (c *EngineController) runLoop(
	ctx context.Context,
	gen int,
	prompt string,
	pendingSkills []string,
	images []llm.Image,
) {
	// The beat lets a rapid double-submit collapse into the newer run before
	// this one publishes anything.
	if !c.waitOrDone(ctx, gen, 120*time.Millisecond) {
		return
	}
	c.publish(FooterMsg{Kind: FooterSetActivity, Activity: ActivityStreaming})

	if c.engine == nil {
		c.publishLoopError(gen, "agent not configured")
		return
	}

	for ev, err := range c.engine.Loop(ctx, prompt, agent.LoopOpts{
		PendingSkills: pendingSkills,
		Images:        images,
	}) {
		if !c.alive(gen) {
			return
		}
		if err != nil {
			c.publishLoopError(gen, err.Error())
			return
		}
		if ev != nil {
			c.publish(SessionEventMsg{Event: ev})
		}
	}
}
