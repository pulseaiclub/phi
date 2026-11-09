package controller

import (
	"context"

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
	c.publish(ExtHostRequestMsg{Context: ctx, Request: req, Reply: reply})
	select {
	case result := <-reply:
		return result
	case <-ctx.Done():
		c.publish(ExtHostRequestMsg{Context: ctx, Request: req, Reply: reply, Dismiss: true})
		return pxb.HostResult{Error: ctx.Err().Error()}
	}
}
