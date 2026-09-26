package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/debuglog"
)

// BuildAPI installs shim handlers onto api from this process's registrations.
func (p *Proc) BuildAPI(api *ext.API) {
	if p == nil || api == nil {
		return
	}
	for _, t := range p.tools {
		name := t.Name
		var params map[string]any
		if len(t.SchemaJSON) > 0 {
			_ = json.Unmarshal(t.SchemaJSON, &params)
		}
		var detailFn func(json.RawMessage) string
		if t.HasDetail {
			detailFn = func(args json.RawMessage) string {
				d, err := p.CallToolDetail(context.Background(), name, args)
				if err != nil {
					return ""
				}
				return d
			}
		}
		api.RegisterTool(ext.Tool{
			Name:           name,
			Description:    t.Description,
			Parameters:     params,
			DetailFromArgs: detailFn,
			Readable:       t.Readable,
			Execute: func(ctx context.Context, args json.RawMessage) (ext.ToolResult, error) {
				return p.CallTool(ctx, name, args)
			},
		})
	}
	for _, c := range p.cmds {
		name := c.Name
		desc := c.Description
		needsArgs := c.NeedsArgs
		api.RegisterCommand(name, ext.Command{
			Description: desc,
			NeedsArgs:   needsArgs,
			Handler: func(args string, _ *ext.Context) error {
				resp, err := p.CallCommand(context.Background(), name, args)
				if err != nil {
					return err
				}
				if !resp.OK {
					if resp.Error != "" {
						return errors.New(resp.Error)
					}
					return fmt.Errorf("extension command %q failed", name)
				}
				return nil
			},
		})
	}
	if p.WantsIntercept(pxb.EvToolCall) {
		api.On(ext.EventToolCall, func(ev ext.ToolCallEvent, _ *ext.Context) *ext.ToolCallResult {
			resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
				Event: pxb.EvToolCall, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID, Input: ev.Input,
			})
			if err != nil {
				debuglog.Logf("extension %q: tool_call intercept: %v", p.Manifest.Name, err)
				return nil
			}
			return &ext.ToolCallResult{Block: resp.Block, Reason: resp.Reason, Input: resp.Input, Context: resp.Context}
		})
	}
	if p.WantsIntercept(pxb.EvToolResult) {
		api.On(ext.EventToolResult, func(ev ext.ToolResultEvent, _ *ext.Context) *ext.ToolResultResult {
			resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
				Event: pxb.EvToolResult, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID,
				Input: ev.Input, Content: ev.Content, IsError: ev.IsError, ErrText: ev.Err,
			})
			if err != nil {
				debuglog.Logf("extension %q: tool_result intercept: %v", p.Manifest.Name, err)
				return nil
			}
			return &ext.ToolResultResult{
				Content: resp.Content,
				Context: resp.Context,
				Stop:    resp.Stop,
				Reason:  resp.Reason,
			}
		})
	}
	if p.WantsIntercept(pxb.EvSessionBeforeSwitch) {
		api.On(
			ext.EventSessionBeforeSwitch,
			func(ev ext.SessionBeforeSwitchEvent, _ *ext.Context) *ext.SessionBeforeSwitchResult {
				resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
					Event: pxb.EvSessionBeforeSwitch, Reason: ev.Reason, TargetID: ev.TargetSessionID,
				})
				if err != nil {
					return nil
				}
				return &ext.SessionBeforeSwitchResult{Cancel: resp.Cancel, Reason: resp.Reason, Toast: resp.Toast}
			},
		)
	}
	if p.WantsIntercept(pxb.EvBeforeAgentStart) {
		api.On(
			ext.EventBeforeAgentStart,
			func(ev ext.BeforeAgentStartEvent, _ *ext.Context) *ext.BeforeAgentStartResult {
				resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
					Event: pxb.EvBeforeAgentStart, Prompt: ev.Prompt,
				})
				if err != nil {
					return nil
				}
				return &ext.BeforeAgentStartResult{
					Prompt:             resp.Prompt,
					SystemPromptAppend: resp.SystemPromptAppend,
				}
			},
		)
	}
	if p.WantsIntercept(pxb.EvUserInput) {
		api.On(ext.EventUserInput, func(ev ext.UserInputEvent, _ *ext.Context) *ext.UserInputResult {
			resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
				Event: pxb.EvUserInput, Prompt: ev.Text,
			})
			if err != nil {
				return nil
			}
			return &ext.UserInputResult{Handled: resp.Handled, Text: resp.Prompt, Reason: resp.Reason}
		})
	}
	if p.WantsIntercept(pxb.EvTurnStopping) {
		api.On(ext.EventTurnStopping, func(ev ext.TurnStoppingEvent, _ *ext.Context) *ext.TurnStoppingResult {
			resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
				Event: pxb.EvTurnStopping, TurnIndex: uint32(ev.TurnIndex), //nolint:gosec // G115
			})
			if err != nil {
				return nil
			}
			return &ext.TurnStoppingResult{Continue: resp.Continue, Message: resp.Prompt, Reason: resp.Reason}
		})
	}
	// Fire-and-forget event shims.
	if _, ok := p.events[pxb.EvSessionStart]; ok {
		api.On(ext.EventSessionStart, func(ev ext.SessionStartEvent, _ *ext.Context) {
			p.Emit(pxb.EventNotify{
				Event:             pxb.EvSessionStart,
				Reason:            ev.Reason,
				PreviousSessionID: ev.PreviousSessionID,
			})
		})
	}
	if _, ok := p.events[pxb.EvSessionShutdown]; ok {
		api.On(ext.EventSessionShutdown, func(ev ext.SessionShutdownEvent, _ *ext.Context) {
			p.Emit(pxb.EventNotify{
				Event:           pxb.EvSessionShutdown,
				Reason:          ev.Reason,
				TargetSessionID: ev.TargetSessionID,
			})
		})
	}
	if _, ok := p.events[pxb.EvSessionCompact]; ok {
		api.On(ext.EventSessionCompact, func(ev ext.SessionCompactEvent, _ *ext.Context) {
			p.Emit(pxb.EventNotify{Event: pxb.EvSessionCompact, Reason: ev.Reason})
		})
	}
	if _, ok := p.events[pxb.EvAgentStart]; ok {
		api.On(ext.EventAgentStart, func(ext.AgentStartEvent, *ext.Context) {
			p.Emit(pxb.EventNotify{Event: pxb.EvAgentStart})
		})
	}
	if _, ok := p.events[pxb.EvAgentEnd]; ok {
		api.On(ext.EventAgentEnd, func(ext.AgentEndEvent, *ext.Context) {
			p.Emit(pxb.EventNotify{Event: pxb.EvAgentEnd})
		})
	}
	if _, ok := p.events[pxb.EvTurnStart]; ok {
		api.On(ext.EventTurnStart, func(ev ext.TurnStartEvent, _ *ext.Context) {
			//nolint:gosec // G115: turn index is a small session counter
			p.Emit(pxb.EventNotify{Event: pxb.EvTurnStart, TurnIndex: uint32(ev.TurnIndex)})
		})
	}
	if _, ok := p.events[pxb.EvTurnEnd]; ok {
		api.On(ext.EventTurnEnd, func(ev ext.TurnEndEvent, _ *ext.Context) {
			//nolint:gosec // G115: turn index is a small session counter
			p.Emit(pxb.EventNotify{Event: pxb.EvTurnEnd, TurnIndex: uint32(ev.TurnIndex)})
		})
	}
	if _, ok := p.events[pxb.EvToolExecStart]; ok {
		api.On(ext.EventToolExecutionStart, func(ev ext.ToolExecutionStartEvent, _ *ext.Context) {
			p.Emit(
				pxb.EventNotify{
					Event:      pxb.EvToolExecStart,
					ToolName:   ev.ToolName,
					ToolCallID: ev.ToolCallID,
					Input:      ev.Args,
				},
			)
		})
	}
	if _, ok := p.events[pxb.EvToolExecEnd]; ok {
		api.On(ext.EventToolExecutionEnd, func(ev ext.ToolExecutionEndEvent, _ *ext.Context) {
			p.Emit(
				pxb.EventNotify{
					Event:      pxb.EvToolExecEnd,
					ToolName:   ev.ToolName,
					ToolCallID: ev.ToolCallID,
					IsError:    ev.IsError,
				},
			)
		})
	}
}
