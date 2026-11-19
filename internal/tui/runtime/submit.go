package runtime

// SubmitFilter consumes one submitted text line; true means handled. Filters
// run in registration order before the shell's own submit path — the bash
// plugin claims "!cmd" prefixes here.
type SubmitFilter func(text string) bool

// AddSubmitFilter registers a submit filter.
func (rt *Runtime) AddSubmitFilter(f SubmitFilter) { rt.submitFilters = append(rt.submitFilters, f) }

// FilterSubmit runs the filter chain; true means the text was consumed.
func (rt *Runtime) FilterSubmit(text string) bool {
	for _, f := range rt.submitFilters {
		if f(text) {
			return true
		}
	}
	return false
}
