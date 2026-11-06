package controller

import (
	"context"
	"errors"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

// ExtHostRequestMsg keeps extension requests on the UI goroutine, which owns
// the session and picker. Dismiss only closes the matching timed-out picker.
type ExtHostRequestMsg struct {
	Context context.Context
	Request pxb.HostRequest
	Reply   chan pxb.HostResult
	Dismiss bool
}

func (ExtHostRequestMsg) isMsg() {}

func (c *EngineController) extensionHostRequest(req pxb.HostRequest) pxb.HostResult {
	ctx, cancel := context.WithTimeout(context.Background(), c.askTimeout())
	defer cancel()
	reply := make(chan pxb.HostResult, 1)
	msg := ExtHostRequestMsg{Context: ctx, Request: req, Reply: reply}
	c.publish(msg)
	select {
	case r := <-reply:
		return r
	case <-ctx.Done():
		msg.Dismiss = true
		c.publish(msg)
		return pxb.HostResult{Error: ctx.Err().Error()}
	}
}

func (c *EngineController) SessionTurns() []ext.SessionTurn {
	if c.engine == nil || c.engine.Session() == nil {
		return nil
	}
	var turns []ext.SessionTurn
	for _, msg := range c.engine.Session().UserTurns() {
		turns = append(turns, ext.SessionTurn{
			EntryID: msg.ID, Text: msg.UserInput(), Timestamp: msg.Timestamp, ImageCount: len(msg.Message.Images),
		})
	}
	return turns
}

func (c *EngineController) RewindSession(req ext.RewindRequest) (string, error) {
	if c.engine == nil || req.SessionID != c.SessionID() {
		return "", errors.New("session changed; reopen the turn picker")
	}
	if err := c.refuseWhileJobsLive("rewind"); err != nil {
		return "", err
	}
	if req.ValidateOnly {
		return "", c.engine.Session().CanRewindTo(req.EntryID)
	}
	text, err := c.engine.Session().RewindTo(req.EntryID)
	if err != nil {
		return "", err
	}
	c.streamMu.Lock()
	c.retryReady = false
	c.lastPrompt = LastPrompt{}
	c.promptAppended = false
	c.streamGen++
	c.streamMu.Unlock()
	return text, nil
}
