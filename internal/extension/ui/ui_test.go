package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	ext "github.com/pulseaiclub/phi/ext/go"
)

func TestBusUINilCallbacksDegrade(t *testing.T) {
	var u BusUI

	assert.NotPanics(t, func() { u.Notify("hello", "info") })
	assert.NotPanics(t, func() { u.SetStatus("build", "running") })
	assert.False(t, u.Confirm("Continue?", "Proceed with the run"))
	assert.Equal(t, ext.ConfirmReply{}, u.ConfirmOpts(ext.ConfirmRequest{Title: "t", Message: "m"}))
}

func TestBusUIForwardsNotify(t *testing.T) {
	var gotMessage, gotKind string
	u := BusUI{NotifyFn: func(message, kind string) {
		gotMessage, gotKind = message, kind
	}}

	u.Notify("extension loaded", "warning")

	assert.Equal(t, "extension loaded", gotMessage)
	assert.Equal(t, "warning", gotKind)
}

func TestBusUIForwardsConfirm(t *testing.T) {
	var got ext.ConfirmRequest
	u := BusUI{ConfirmFn: func(req ext.ConfirmRequest) ext.ConfirmReply {
		got = req
		return ext.ConfirmReply{OK: true}
	}}

	assert.True(t, u.Confirm("Delete file?", "This cannot be undone"))
	assert.Equal(t, ext.ConfirmRequest{Title: "Delete file?", Message: "This cannot be undone"}, got)
}

func TestBusUIForwardsConfirmOpts(t *testing.T) {
	var got ext.ConfirmRequest
	u := BusUI{ConfirmFn: func(req ext.ConfirmRequest) ext.ConfirmReply {
		got = req
		return ext.ConfirmReply{OK: false}
	}}
	req := ext.ConfirmRequest{
		Title:   "Overwrite?",
		Message: "Replace the file",
		Yes:     "Replace",
		No:      "Keep",
		Danger:  true,
	}

	assert.False(t, u.ConfirmOpts(req).OK)
	assert.Equal(t, req, got, "every field reaches the host callback unchanged")
}

func TestBusUIForwardsSetStatus(t *testing.T) {
	var gotKey, gotText string
	u := BusUI{SetStatusFn: func(key, text string) {
		gotKey, gotText = key, text
	}}

	u.SetStatus("job", "3 of 5")

	assert.Equal(t, "job", gotKey)
	assert.Equal(t, "3 of 5", gotText)
}

var _ ext.UI = BusUI{}
