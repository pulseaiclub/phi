package editor

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
)

func (e *Editor) setThinkingExpanded(expanded bool) {
	if err := e.ctrl.SetThinkingExpanded(expanded); err != nil {
		e.toast.Show(err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	e.transcript.SetThinkingExpanded(expanded)
	message := "Thinking: collapsed (saved)"
	if expanded {
		message = "Thinking: expanded (saved)"
	}
	e.toast.Show(message, toast.ToastSuccess, 2*time.Second)
	e.requestRedraw()
}
