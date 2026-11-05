package editor

import (
	"encoding/json"
	"errors"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func (e *Editor) applyExtensionRequest(msg controller.ExtHostRequestMsg) {
	if msg.Dismiss {
		e.composer.ApplyExtensionSelect(ext.SelectRequest{}, msg)
		return
	}
	if msg.Context.Err() != nil {
		return
	}
	reply := func(body any, err error) {
		if err != nil {
			msg.Reply <- pxb.HostResult{Error: err.Error()}
			return
		}
		data, err := json.Marshal(body)
		if err != nil {
			msg.Reply <- pxb.HostResult{Error: err.Error()}
			return
		}
		msg.Reply <- pxb.HostResult{OK: true, Body: string(data)}
	}
	switch msg.Request.Method {
	case "select":
		var req ext.SelectRequest
		if err := json.Unmarshal([]byte(msg.Request.Arg), &req); err != nil {
			reply(nil, err)
			return
		}
		e.composer.ApplyExtensionSelect(req, msg)
	case "session_turns":
		reply(e.ctrl.SessionTurns(), nil)
	case "session_rewind":
		var req ext.RewindRequest
		if err := json.Unmarshal([]byte(msg.Request.Arg), &req); err != nil {
			reply(nil, err)
			return
		}
		if e.submitter.IsBusy() || e.overlays.Active() {
			reply(nil, errors.New("cannot rewind while a reply, shell command, or dialog is active"))
			return
		}
		text, err := e.ctrl.RewindSession(req)
		if err == nil && !req.ValidateOnly {
			snap := e.ctrl.ReplaySnapshot()
			e.transcript.LoadReplay(snap)
			e.transcript.ResetSubagents()
			e.transcript.Sync()
			e.transcript.StickToBottom()
			e.footer.UpdateTokenDisplay(snap.LastUsage())
			e.composer.ClearPendingImages()
			e.composer.ClearPendingSkills()
			e.composer.ClearPendingRefs()
			e.composer.SetInput(text)
		}
		reply(nil, err)
	default:
		reply(nil, errors.New("unknown host request: "+msg.Request.Method))
	}
}
