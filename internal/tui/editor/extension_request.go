package editor

import (
	"encoding/json"
	"strconv"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func (e *Editor) applyExtensionRequest(msg controller.ExtHostRequestMsg) {
	if msg.Dismiss {
		if e.extSelectReply == msg.Reply {
			e.composer.HideList()
		}
		return
	}
	if msg.Context.Err() != nil {
		return
	}
	reply := msg.Reply
	fail := func(err string) { reply <- pxb.HostResult{Error: err} }
	if msg.Request.Method != "select" {
		fail("unsupported host request: " + msg.Request.Method)
		return
	}
	var req ext.SelectRequest
	if err := json.Unmarshal([]byte(msg.Request.Arg), &req); err != nil {
		fail("decode selection request: " + err.Error())
		return
	}
	if len(req.Items) == 0 {
		fail("selection requires at least one item")
		return
	}
	if e.overlays.Active() {
		fail("cannot select while another dialog is active")
		return
	}
	if e.code.Active() || e.diff.Active() {
		fail("cannot select while a code or diff viewer is open; close the viewer and try again")
		return
	}

	chosen := -1
	e.composer.ShowExtensionSelect(req,
		func(index int) { chosen = index },
		func() {
			// A late timeout must not answer for a newer picker.
			if e.extSelectReply != reply {
				return
			}
			e.extSelectReply = nil
			if chosen >= 0 {
				reply <- pxb.HostResult{Body: strconv.Itoa(chosen), OK: true}
				return
			}
			reply <- pxb.HostResult{}
		},
	)
	e.extSelectReply = reply
}
