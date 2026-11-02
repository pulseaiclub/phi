package configui

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/textfield"
	"github.com/pulseaiclub/phi/internal/project"
)

const (
	// fetchTimeout caps one provider model-list request.
	fetchTimeout = 15 * time.Second
	// pickerPrimaryWidth keeps choice labels in one column.
	pickerPrimaryWidth = 22
)

// ConfigEditor is the full-screen config form. It implements components.Widget and is
// driven by the App loop, so all state lives on the UI goroutine — except the
// background model-list fetch, which hands its result back under fetchMu.
type ConfigEditor struct {
	doc    *project.ConfigDoc
	path   string
	skills string
	theme  components.Theme
	list   ModelLister
	redraw func()

	rows     []row
	cursor   int
	scroll   int
	expanded string
	dirty    bool

	status     string
	statusKind statusKind

	edit    *editState
	confirm *confirmState
	picker  listpicker.Picker

	// Painted geometry, recorded by Draw for hit testing, scrolling and the
	// text caret.
	width   int
	rowsTop int
	page    int
	caretX  int
	caretY  int

	fetchMu sync.Mutex
	fetch   fetchState
}

// New builds the editor for doc. path is the file to write, skills is the
// directory an empty skill_path falls back to, list fetches provider model IDs
// (nil hides the feature), and redraw wakes the app after a background fetch.
func New(
	doc *project.ConfigDoc,
	path, skills string,
	theme components.Theme,
	list ModelLister,
	redraw func(),
) *ConfigEditor {
	e := &ConfigEditor{
		doc:    doc,
		path:   path,
		skills: skills,
		theme:  theme,
		list:   list,
		redraw: redraw,
	}
	// Build once so the editor answers keys before the first frame.
	e.rebuild()
	e.move(1)
	return e
}

// Dirty reports whether the document has unsaved edits.
func (e *ConfigEditor) Dirty() bool { return e != nil && e.dirty }

type editState struct {
	key  string
	line textfield.Field
}

// confirmState is a yes/no modal over the form.
type confirmState struct {
	title  string
	body   string
	danger bool
	yes    bool
	run    func(ctx *components.EventContext)
}

type fetchState struct {
	active bool
	done   bool
	index  int
	// name is the model's name when the fetch started; if the entry at index
	// no longer matches it, the result is for a model that moved or was
	// replaced while the request was in flight.
	name string
	ids  []string
	err  error
}

// Handle routes one event. Modal states own the keyboard outright; otherwise
// navigation, editing, and the shortcut keys share it.
func (e *ConfigEditor) Handle(ctx *components.EventContext, ev xui.Event) {
	switch ev := ev.(type) {
	case xui.KeyEvent:
		if !ev.Press {
			return
		}
		e.handleKey(ctx, ev)
	case xui.PasteEvent:
		if e.edit != nil {
			e.edit.line.Insert(ev.Text)
			ctx.ConsumeAndRedraw()
			return
		}
		ctx.Consume = true

	case xui.MouseEvent:
		e.handleMouse(ctx, ev)
	}
}

func (e *ConfigEditor) handleKey(ctx *components.EventContext, k xui.KeyEvent) {
	switch {
	case e.picker.Open:
		e.picker.Handle(ctx, k)
	case e.confirm != nil:
		e.handleConfirmKey(ctx, k)
	case e.edit != nil:
		e.handleEditKey(ctx, k)
	default:
		e.handleNavKey(ctx, k)
	}
}

