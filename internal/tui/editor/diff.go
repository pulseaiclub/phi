package editor

import (
	"context"
	"time"

	"github.com/pulseaiclub/phi/internal/components/changelist"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

func (e *Editor) showChanges(restore bool) {
	root, changes, err := gitx.ChangedFiles(context.Background(), e.cwd)
	if err != nil {
		e.toast.Show(err.Error(), toast.ToastError, 4*time.Second)
		e.composer.FocusChat()
		return
	}
	if len(changes) == 0 {
		e.toast.Show("No changes", toast.ToastSuccess, 2*time.Second)
		e.composer.FocusChat()
		return
	}
	byID := make(map[string]gitx.Change, len(changes))
	for _, change := range changes {
		byID[change.Path] = change
	}
	e.composer.ShowList(changelist.Items(changes), changelist.Config(), func(item listpicker.Item) {
		e.diffList = e.composer.ListState()
		e.diff.OpenChange(root, byID[item.ID])
		e.composer.SetListFocusReturn(e)
		e.captureOverlayFocus()
	}, nil, nil)
	if restore {
		e.composer.RestoreListState(e.diffList)
		if _, exists := byID[e.diffList.SelectedID]; !exists {
			e.toast.Show("Selected change is no longer available; list refreshed", toast.ToastWarning, 3*time.Second)
		}
	}
}
