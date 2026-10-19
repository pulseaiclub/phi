package configui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
)

// Field keys are stable ids: the cursor, the list editor, and tests all address
// fields by key, never by row index, so a rebuild may reorder rows freely.
const (
	keySkillPath = "skill_path"
	keyMaxRounds = "max_rounds"

	keyPermMode        = "perm.mode"
	keyPermWorkspace   = "perm.workspace_only_writes"
	keyPermTimeout     = "perm.ask_timeout_sec"
	keyPermDangerous   = "perm.dangerously_allow_all"
	keyPermBashDefault = "perm.bash.default"
	keyPermBashAllow   = "perm.bash.allow"
	keyPermBashDeny    = "perm.bash.deny"
	keyPermRules       = "perm.rules"

	keyAgentsEnabled = "agents.enabled"
	keyAgentsExplore = "agents.models.explore"
	keyAgentsReview  = "agents.models.review"
	keyAgentsWorker  = "agents.models.worker"
)

// keyPermRulePrefix keys one read-only row per configured rule.
const keyPermRulePrefix = "perm.rules."

// Model fields live under model.<index>.<field>.
const (
	fName  = "name"
	fKey   = "api_key"
	fURL   = "base_url"
	fAPI   = "api"
	fCtx   = "context_window"
	fImage = "image_enabled"
	fThink = "think_level"
	fDeflt = "default"
)

// defaultBaseURL mirrors the endpoint project.parseConfigFile falls back to.
const defaultBaseURL = "https://api.openai.com/v1"

// Tri-state toggle encodings. A bool field is written as absent (auto, the
// loader default), on, or off.
const (
	triAuto = "auto"
	triOn   = "on"
	triOff  = "off"
)

func modelKey(index int, field string) string {
	return "model." + strconv.Itoa(index) + "." + field
}

// splitModelKey parses model.<index>.<field>.
func splitModelKey(key string) (index int, field string, ok bool) {
	rest, found := strings.CutPrefix(key, "model.")
	if !found {
		return 0, "", false
	}
	rawIndex, field, found := strings.Cut(rest, ".")
	if !found {
		return 0, "", false
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		return 0, "", false
	}
	return index, field, true
}

func listItemKey(key string, index int) string {
	return key + "#" + strconv.Itoa(index)
}

func listAddKey(key string) string { return key + "#new" }

// splitListKey parses <field>#<index>, where index -1 marks the add row.
func splitListKey(key string) (field string, index int, ok bool) {
	base, suffix, found := strings.Cut(key, "#")
	if !found {
		return "", 0, false
	}
	if suffix == "new" {
		return base, -1, true
	}
	index, err := strconv.Atoi(suffix)
	if err != nil || index < 0 {
		return "", 0, false
	}
	return base, index, true
}

type rowKind int

const (
	rowSection rowKind = iota
	rowError
	rowModel
	rowField
	rowItem
	rowAdd
	rowNote
)

type fieldKind int

const (
	kindText fieldKind = iota
	kindSecret
	kindInt
	kindBool      // tri-state: auto / on / off
	kindPlainBool // plain bool with a default of off
	kindChoice
	kindList
)

// option is one choice value. An empty value drops the key from the file,
// which is how the loader's "use the default" state is expressed.
type option struct {
	value string
	label string
	desc  string
}

func opt(value, label, desc string) option { return option{value: value, label: label, desc: desc} }

// valueStyle picks a semantic color for a rendered value.
type valueStyle int

const (
	styleValue valueStyle = iota
	styleMuted
	styleOn
	styleWarn
)

// row is one painted line of the form. Rows are a view model: build rebuilds
// them from the document, and apply writes a single field back.
type row struct {
	kind rowKind
	key  string

	// rowSection / rowError
	title string
	note  string

	// rowModel
	model int
	name  string
	badge string

	// rowField / rowItem / rowAdd
	field     fieldKind
	label     string
	raw       string
	display   string
	style     valueStyle
	hint      string
	options   []option
	items     []string
	listKey   string // owning field key for rowItem / rowAdd
	inherited bool   // list rows showing built-in rules rather than file rules
}

