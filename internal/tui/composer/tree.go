package composer

import "github.com/pulseaiclub/phi/internal/components/sessiontree"

func (c *ComposerPane) ShowTree(items []sessiontree.Item, current, filter string, keys map[string]string) {
	c.HideCompleters()
	c.HidePalette()
	c.listPicker.Hide()
	c.Tree.Theme = c.theme
	c.Tree.OnClose = c.FocusChat
	c.Tree.Show(items, current, filter, keys)
	if c.requestFocusEditor != nil {
		c.requestFocusEditor()
	}
	if c.onRedraw != nil {
		c.onRedraw()
	}
}

func (c *ComposerPane) RestoreTreeFocus() {
	if c.Tree.Open && c.requestFocusEditor != nil {
		c.requestFocusEditor()
	} else {
		c.FocusChat()
	}
}
