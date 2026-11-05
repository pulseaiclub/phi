package phi

import (
	"encoding/json"
	"errors"
	"fmt"

	ext "github.com/pulseaiclub/phi/ext/go"
)

// Select opens a host picker. Esc returns OK=false without an error.
func (extension *ExtensionAPI) Select(req ext.SelectRequest) (ext.SelectReply, error) {
	res, err := extension.hostCall("select", req)
	if err != nil || !res.OK {
		return ext.SelectReply{}, err
	}
	var index int
	if err := json.Unmarshal([]byte(res.Body), &index); err != nil {
		return ext.SelectReply{}, fmt.Errorf("phi: decode selection: %w", err)
	}
	if index < 0 || index >= len(req.Items) {
		return ext.SelectReply{}, fmt.Errorf("phi: selection index %d is out of range", index)
	}
	return ext.SelectReply{Index: index, OK: true}, nil
}

// SessionTurns returns every user message on the active branch, newest first.
func (extension *ExtensionAPI) SessionTurns() ([]ext.SessionTurn, error) {
	res, err := extension.hostCall("session_turns", nil)
	if err != nil {
		return nil, err
	}
	var turns []ext.SessionTurn
	if err := json.Unmarshal([]byte(res.Body), &turns); err != nil {
		return nil, fmt.Errorf("phi: decode session turns: %w", err)
	}
	return turns, nil
}

// SessionRewind validates or rewinds the active conversation and refills its prompt.
func (extension *ExtensionAPI) SessionRewind(req ext.RewindRequest) error {
	res, err := extension.hostCall("session_rewind", req)
	if err == nil && !res.OK {
		return errors.New("phi: host rejected session rewind")
	}
	return err
}