func (r row) selectable() bool {
	switch r.kind {
	case rowSection, rowError, rowNote:
		return false
	default:
		return true
	}
}

// fieldSpec declares one editable field; fieldRow renders it against the
// document.
type fieldSpec struct {
	key         string
	label       string
	kind        fieldKind
	options     []option
	placeholder string
	// defaultOn is the loader's value when the bool key is absent.
	defaultOn bool
}

var (
	apiOptions = []option{
		opt("", "auto", "infer from the model name"),
		opt(string(llm.OpenAI), "OpenAI", "chat completions"),
		opt(string(llm.OpenAIResponses), "OpenAI Responses", "responses API"),
		opt(string(llm.Anthropic), "Anthropic", "messages API"),
		opt(string(llm.Gemini), "Gemini", "generateContent"),
	}
	thinkDescriptions = map[llm.ThinkMode]string{
		llm.Off:     "no reasoning",
		llm.Minimal: "shortest reasoning",
		llm.Low:     "light reasoning",
		llm.Medium:  "balanced",
		llm.High:    "deep reasoning",
		llm.XHigh:   "deeper still",
		llm.Max:     "provider maximum",
	}
	permModeOptions = []option{
		opt("", "auto", "interactive: ask before gated tools"),
		opt(string(permission.ModeInteractive), "interactive", "ask before gated tools"),
		opt(string(permission.ModeReadonly), "readonly", "deny writes, keep reads"),
		opt(string(permission.ModeAutopilot), "autopilot", "fold ask to allow"),
		opt(string(permission.ModeHeadlessStrict), "headless-strict", "fold ask to deny"),
	}
	bashDefaultOptions = []option{
		opt("", "auto", "policy default (ask)"),
		opt("ask", "ask", "prompt before running"),
		opt("allow", "allow", "run without asking"),
		opt("deny", "deny", "never run"),
	}
)

// build rebuilds rows from the document. The caller re-anchors the cursor
// afterwards; see ConfigEditor.refocus.
func (e *ConfigEditor) build() {
	e.rows = e.rows[:0]
	e.buildModels()
	e.addSection("Skills", "")
	skills := displayPath(e.skills)
	e.addField(e.fieldRow(fieldSpec{key: keySkillPath, label: "skill path", kind: kindText, placeholder: skills}))
	e.addSection("Agent Loop", "")
	e.addField(e.fieldRow(fieldSpec{key: keyMaxRounds, label: "max rounds", kind: kindInt, placeholder: "64"}))
	e.addSection("Permissions", "")
	e.addField(e.fieldRow(fieldSpec{key: keyPermMode, label: "mode", kind: kindChoice, options: permModeOptions}))
	e.addField(e.fieldRow(fieldSpec{
		key: keyPermWorkspace, label: "workspace only writes", kind: kindBool, defaultOn: true,
	}))
	e.addField(e.fieldRow(fieldSpec{
		key: keyPermTimeout, label: "ask timeout", kind: kindInt, placeholder: strconv.Itoa(defaultTimeout()),
	}))
	e.addField(e.fieldRow(fieldSpec{key: keyPermDangerous, label: "allow everything", kind: kindBool}))
	e.addSection("Bash", "")
	e.addField(e.fieldRow(fieldSpec{
		key: keyPermBashDefault, label: "default", kind: kindChoice, options: bashDefaultOptions,
	}))
	e.addListField(keyPermBashAllow, "allow patterns")
	e.addListField(keyPermBashDeny, "deny patterns")
	e.addSection("Rules", "")
	note := "none — set permissions.rules in config.yaml"
	if n := len(e.permRules()); n == 1 {
		note = "1 rule, read-only here — edit permissions.rules in config.yaml"
	} else if n > 1 {
		note = plural(n, "rule") + ", read-only here — edit permissions.rules in config.yaml"
	}
	e.addField(row{kind: rowNote, key: keyPermRules, label: note, style: styleMuted})
	for i, rule := range e.permRules() {
		e.addField(row{
			kind: rowNote, key: keyPermRulePrefix + strconv.Itoa(i),
			label: rule.Summary(), style: styleValue,
		})
	}
	e.addSection("Sub-agents", "")
	e.addField(e.fieldRow(fieldSpec{key: keyAgentsEnabled, label: "enabled", kind: kindBool, defaultOn: true}))
	for _, role := range []struct{ key, label string }{
		{keyAgentsExplore, "explore model"},
		{keyAgentsReview, "review model"},
		{keyAgentsWorker, "worker model"},
	} {
		e.addField(e.fieldRow(fieldSpec{
			key: role.key, label: role.label, kind: kindChoice, options: e.modelRefOptions(),
		}))
	}
}

