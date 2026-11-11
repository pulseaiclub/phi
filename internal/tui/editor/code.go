package editor

import (
	"context"
	"time"

	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/listrows"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
)

func (e *Editor) showCodeFiles() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	paths, truncated, err := filesearch.Search(ctx, e.cwd, "", 10000)
	if err != nil {
		e.toast.Show("List files: "+err.Error(), toast.ToastError, 4*time.Second)
		e.composer.FocusChat()
		return
	}
	cfg := listrows.FileConfig()
	if truncated {
		cfg.Title = "Files (first 10000; use /code path for other files)"
	}
	e.composer.ShowList(listrows.FileItems(paths), cfg, func(item listpicker.Item) {
		// Picker IDs are literal paths, not the @path:line command syntax.
		e.code.OpenAt(item.ID, 0)
		e.composer.SetListFocusReturn(e)
		e.captureOverlayFocus()
	}, nil)
}