func (e *ConfigEditor) handleNavKey(ctx *components.EventContext, k xui.KeyEvent) {
	switch k.Code {
	case xui.KeyUp:
		e.move(-1)
	case xui.KeyDown:
		e.move(1)
	case xui.KeyPageUp:
		e.move(-max(e.page, 1))
	case xui.KeyPageDown:
		e.move(max(e.page, 1))
	case xui.KeyHome:
		e.jumpEdge(false)
	case xui.KeyEnd:
		e.jumpEdge(true)
	case xui.KeyLeft:
		e.cycleFocused(-1)
	case xui.KeyRight:
		e.cycleFocused(1)
	case xui.KeyEnter, xui.KeyTab:
		e.activate()
	case xui.KeyEscape:
		if e.expanded != "" {
			e.collapse()
		} else {
			e.requestQuit(ctx)
		}
	case xui.KeyRune:
		if k.Mods.Has(xui.ModAlt) {
			ctx.Consume = true
			return
		}
		if k.Mods.Has(xui.ModCtrl) {
			if k.Rune != 's' && k.Rune != 'S' {
				ctx.Consume = true
				return
			}
			e.save()
			break
		}
		switch k.Rune {
		case 'k':
			e.move(-1)
		case 'j':
			e.move(1)
		case 'g':
			e.jumpEdge(false)
		case 'G':
			e.jumpEdge(true)
		case 'a':
			e.addModel()
		case 'd':
			e.deleteFocused()
		case 'f':
			e.fetchModels()
		case 's':
			e.save()
		case 'q':
			e.requestQuit(ctx)
		case ' ':
			e.toggleFocused()
		default:
			ctx.Consume = true
			return
		}
	default:
		ctx.Consume = true
		return
	}
	ctx.ConsumeAndRedraw()
}

func (e *ConfigEditor) focused() (row, bool) {
	if e.cursor < 0 || e.cursor >= len(e.rows) {
		return row{}, false
	}
	return e.rows[e.cursor], true
}

func (e *ConfigEditor) move(delta int) {
	i := e.cursor
	for {
		i += delta
		if i < 0 || i >= len(e.rows) {
			return
		}
		if e.rows[i].selectable() {
			break
		}
	}
	e.setCursor(i)
}

// jumpEdge moves to the first or last selectable row.
func (e *ConfigEditor) jumpEdge(last bool) {
	for i := range e.rows {
		index := i
		if last {
			index = len(e.rows) - 1 - i
		}
		if e.rows[index].selectable() {
			e.setCursor(index)
			return
		}
	}
}

// setCursor moves the cursor, collapsing an expanded list when the cursor
// leaves it. Row indices shift on collapse, so the target key is captured
// first.
func (e *ConfigEditor) setCursor(index int) {
	if index < 0 || index >= len(e.rows) || !e.rows[index].selectable() {
		return
	}
	key := e.rows[index].key
	if e.expanded != "" {
		if base, _, ok := splitListKey(key); !ok || base != e.expanded {
			e.expanded = ""
			e.rebuild()
		}
	}
	e.anchor(key, index)
}

// refocus rebuilds rows and puts the cursor back on key.
func (e *ConfigEditor) refocus(key string) {
	e.rebuild()
	e.anchor(key, e.cursor)
}

func (e *ConfigEditor) anchor(key string, fallback int) {
	if len(e.rows) == 0 {
		e.cursor = 0
		return
	}
	for i := range e.rows {
		if e.rows[i].key == key && e.rows[i].selectable() {
			e.cursor = i
			return
		}
	}
	fallback = min(max(fallback, 0), len(e.rows)-1)
	for fallback > 0 && !e.rows[fallback].selectable() {
		fallback--
	}
	e.cursor = fallback
}

func (e *ConfigEditor) rebuild() {
	e.build()
	e.cursor = min(max(e.cursor, 0), max(len(e.rows)-1, 0))
}

func (e *ConfigEditor) activate() {
	r, ok := e.focused()
	if !ok {
		return
	}
	switch r.kind {
	case rowAdd:
		e.startEdit(r.key, "")
	case rowItem:
		e.startEdit(r.key, r.label)
	case rowField:
		switch r.field {
		case kindList:
			e.expand(r.key)
		case kindBool, kindPlainBool:
			e.toggleFocused()
		case kindChoice:
			e.openKeyPicker(r.label, r.key, r.options)
		default:
			e.startEdit(r.key, r.raw)
		}
	}
}

func (e *ConfigEditor) expand(key string) {
	e.expanded = key
	e.rebuild()
	if len(e.listItems(key)) > 0 {
		e.anchor(listItemKey(key, 0), e.cursor)
		return
	}
	e.anchor(listAddKey(key), e.cursor)
}

func (e *ConfigEditor) collapse() {
	key := e.expanded
	e.expanded = ""
	e.refocus(key)
}

func (e *ConfigEditor) toggleFocused() {
	r, ok := e.focused()
	if !ok {
		return
	}
	switch r.field {
	case kindBool:
		e.commit(r.key, cycleTri(r.raw))
	case kindPlainBool:
		e.commit(r.key, onOff(r.raw != triOn))
	}
}

