package controller

import (
	"context"
	"time"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project"
)

// This file is the permission bridge: the gate (BypassGate over the config
// policy) plus the three overlay RPCs that round-trip asks through the bus.
// The UI goroutine answers; the agent goroutine waits here.

func (c *EngineController) initGate(policy permission.Policy) {
	if policy.AskTimeoutSec > 0 {
		c.askTimeoutSec = policy.AskTimeoutSec
	}
	if policy.Mode == "" {
		policy.Mode = permission.ModeInteractive
	}
	if policy.Mode == permission.ModeAllowAll {
		c.allowAll.Store(true)
	}
	// Do not clear allowAll when config does not pin a mode — the TUI defaults
	// to bypass, and the palette toggle must survive SetModel / re-init.
	var inner permission.Gate
	inner, err := permission.NewGate(policy, permission.WorkspaceRoot())
	if err != nil {
		inner, err = permission.NewGate(permission.DefaultPolicy(), permission.WorkspaceRoot())
	}
	if err != nil {
		inner = permission.AllowAll{}
	}
	c.gate = &permission.BypassGate{Inner: inner, Enabled: &c.allowAll}
}

func (c *EngineController) SetAllowAll(v bool) {
	c.allowAll.Store(v)
}

// AllowAll reports whether the permission gate is in bypass mode.
func (c *EngineController) AllowAll() bool { return c.allowAll.Load() }

func (c *EngineController) askTimeout() time.Duration {
	if c.askTimeoutSec > 0 {
		return time.Duration(c.askTimeoutSec) * time.Second
	}
	return time.Duration(defaultAskTimeoutSec) * time.Second
}

func waitReply[T any](ctx context.Context, ch <-chan T, timeout time.Duration, onAbort func()) (T, error) {
	var zero T
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		onAbort()
		return zero, ctx.Err()
	case <-timer.C:
		onAbort()
		return zero, nil
	}
}

func (c *EngineController) askPermission(
	ctx context.Context,
	req permission.Request,
	reason string,
) (permission.AskResult, error) {
	if c.allowAll.Load() {
		return permission.AskResult{Approved: true}, nil
	}
	reply := make(chan AskReply, 1)
	c.publish(OverlayMsg{Kind: OverlayPermissionAsk, Request: req, Reason: reason, PermReply: reply})
	r, err := waitReply(ctx, reply, c.askTimeout(), func() {
		c.publish(OverlayMsg{Kind: OverlayPermissionDismiss})
	})
	if err != nil {
		return permission.AskResult{}, err
	}
	if r.AllowSession || r.AllowPersistent {
		c.allowAll.Store(true)
	}
	if r.AllowPersistent {
		_ = project.SetPermissionMode(c.proj.Global(), permission.ModeAllowAll)
	}
	return permission.AskResult{Approved: r.Approved, Feedback: r.Feedback}, nil
}

func (c *EngineController) askContinue(ctx context.Context, maxRounds int) (bool, error) {
	reply := make(chan ContinueReply, 1)
	c.publish(OverlayMsg{Kind: OverlayContinueAsk, MaxRounds: maxRounds, ContReply: reply})
	r, err := waitReply(ctx, reply, c.askTimeout(), func() {
		c.publish(OverlayMsg{Kind: OverlayContinueDismiss})
	})
	if err != nil {
		return false, err
	}
	return r.Continue, nil
}

func (c *EngineController) askExtConfirm(req ext.ConfirmRequest) ext.ConfirmReply {
	reply := make(chan ExtConfirmReply, 1)
	c.publish(OverlayMsg{
		Kind:         OverlayExtConfirm,
		Title:        req.Title,
		Message:      req.Message,
		Yes:          req.Yes,
		No:           req.No,
		Danger:       req.Danger,
		ConfirmReply: reply,
	})
	r, _ := waitReply(context.Background(), reply, c.askTimeout(), func() {
		c.publish(OverlayMsg{Kind: OverlayExtConfirmDismiss})
	})
	return ext.ConfirmReply{OK: r.OK}
}