func (e *ConfigEditor) buildModels() {
	note := "none yet — press a to add one"
	if n := len(e.doc.Models); n == 1 {
		note = "1 model"
	} else if n > 1 {
		note = strconv.Itoa(n) + " models"
	}
	e.addSection("Models", note)
	for i, m := range e.doc.Models {
		badge := ""
		if m.Default {
			badge = "default"
		}
		name := m.Name
		if strings.TrimSpace(name) == "" {
			name = "unnamed model"
		}
		resolved, err := project.ResolveModelDoc(m)
		e.rows = append(e.rows, row{
			kind:  rowModel,
			key:   modelKey(i, "header"),
			model: i,
			name:  name,
			badge: badge,
			note:  modelNote(m, resolved),
		})
		if err != nil {
			e.rows = append(e.rows, row{kind: rowError, key: modelKey(i, "error"), note: err.Error()})
		}
		for _, sp := range modelSpecs(resolved) {
			sp.key = modelKey(i, sp.key)
			e.addField(e.fieldRow(sp))
		}
	}
}

// Placeholders show resolved defaults, falling back to generic values for unknown presets.
func modelSpecs(cfg llm.ModelConfig) []fieldSpec {
	baseURL := defaultBaseURL
	if cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}
	modes := model.ThinkingModes(cfg)
	options := make([]option, 1, len(modes)+1)
	options[0] = opt("", "auto", "model or preset default")
	for _, mode := range modes {
		desc := thinkDescriptions[mode]
		if desc == "" {
			desc = "reasoning effort"
		}
		options = append(options, opt(string(mode), string(mode), desc))
	}
	return []fieldSpec{
		{key: fName, label: "name", kind: kindText, placeholder: "model id"},
		{key: fKey, label: "api key", kind: kindSecret},
		{key: fURL, label: "base url", kind: kindText, placeholder: baseURL},
		{key: fAPI, label: "api", kind: kindChoice, options: apiOptions},
		{key: fCtx, label: "context window", kind: kindInt, placeholder: humanTokens(cfg.ContextWindow)},
		{key: fImage, label: "image input", kind: kindBool, defaultOn: cfg.ImageEnabled},
		{key: fThink, label: "thinking", kind: kindChoice, options: options},
		{key: fDeflt, label: "use by default", kind: kindPlainBool},
	}
}

func (e *ConfigEditor) modelRefOptions() []option {
	out := []option{opt("", "inherit", "parent session model")}
	for _, m := range e.doc.Models {
		if strings.TrimSpace(m.Name) == "" {
			continue
		}
		out = append(out, opt(m.Name, m.Name, "configured model"))
	}
	return out
}

func (e *ConfigEditor) addSection(title, note string) {
	e.rows = append(e.rows, row{kind: rowSection, key: "§" + title, title: title, note: note})
}

func (e *ConfigEditor) addField(r row) { e.rows = append(e.rows, r) }

