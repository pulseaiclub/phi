package phi

import (
	"encoding/json"
	"errors"
	"fmt"

	ext "github.com/pulseaiclub/phi/ext/go"
)

// Select opens a host picker. Esc returns OK=false without an error.
// Call from a command, tool, or intercept handler, like Confirm.
func (extension *ExtensionAPI) Select(req ext.SelectRequest) (ext.SelectReply, error) {
	res, err := extension.hostCall("select", req)
	if err != nil || !res.OK {
		return ext.SelectReply{}, err
	}
	var index *int
	if err := json.Unmarshal([]byte(res.Body), &index); err != nil {
		return ext.SelectReply{}, fmt.Errorf("phi: decode selection: %w", err)
	}
	if index == nil {
		return ext.SelectReply{}, errors.New("phi: missing selection index")
	}
	if *index < 0 || *index >= len(req.Items) {
		return ext.SelectReply{}, fmt.Errorf("phi: selection index %d is out of range", *index)
	}
	return ext.SelectReply{Index: *index, OK: true}, nil
}
