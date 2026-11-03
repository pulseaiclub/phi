package controller

import (
	"github.com/pulseaiclub/phi/internal/job"
)

// This file owns the job→bus progress pipeline: one subscription to the job
// manager, one signature-based dedup, one published message per real change.

func (c *EngineController) startJobProgress() {
	ch, cancel := c.asm.Jobs().Subscribe()
	c.unsubJobs = cancel
	go func() {
		for p := range ch {
			if c.shouldPublishJobProgress(p) {
				c.publish(JobProgressMsg{Progress: p})
			}
		}
	}()
}

// shouldPublishJobProgress drops repeated signatures so a chatty sub-agent
// does not flood the bus with identical progress rows.
func (c *EngineController) shouldPublishJobProgress(p job.Progress) bool {
	key := p.JobID + "\x00" + p.ToolUseID
	if p.ToolUseID == "" {
		key = p.JobID + "\x00" + p.Name + "\x00" + p.Detail
	}
	sig := p.Status + "\x00" + p.Name + "\x00" + p.Detail
	if prev, ok := c.lastJobProgress.Load(key); ok && prev.(string) == sig {
		return false
	}
	c.lastJobProgress.Store(key, sig)
	return true
}