// addListField appends a list field and, while it is expanded, its item rows.
func (e *ConfigEditor) addListField(key, label string) {
	items := e.listItems(key)
	inherited := !e.listSet(key)
	display := plural(len(items), "pattern")
	style := styleValue
	if inherited {
		display, style = "built-in · "+plural(len(items), "pattern"), styleMuted
	}
	e.addField(row{
		kind: rowField, key: key, field: kindList, label: label,
		raw: strconv.Itoa(len(items)), display: display, style: style,
		items: items, listKey: key, inherited: inherited,
	})
	if e.expanded != key {
		return
	}
	for i, item := range items {
		style := styleValue
		if inherited {
			style = styleMuted
		}
		e.addField(row{
			kind: rowItem, key: listItemKey(key, i), label: item, style: style,
			items: items, listKey: key, inherited: inherited,
		})
	}
	e.addField(row{
		kind: rowAdd, key: listAddKey(key), label: addLabel(inherited),
		style: styleMuted, listKey: key, inherited: inherited,
	})
}

// addLabel names the "add a rule" row; a list that is still inheriting gets an
// explicit warning that the first edit writes the built-in rules out too.
func addLabel(inherited bool) string {
	if inherited {
		return "+ new pattern (keeps built-in rules)"
	}
	return "+ new pattern"
}

// fieldRow renders one field. It is the only place the value column is built,
// so styling stays consistent across sections.
func (e *ConfigEditor) fieldRow(sp fieldSpec) row {
	raw := e.rawFor(sp.key)
	r := row{kind: rowField, key: sp.key, field: sp.kind, label: sp.label, raw: raw, options: sp.options}
	switch sp.kind {
	case kindText:
		r.display, r.style = raw, styleValue
		if raw == "" {
			r.display, r.style, r.hint = unset(sp.placeholder), styleMuted, sp.placeholder
		}
	case kindSecret:
		if raw == "" {
			r.display, r.style = "not set", styleWarn
		} else {
			// A key that is set is data like any other value: bright dots say
			// "set", while the quiet tone is reserved for absent or inherited
			// values.
			r.display, r.style = maskSecret(raw), styleValue
		}
	case kindInt:
		r.display, r.style = raw, styleValue
		if raw == "" {
			r.display, r.style = unset(sp.placeholder), styleMuted
			r.hint = "loader default"
		}
	case kindBool:
		switch raw {
		case triOn:
			r.display, r.style = triOn, styleOn
		case triOff:
			r.display, r.style = triOff, styleValue
		default:
			r.display, r.style = triAuto, styleMuted
			r.hint = "loader default: " + onOff(sp.defaultOn)
		}
	case kindPlainBool:
		r.display, r.style = triOff, styleValue
		if raw == triOn {
			r.display, r.style = triOn, styleOn
		}
	case kindChoice:
		label, desc := optionFor(sp.options, raw)
		r.display, r.style, r.hint = label, styleValue, desc
		if raw == "" {
			r.style = styleMuted
		}
	}
	return r
}

// unset renders an empty field: the loader default when one is known, an
// em dash when the value only exists in the file.
func unset(fallback string) string {
	if fallback == "" {
		return "—"
	}
	return fallback
}

func optionFor(options []option, value string) (label, desc string) {
	for _, o := range options {
		if o.value == value {
			return o.label, o.desc
		}
	}
	// A hand-edited file may hold a value the picker does not offer; show it
	// rather than silently rewriting it.
	return value, "not a known value"
}

