package runtime

// SubmitChain owns the submit filter chain. Filters run in registration order
// before the shell's own submit path — the bash plugin claims "!cmd" prefixes
// here.
type SubmitChain struct {
	filters []func(text string) bool
}

// Add registers a filter that consumes one submitted text line; true means
// handled.
func (sc *SubmitChain) Add(f func(text string) bool) { sc.filters = append(sc.filters, f) }

// Filter runs the chain; true means the text was consumed.
func (sc *SubmitChain) Filter(text string) bool {
	for _, f := range sc.filters {
		if f(text) {
			return true
		}
	}
	return false
}