// cycleFocused steps a choice or toggle with ←/→ so the common edits never need
// a modal.
func (e *ConfigEditor) cycleFocused(delta int) {
	r, ok := e.focused()
	if !ok || r.kind != rowField {
		return
	}
	switch r.field {
	case kindBool:
		e.commit(r.key, cycleTri(r.raw))
	case kindPlainBool:
		e.commit(r.key, onOff(r.raw != triOn))
	case kindChoice:
		if len(r.options) == 0 {
			return
		}
		at := 0
		for i, o := range r.options {
			if o.value == r.raw {
				at = i
				break
			}
		}
		next := (at + delta + len(r.options)) % len(r.options)
		e.commit(r.key, r.options[next].value)
	}
}

func (e *ConfigEditor) commit(key, value string) {
	if err := e.apply(key, value); err != nil {
		e.setError(err.Error())
		return
	}
	e.dirty = true
	e.status, e.statusKind = "", statusNone
	e.refocus(key)
}

func (e *ConfigEditor) addModel() {
	e.expanded = ""
	e.doc.Models = append(e.doc.Models, project.ModelDoc{})
	if len(e.doc.Models) == 1 {
		e.doc.Models[0].Default = true
	}
	e.dirty = true
	index := len(e.doc.Models) - 1
	e.refocus(modelKey(index, fName))
	e.startEdit(modelKey(index, fName), "")
	e.setStatus("new model — type a model id")
}

func (e *ConfigEditor) deleteFocused() {
	r, ok := e.focused()
	if !ok {
		return
	}
	if r.kind == rowItem {
		_, index, _ := splitListKey(r.key)
		e.removeListItem(r.listKey, index)
		e.dirty = true
		e.refocus(listAddKey(r.listKey))
		return
	}
	index, ok := modelOf(r)
	if !ok {
		return
	}
	name := e.doc.Models[index].Name
	if strings.TrimSpace(name) == "" {
		name = "this model"
	} else {
		name = "model " + name
	}
	e.confirm = &confirmState{
		title:  "Delete " + name + "?",
		body:   "Nothing is written until you save.",
		danger: true,
		yes:    false, // destructive: Enter keeps the model
		run: func(*components.EventContext) {
			e.deleteModel(index)
		},
	}
}

func (e *ConfigEditor) deleteModel(index int) {
	if index < 0 || index >= len(e.doc.Models) {
		return
	}
	e.doc.Models = append(e.doc.Models[:index:index], e.doc.Models[index+1:]...)
	e.dirty = true
	// Keep one default: with none marked, the loader silently starts from the
	// first entry — make that the explicit choice here instead.
	defaults := 0
	for _, m := range e.doc.Models {
		if m.Default {
			defaults++
		}
	}
	if defaults == 0 && len(e.doc.Models) > 0 {
		e.doc.Models[0].Default = true
	}
	if next := min(index, len(e.doc.Models)-1); next >= 0 {
		e.refocus(modelKey(next, fName))
		return
	}
	e.refocus(keySkillPath)
}

// modelOf reports the model index a row belongs to.
func modelOf(r row) (int, bool) {
	switch r.kind {
	case rowModel:
		return r.model, true
	case rowField:
		if index, _, ok := splitModelKey(r.key); ok {
			return index, true
		}
	}
	return 0, false
}

func (e *ConfigEditor) save() {
	if err := e.doc.Validate(); err != nil {
		e.setError(err.Error())
		return
	}
	if err := e.doc.Save(e.path); err != nil {
		e.setError(err.Error())
		return
	}
	e.dirty = false
	e.status, e.statusKind = "saved "+e.path, statusSuccess
}

func (e *ConfigEditor) requestQuit(ctx *components.EventContext) {
	if !e.dirty {
		ctx.Quit = true
		return
	}
	e.confirm = &confirmState{
		title:  "Discard unsaved changes?",
		body:   "The file on disk still holds the last saved version.",
		danger: true,
		yes:    false,
		run: func(ctx *components.EventContext) {
			ctx.Quit = true
		},
	}
}