// rawFor returns the canonical string form of a field's value — the same
// encoding apply accepts back. "" means the key is absent.
func (e *ConfigEditor) rawFor(key string) string {
	if index, field, ok := splitModelKey(key); ok {
		m := e.modelAt(index)
		if m == nil {
			return ""
		}
		switch field {
		case fName:
			return m.Name
		case fKey:
			return m.APIKey
		case fURL:
			return m.BaseURL
		case fAPI:
			return m.API
		case fCtx:
			return intString(m.ContextWindow)
		case fImage:
			return triString(m.ImageEnabled)
		case fThink:
			return m.ThinkLevel
		case fDeflt:
			return onOff(m.Default)
		}
		return ""
	}
	if base, index, ok := splitListKey(key); ok {
		items := e.listItems(base)
		if index < 0 || index >= len(items) {
			return ""
		}
		return items[index]
	}
	switch key {
	case keySkillPath:
		return stringValue(e.doc.SkillPath)
	case keyMaxRounds:
		return intString(e.doc.MaxRounds)
	case keyPermMode:
		return permString(e.doc, func(p *project.PermDoc) string { return p.Mode })
	case keyPermWorkspace:
		return triString(permBool(e.doc, func(p *project.PermDoc) *bool { return p.WorkspaceOnlyWrites }))
	case keyPermTimeout:
		return intString(permInt(e.doc, func(p *project.PermDoc) *int { return p.AskTimeoutSec }))
	case keyPermDangerous:
		return triString(permBool(e.doc, func(p *project.PermDoc) *bool { return p.DangerouslyAllowAll }))
	case keyPermBashDefault:
		return bashString(e.doc, func(b *project.BashDoc) string { return b.Default })
	case keyAgentsEnabled:
		return triString(agentsBool(e.doc, func(a *project.AgentsDoc) *bool { return a.Enabled }))
	case keyAgentsExplore, keyAgentsReview, keyAgentsWorker:
		return agentsString(e.doc, func(m *project.AgentsRoleModels) string {
			switch key {
			case keyAgentsExplore:
				return m.Explore
			case keyAgentsReview:
				return m.Review
			default:
				return m.Worker
			}
		})
	}
	return ""
}

// apply writes one field. An error leaves the document untouched so the editor
// can report it and keep the user in the field.
func (e *ConfigEditor) apply(key, value string) error {
	if index, field, ok := splitModelKey(key); ok {
		return e.applyModel(index, field, value)
	}
	if base, index, ok := splitListKey(key); ok {
		return e.applyListItem(base, index, value)
	}
	switch key {
	case keySkillPath:
		e.doc.SkillPath = optionalString(strings.TrimSpace(value))
	case keyMaxRounds:
		rounds, err := parsePositiveInt(value, "rounds")
		if err != nil {
			return err
		}
		e.doc.MaxRounds = rounds
	case keyPermMode:
		perms(e.doc).Mode = strings.TrimSpace(value)
	case keyPermWorkspace:
		on, err := parseTri(value)
		if err != nil {
			return err
		}
		perms(e.doc).WorkspaceOnlyWrites = on
	case keyPermTimeout:
		secs, err := parsePositiveInt(value, "seconds")
		if err != nil {
			return err
		}
		perms(e.doc).AskTimeoutSec = secs
	case keyPermDangerous:
		on, err := parseTri(value)
		if err != nil {
			return err
		}
		perms(e.doc).DangerouslyAllowAll = on
	case keyPermBashDefault:
		bash(e.doc).Default = value
	case keyAgentsEnabled:
		on, err := parseTri(value)
		if err != nil {
			return err
		}
		agents(e.doc).Enabled = on
	case keyAgentsExplore, keyAgentsReview, keyAgentsWorker:
		role := strings.TrimPrefix(key, "agents.models.")
		switch role {
		case "explore":
			agentsModels(e.doc).Explore = value
		case "review":
			agentsModels(e.doc).Review = value
		case "worker":
			agentsModels(e.doc).Worker = value
		default:
			return fmt.Errorf("unknown role %q", role)
		}
	default:
		return fmt.Errorf("unknown field %q", key)
	}
	return nil
}

func (e *ConfigEditor) applyModel(index int, field, value string) error {
	m := e.modelAt(index)
	if m == nil {
		return fmt.Errorf("model %d no longer exists", index+1)
	}
	switch field {
	case fName:
		name := strings.TrimSpace(value)
		if name == "" {
			return errors.New("model name cannot be empty")
		}
		m.Name = name
	case fKey:
		m.APIKey = strings.TrimSpace(value)
	case fURL:
		m.BaseURL = strings.TrimSpace(value)
	case fAPI:
		m.API = value
	case fCtx:
		window, err := parsePositiveInt(value, "tokens")
		if err != nil {
			return err
		}
		m.ContextWindow = window
	case fImage:
		on, err := parseTri(value)
		if err != nil {
			return err
		}
		m.ImageEnabled = on
	case fThink:
		m.ThinkLevel = value
	case fDeflt:
		on := value == triOn
		if on {
			for i := range e.doc.Models {
				if i != index {
					e.doc.Models[i].Default = false
				}
			}
		}
		m.Default = on
	default:
		return fmt.Errorf("unknown model field %q", field)
	}
	return nil
}

