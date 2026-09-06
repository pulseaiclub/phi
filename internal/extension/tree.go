package extension

import (
	"context"
	"fmt"
	"slices"
	"time"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

func (r *Runner) EmitSessionBeforeTree(
	ctx context.Context,
	ev ext.SessionBeforeTreeEvent,
) (ext.SessionBeforeTreeResult, error) {
	var out ext.SessionBeforeTreeResult
	if r == nil {
		return out, nil
	}
	r.mu.Lock()
	procs := slices.Clone(r.procs)
	r.mu.Unlock()
	for _, p := range procs {
		payload := pxb.EncodeTreeNavigation(
			pxb.TreeNavigation{
				FromID:       ev.FromID,
				TargetID:     ev.TargetID,
				SelectedID:   ev.SelectedID,
				Summarize:    ev.Summarize,
				Instructions: ev.Instructions,
			},
		)
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, err := p.Intercept(callCtx, pxb.InterceptReq{Event: pxb.EvSessionBeforeTree, Input: payload})
		cancel()
		if err != nil {
			return out, fmt.Errorf("extension %s before tree: %w", p.Manifest.Name, err)
		}
		if res.Cancel {
			return ext.SessionBeforeTreeResult{Cancel: true, Reason: res.Reason}, nil
		}
		if res.Content != "" {
			out.Summary = res.Content
		}
		if res.Prompt != "" {
			out.Instructions = res.Prompt
			ev.Instructions = res.Prompt
		}
	}
	return out, ctx.Err()
}

func (r *Runner) EmitSessionTree(ev ext.SessionTreeEvent) {
	if r == nil {
		return
	}
	r.mu.Lock()
	procs := slices.Clone(r.procs)
	r.mu.Unlock()
	payload := pxb.EncodeTreeNavigation(
		pxb.TreeNavigation{FromID: ev.FromID, TargetID: ev.TargetID, Summary: ev.Summary, SummaryID: ev.SummaryID},
	)
	for _, p := range procs {
		p.Emit(pxb.EventNotify{Event: pxb.EvSessionTree, Input: payload})
	}
}
