package extension

import ext "github.com/pulseaiclub/phi/ext/go"

// BusUI publishes UI effects onto host callbacks.
type BusUI struct {
	NotifyFn    func(message, kind string)
	ConfirmFn   func(ext.ConfirmRequest) ext.ConfirmReply
	PickerFn    func(ext.PickerRequest) ext.PickerReply
	SetStatusFn func(key, text string)
}

func (u BusUI) Notify(message, kind string) {
	if u.NotifyFn != nil {
		u.NotifyFn(message, kind)
	}
}

func (u BusUI) Confirm(title, message string) bool {
	return u.ConfirmOpts(ext.ConfirmRequest{Title: title, Message: message}).OK
}

func (u BusUI) ConfirmOpts(req ext.ConfirmRequest) ext.ConfirmReply {
	if u.ConfirmFn != nil {
		return u.ConfirmFn(req)
	}
	return ext.ConfirmReply{}
}

// ShowPicker blocks until the host list overlay answers; without a PickerFn the
// request counts as dismissed.
func (u BusUI) ShowPicker(req ext.PickerRequest) ext.PickerReply {
	if u.PickerFn != nil {
		return u.PickerFn(req)
	}
	return ext.PickerReply{}
}

func (u BusUI) SetStatus(key, text string) {
	if u.SetStatusFn != nil {
		u.SetStatusFn(key, text)
	}
}

var _ ext.UI = BusUI{}