func (e *ConfigEditor) applyListItem(base string, index int, value string) error {
	items := e.listItems(base)
	// The pattern is stored verbatim: bash rules are regexes, and trimming one
	// would quietly change what it matches.
	rule := value
	switch {
	case index < 0:
		if strings.TrimSpace(rule) == "" {
			return errors.New("pattern cannot be empty")
		}
		items = append(items, rule)
	case index < len(items):
		if strings.TrimSpace(rule) == "" {
			return errors.New("pattern cannot be empty")
		}
		items[index] = rule
	default:
		return fmt.Errorf("no pattern at %d", index+1)
	}
	return e.setList(base, items)
}

// listItems returns the effective rules of a bash list field: the explicit list
// when the key is set, otherwise the loader's built-in defaults. Always a copy,
// so callers may edit it.
func (e *ConfigEditor) listItems(key string) []string {
	var list []string
	switch key {
	case keyPermBashAllow:
		list = bashStringList(e.doc, func(b *project.BashDoc) *project.StringList { return b.Allow })
	case keyPermBashDeny:
		list = bashStringList(e.doc, func(b *project.BashDoc) *project.StringList { return b.Deny })
	default:
		return nil
	}
	if list == nil {
		policy := permission.DefaultPolicy()
		if key == keyPermBashAllow {
			list = policy.BashAllow
		} else {
			list = policy.BashDeny
		}
	}
	return append([]string(nil), list...)
}

// listSet reports whether the rules are written in the file, which is when
// there is no such key.
func (e *ConfigEditor) listSet(key string) bool {
	switch key {
	case keyPermBashAllow:
		return bashStringList(e.doc, func(b *project.BashDoc) *project.StringList { return b.Allow }) != nil
	case keyPermBashDeny:
		return bashStringList(e.doc, func(b *project.BashDoc) *project.StringList { return b.Deny }) != nil
	default:
		return false
	}
}

// setList writes rules as an explicit list. An empty list stays explicit on
// purpose: it is how a user says "no patterns at all".
func (e *ConfigEditor) setList(key string, items []string) error {
	list := project.StringList(items)
	b := bash(e.doc)
	switch key {
	case keyPermBashAllow:
		b.Allow = &list
	case keyPermBashDeny:
		b.Deny = &list
	default:
		return fmt.Errorf("unknown rule list %q", key)
	}
	return nil
}

// removeListItem drops one rule, materializing the built-in rules first when
// the file had none.
func (e *ConfigEditor) removeListItem(key string, index int) {
	items := e.listItems(key)
	if index < 0 || index >= len(items) {
		return
	}
	items = append(items[:index:index], items[index+1:]...)
	if err := e.setList(key, items); err != nil {
		e.setError(err.Error())
	}
}

func (e *ConfigEditor) modelAt(index int) *project.ModelDoc {
	if index < 0 || index >= len(e.doc.Models) {
		return nil
	}
	return &e.doc.Models[index]
}

func modelNote(m project.ModelDoc, cfg llm.ModelConfig) string {
	api := m.API
	window := 0
	if m.ContextWindow != nil {
		window = *m.ContextWindow
	}
	if api == "" {
		api = string(cfg.API)
	}
	if window == 0 {
		window = cfg.ContextWindow
	}
	if api == "" {
		api = "auto"
	}
	parts := []string{api}
	if window > 0 {
		parts = append(parts, humanTokens(window)+" context")
	}
	return strings.Join(parts, " · ")
}

// maskSecret keeps the last four characters visible: enough to tell two keys
// apart while editing a list of models, not enough to leak one.
func maskSecret(value string) string {
	runes := []rune(value)
	if len(runes) <= 8 {
		return strings.Repeat("•", len(runes))
	}
	return "••••" + string(runes[len(runes)-4:])
}

