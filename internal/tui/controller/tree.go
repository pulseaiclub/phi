package controller

import (
	"context"
	"errors"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
)

type TreeResultMsg struct {
	Plan     session.TreeNavigation
	Snapshot session.Snapshot
	Tree     session.TreeSnapshot
	Err      error
}

func (TreeResultMsg) isMsg() {}

type TreeOpenMsg struct{}

func (TreeOpenMsg) isMsg() {}

func (c *EngineController) TreeSettings() project.TreeConfig {
	if c == nil || c.proj == nil {
		return project.TreeConfig{}
	}
	return c.proj.Config().Tree
}

func (c *EngineController) TreeBusy() bool { return c != nil && c.treeBusy.Load() }

func (c *EngineController) Running() bool {
	if c == nil {
		return false
	}
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	return c.streamDone != nil
}

func (c *EngineController) TreeSnapshot() session.TreeSnapshot {
	if c == nil || c.engine == nil {
		return session.TreeSnapshot{}
	}
	return c.engine.Session().TreeSnapshot()
}

func (c *EngineController) SetTreeLabel(id, label string) error {
	if c == nil || c.engine == nil {
		return errors.New("agent not configured")
	}
	return c.engine.Session().SetLabel(id, label)
}

func (c *EngineController) RecordHistory(h session.HistoryEntry) error {
	if c == nil || c.engine == nil {
		return nil
	}
	return c.engine.Session().AppendHistory(h)
}

// StartTreeNavigation reserves the engine before waiting for the old stream and local shell.
// The reservation remains until the UI has applied the replay, so extension submits cannot race it.
func (c *EngineController) StartTreeNavigation(
	selected string,
	opts agent.TreeOptions,
	localDone <-chan struct{},
) bool {
	if c == nil || c.engine == nil {
		return false
	}
	c.streamMu.Lock()
	if c.treeBusy.Load() {
		c.streamMu.Unlock()
		return false
	}
	c.treeBusy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	c.treeCancel = cancel
	done := c.streamDone
	if c.streamCancel != nil {
		c.streamCancel()
	}
	c.streamMu.Unlock()
	go func() {
		defer cancel()
		// Even a cancelled navigation must wait for writers before taking a replay snapshot.
		if localDone != nil {
			<-localDone
		}
		if done != nil {
			<-done
		}
		c.streamMu.Lock()
		c.streamGen++
		c.streamMu.Unlock()
		var plan session.TreeNavigation
		err := ctx.Err()
		if err == nil {
			plan, err = c.engine.NavigateTree(ctx, selected, opts)
		}
		c.publish(TreeResultMsg{Plan: plan, Snapshot: c.ReplaySnapshot(), Tree: c.TreeSnapshot(), Err: err})
	}()
	return true
}

func (c *EngineController) CancelTreeNavigation() {
	if c == nil {
		return
	}
	c.streamMu.Lock()
	cancel := c.treeCancel
	c.streamMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *EngineController) FinishTreeNavigation() {
	c.streamMu.Lock()
	c.treeCancel = nil
	c.treeBusy.Store(false)
	c.streamMu.Unlock()
}
