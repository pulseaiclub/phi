package phi

import (
	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

func (extension *ExtensionAPI) OnSessionBeforeTree(fn func(ext.SessionBeforeTreeEvent) *ext.SessionBeforeTreeResult) {
	extension.mu.Lock()
	defer extension.mu.Unlock()
	extension.onSessionBeforeTree = fn
	extension.intercept = appendUnique(extension.intercept, pxb.EvSessionBeforeTree)
}

func (extension *ExtensionAPI) OnSessionTree(fn func(ext.SessionTreeEvent)) {
	extension.SubscribeEvent(ext.EventSessionTree, func(event pxb.EventNotify) {
		v, err := pxb.DecodeTreeNavigation(event.Input)
		if err == nil && fn != nil {
			fn(ext.SessionTreeEvent{FromID: v.FromID, TargetID: v.TargetID, Summary: v.Summary, SummaryID: v.SummaryID})
		}
	})
}