func humanTokens(n int) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1_000_000:
		// A whole million stays "1M"; a fractional one keeps one digit so a
		// 1.28M context is not rounded down to "1M".
		if n%1_000_000 == 0 {
			return strconv.Itoa(n/1_000_000) + "M"
		}
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.Itoa(n/1000) + "k"
	default:
		return strconv.Itoa(n)
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func onOff(on bool) string {
	if on {
		return triOn
	}
	return triOff
}

func triString(v *bool) string {
	if v == nil {
		return triAuto
	}
	return onOff(*v)
}

func parseTri(value string) (*bool, error) {
	switch value {
	case triAuto:
		return nil, nil
	case triOn:
		on := true
		return &on, nil
	case triOff:
		off := false
		return &off, nil
	default:
		return nil, fmt.Errorf("expected auto, on, or off — got %q", value)
	}
}

// cycleTri advances the tri-state toggle: auto (absent) → on → off → auto.
func cycleTri(raw string) string {
	switch raw {
	case triAuto:
		return triOn
	case triOn:
		return triOff
	default:
		return triAuto
	}
}

func parsePositiveInt(value, unit string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("%q is not a number", value)
	}
	if n <= 0 {
		return nil, fmt.Errorf("must be a positive number of %s", unit)
	}
	return &n, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func intString(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func defaultTimeout() int { return permission.DefaultPolicy().AskTimeoutSec }

func perms(doc *project.ConfigDoc) *project.PermDoc {
	if doc.Permissions == nil {
		doc.Permissions = &project.PermDoc{}
	}
	return doc.Permissions
}

// permRules returns the configured rules; the editor shows them without
// editing them, since a rule is a struct rather than the scalar this form edits.
func (e *ConfigEditor) permRules() []permission.RuleSpec {
	if e.doc.Permissions == nil {
		return nil
	}
	return e.doc.Permissions.Rules
}

func bash(doc *project.ConfigDoc) *project.BashDoc {
	p := perms(doc)
	if p.Bash == nil {
		p.Bash = &project.BashDoc{}
	}
	return p.Bash
}

func agents(doc *project.ConfigDoc) *project.AgentsDoc {
	if doc.Agents == nil {
		doc.Agents = &project.AgentsDoc{}
	}
	return doc.Agents
}

func agentsModels(doc *project.ConfigDoc) *project.AgentsRoleModels {
	a := agents(doc)
	if a.Models == nil {
		a.Models = &project.AgentsRoleModels{}
	}
	return a.Models
}

func permString(doc *project.ConfigDoc, pick func(*project.PermDoc) string) string {
	if doc.Permissions != nil {
		return pick(doc.Permissions)
	}
	return ""
}

func permBool(doc *project.ConfigDoc, pick func(*project.PermDoc) *bool) *bool {
	if doc.Permissions != nil {
		return pick(doc.Permissions)
	}
	return nil
}

func permInt(doc *project.ConfigDoc, pick func(*project.PermDoc) *int) *int {
	if doc.Permissions != nil {
		return pick(doc.Permissions)
	}
	return nil
}

func bashString(doc *project.ConfigDoc, pick func(*project.BashDoc) string) string {
	if doc.Permissions != nil && doc.Permissions.Bash != nil {
		return pick(doc.Permissions.Bash)
	}
	return ""
}

func bashStringList(doc *project.ConfigDoc, pick func(*project.BashDoc) *project.StringList) []string {
	if doc.Permissions == nil || doc.Permissions.Bash == nil {
		return nil
	}
	if list := pick(doc.Permissions.Bash); list != nil {
		return *list
	}
	return nil
}

func agentsBool(doc *project.ConfigDoc, pick func(*project.AgentsDoc) *bool) *bool {
	if doc.Agents != nil {
		return pick(doc.Agents)
	}
	return nil
}

func agentsString(doc *project.ConfigDoc, pick func(*project.AgentsRoleModels) string) string {
	if doc.Agents != nil && doc.Agents.Models != nil {
		return pick(doc.Agents.Models)
	}
	return ""
}
