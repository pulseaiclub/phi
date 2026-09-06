package controller

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/project"
)

type ThinkingExpandedMsg struct{ Expanded bool }

func (ThinkingExpandedMsg) isMsg() {}

func (c *EngineController) ThinkingExpanded() bool {
	return c != nil && c.proj != nil && c.proj.Config() != nil && c.proj.Config().TUI.ThinkingExpanded
}

func (c *EngineController) SetThinkingExpanded(expanded bool) error {
	if c == nil || c.proj == nil {
		return errors.New("cannot save Thinking preference: project not available")
	}
	if err := project.SetThinkingExpanded(c.proj.Global(), expanded); err != nil {
		return err
	}
	if cfg := c.proj.Config(); cfg != nil {
		cfg.TUI.ThinkingExpanded = expanded
	}
	return nil
}