// handleConfirmKey moves between the two decision rows the way every other
// panel does: up/left picks the row above, down/right/tab the row below. The
// rows are painted Yes then No, so yes is the first one.
func (e *ConfigEditor) handleConfirmKey(ctx *components.EventContext, k xui.KeyEvent) {
	st := e.confirm
	switch k.Code {
	case xui.KeyUp, xui.KeyLeft:
		st.yes = true
	case xui.KeyDown, xui.KeyRight, xui.KeyTab:
		st.yes = false
	case xui.KeyEnter:
		e.confirm = nil
		if st.yes && st.run != nil {
			st.run(ctx)
		}
	case xui.KeyEscape:
		e.confirm = nil
	case xui.KeyRune:
		switch k.Rune {
		case 'y', 'Y':
			e.confirm = nil
			if st.run != nil {
				st.run(ctx)
			}
		case 'n', 'N':
			e.confirm = nil
		case 'h', 'H', 'k', 'K':
			st.yes = true
		case 'l', 'L', 'j', 'J':
			st.yes = false
		}
	default:
		ctx.Consume = true
		return
	}
	ctx.ConsumeAndRedraw()
}

func (e *ConfigEditor) startEdit(key, raw string) {
	e.edit = &editState{key: key}
	e.edit.line.SetText(raw)
	e.refocus(key)
}

func (e *ConfigEditor) handleEditKey(ctx *components.EventContext, k xui.KeyEvent) {
	switch k.Code {
	case xui.KeyEnter:
		e.commitEdit()
	case xui.KeyEscape:
		e.edit = nil
	case xui.KeyBackspace:
		e.edit.line.DeleteBefore()
	case xui.KeyDelete:
		e.edit.line.DeleteAt()
	case xui.KeyLeft:
		e.edit.line.Move(-1)
	case xui.KeyRight:
		e.edit.line.Move(1)
	case xui.KeyHome:
		e.edit.line.Home()
	case xui.KeyEnd:
		e.edit.line.End()

	case xui.KeyRune:
		if k.Mods.Has(xui.ModCtrl) {
			e.handleEditCtrl(k.Rune)
			break
		}
		if k.Mods.Has(xui.ModAlt) {
			ctx.Consume = true
			return
		}
		text := k.Text
		if text == "" && k.Rune >= 0x20 {
			text = string(k.Rune)
		}
		e.edit.line.Insert(text)

	default:
		ctx.Consume = true
		return
	}
	ctx.ConsumeAndRedraw()
}

func (e *ConfigEditor) handleEditCtrl(r rune) {
	switch unicode.ToLower(r) {
	case 'a':
		e.edit.line.Home()
	case 'e':
		e.edit.line.End()
	case 'u':
		e.edit.line.KillBefore()
	case 'k':
		e.edit.line.KillAfter()
	case 'w':
		// Rub out the word left of the caret: the space run, then the word
		// before it, leaving earlier text intact (readline's word-rubout).
		for {
			ch, ok := e.edit.line.PeekBefore()
			if !ok || !unicode.IsSpace(ch) {
				break
			}
			e.edit.line.DeleteBefore()
		}
		for {
			ch, ok := e.edit.line.PeekBefore()
			if !ok || unicode.IsSpace(ch) {
				break
			}
			e.edit.line.DeleteBefore()
		}
	}
}

func (e *ConfigEditor) commitEdit() {
	st := e.edit
	if st == nil {
		return
	}
	key := st.key
	value := st.line.Text()
	if err := e.apply(key, value); err != nil {
		e.setError(err.Error())
		return
	}
	e.edit = nil
	e.dirty = true
	e.status, e.statusKind = "", statusNone
	if base, index, ok := splitListKey(key); ok && index < 0 {
		e.refocus(listItemKey(base, len(e.listItems(base))-1))
		return
	}
	e.refocus(key)
}

func (e *ConfigEditor) fetchModels() {
	if e.list == nil {
		e.setError("model list is unavailable")
		return
	}
	r, ok := e.focused()
	if !ok {
		return
	}
	index, ok := modelOf(r)
	if !ok {
		e.setStatus("move to a model first")
		return
	}
	m := e.modelAt(index)
	if m == nil {
		return
	}
	e.fetchMu.Lock()
	if e.fetch.active {
		e.fetchMu.Unlock()
		e.setStatus("already fetching models…")
		return
	}
	e.fetch = fetchState{active: true, index: index, name: m.Name}
	e.fetchMu.Unlock()

	baseURL, apiKey, api, name := m.BaseURL, m.APIKey, m.API, m.Name
	e.setStatus("fetching models…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		ids, err := e.list(ctx, baseURL, apiKey, api, name)
		e.fetchMu.Lock()
		e.fetch = fetchState{done: true, index: index, name: name, ids: ids, err: err}
		e.fetchMu.Unlock()
		if e.redraw != nil {
			e.redraw()
		}
	}()
}

// pollFetch adopts a finished background fetch. It runs on the UI goroutine
// inside Draw, next to the frame that shows the "fetching" status.
func (e *ConfigEditor) pollFetch() {
	e.fetchMu.Lock()
	if !e.fetch.done {
		e.fetchMu.Unlock()
		return
	}
	result := e.fetch
	e.fetch = fetchState{}
	e.fetchMu.Unlock()

	if result.err != nil {
		e.setError("model list: " + result.err.Error())
		return
	}
	if len(result.ids) == 0 {
		e.setError("model list: the provider returned no models")
		return
	}
	m := e.modelAt(result.index)
	if m == nil || m.Name != result.name {
		e.setError("model list: the model changed while fetching — press f again")
		return
	}
	options := make([]option, 0, len(result.ids))
	for _, id := range result.ids {
		options = append(options, opt(id, id, "advertised by the provider"))
	}
	e.openKeyPicker("Model list", modelKey(result.index, fName), options)
}

func (e *ConfigEditor) openKeyPicker(title, key string, options []option) {
	if len(options) == 0 {
		return
	}
	current := e.rawFor(key)
	items := make([]listpicker.Item, 0, len(options))
	for _, o := range options {
		badge := ""
		if o.value == current {
			badge = "current"
		}
		items = append(items, listpicker.Item{ID: o.value, Primary: o.label, Badge: badge, Detail: o.desc})
	}
	e.picker = listpicker.Picker{
		Theme:    e.theme,
		OnAccept: func(item listpicker.Item) { e.commit(key, item.ID) },
	}
	e.picker.Show(items, listpicker.ShowConfig{
		Title:        title,
		PrimaryWidth: pickerPrimaryWidth,
		LeadingWidth: 1,
	})
}

func (e *ConfigEditor) handleMouse(ctx *components.EventContext, m xui.MouseEvent) {
	// Modals own the pointer: while one is up the form behind it must not move.
	// A press dismisses the picker (Esc-equivalent); the confirm modal is
	// keyboard-only.
	if e.picker.Open {
		if m.Action == xui.MousePress {
			e.picker.Hide()
			ctx.ConsumeAndRedraw()
			return
		}
		ctx.Consume = true
		return
	}
	if e.confirm != nil {
		ctx.Consume = true
		return
	}
	switch {
	case m.Button == xui.MouseWheelUp:
		e.move(-max(m.Wheel, 1))
	case m.Button == xui.MouseWheelDown:
		e.move(max(m.Wheel, 1))
	case m.Action == xui.MousePress && m.Button == xui.MouseLeft:
		// Clicking away from an inline edit drops it, matching Esc: a click must
		// never keep typing into the field the caret just left.
		if e.edit != nil {
			e.edit = nil
		}
		if index := e.rowAt(m.Y); index >= 0 {
			e.setCursor(index)
		}
	default:
		ctx.Consume = true
		return
	}
	ctx.ConsumeAndRedraw()
}

// rowAt maps a screen row to a selectable row index, or -1.
func (e *ConfigEditor) rowAt(y int) int {
	if e.page <= 0 || y < e.rowsTop || y >= e.rowsTop+e.page {
		return -1
	}
	index := e.scroll + (y - e.rowsTop)
	if index < 0 || index >= len(e.rows) || !e.rows[index].selectable() {
		return -1
	}
	return index
}

func (e *ConfigEditor) setError(msg string) {
	e.status, e.statusKind = msg, statusError
}

func (e *ConfigEditor) setStatus(msg string) {
	e.status, e.statusKind = msg, statusInfo
}

func (e *ConfigEditor) themeOrDefault() components.Theme {
	if e.theme.Border.Fg.Kind == 0 && e.theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return e.theme
}
