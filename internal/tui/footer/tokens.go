package footer

import (
	"fmt"
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/session"
)

// contextFillLevel ranks context-window pressure for the fill label.
type contextFillLevel int

const (
	contextFillOK contextFillLevel = iota
	contextFillRecommend
	contextFillWarning
	contextFillDanger
)

func contextFillRatio(used, window int) float64 {
	if window <= 0 || used <= 0 {
		return 0
	}
	return float64(used) / float64(window)
}

func contextFillLevelFor(ratio float64, window int) contextFillLevel {
	// Tiers scaled by window size: very large windows tolerate higher fills.
	var recommend, warning, danger float64
	switch {
	case window >= 900000:
		recommend, warning, danger = 0.2, 0.8, 0.9
	case window >= 400000:
		recommend, warning, danger = 0.7, 0.8, 0.9
	default:
		recommend, warning, danger = 0.8, 0.9, 0.95
	}
	switch {
	case ratio >= danger:
		return contextFillDanger
	case ratio >= warning:
		return contextFillWarning
	case ratio >= recommend:
		return contextFillRecommend
	default:
		return contextFillOK
	}
}

// formatContextLabel builds a "context: 4% of 128k" fill label (empty when unknown).
func formatContextLabel(usage session.TokenUsage, window int) string {
	if window <= 0 {
		return ""
	}
	used := usage.ContextTokens()
	if used <= 0 {
		return ""
	}
	pct := min(max(int(contextFillRatio(used, window)*100), 0), 100)
	if window >= 1000 {
		// "4% of 128k" says what the percentage is of; "4%/128k" reads as a
		// fraction of an unknown unit.
		return fmt.Sprintf("%d%% of %s", pct, components.FormatTokens(window))
	}
	return fmt.Sprintf("%d%%", pct)
}

// formatUsageStats builds "↑1.2k ↓800 C900 W200 Σ3.0k" (empty when unknown).
// The buckets are disjoint copies of the provider's: ↑ is the input that missed
// the cache, C and W are the cache traffic, Σ their sum (or the reported total).
func formatUsageStats(usage session.TokenUsage) string {
	if !usage.Reported() {
		return ""
	}
	parts := make([]string, 0, 5)
	if usage.PromptTokens > 0 {
		parts = append(parts, "↑"+components.FormatTokens(usage.PromptTokens))
	}
	if usage.CompletionTokens > 0 {
		parts = append(parts, "↓"+components.FormatTokens(usage.CompletionTokens))
	}
	if usage.CachedTokens > 0 {
		parts = append(parts, "C"+components.FormatTokens(usage.CachedTokens))
	}
	if usage.CacheWriteTokens > 0 {
		parts = append(parts, "W"+components.FormatTokens(usage.CacheWriteTokens))
	}
	total := usage.ContextTokens()
	if total > 0 {
		parts = append(parts, "Σ"+components.FormatTokens(total))
	}
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for i := 1; i < len(parts); i++ {
		b.WriteString(" " + parts[i])
	}
	return b.String()
}

// PathLabelStyle styles ambient composer-border embeds (path, idle tokens, activity).
// Alias of ChromeLabelStyle — kept for call-site clarity at the cwd label.
func PathLabelStyle(th components.Theme) xui.Style {
	return ChromeLabelStyle(th)
}

// ChromeLabelStyle is the single ambient voice for chat-input border chrome.
// Model Identity stays the frame's only accent; Warning/Destructive escalate
// only under context pressure.
func ChromeLabelStyle(th components.Theme) xui.Style {
	// Muted without Dim so labels stay readable on dark borders.
	st := th.Muted
	st.Dim = false
	return st
}

// contextPressureStyle colors only the context-fill fragment under pressure.
// At rest it matches ChromeLabelStyle so usage stats never shout ToolName cyan.
func contextPressureStyle(th components.Theme, usage session.TokenUsage, window int) xui.Style {
	used := usage.ContextTokens()
	ratio := contextFillRatio(used, window)
	switch contextFillLevelFor(ratio, window) {
	case contextFillDanger:
		return th.Destructive
	case contextFillWarning:
		return th.Warning
	default:
		return ChromeLabelStyle(th)
	}
}

// tokenStatusLabel builds the idle bottom-left label: usage always ambient,
// context % escalates alone when the window is under pressure.
func tokenStatusLabel(th components.Theme, usage session.TokenUsage, window int) layout.BorderLabel {
	stats := formatUsageStats(usage)
	ctx := formatContextLabel(usage, window)
	chrome := ChromeLabelStyle(th)
	if stats == "" && ctx == "" {
		return layout.BorderLabel{}
	}
	if ctx == "" {
		return layout.BorderLabel{Text: stats, Style: chrome}
	}
	pressure := contextPressureStyle(th, usage, window)
	if stats == "" {
		return layout.BorderLabel{Text: ctx, Style: pressure}
	}
	if contextFillLevelFor(contextFillRatio(usage.ContextTokens(), window), window) <= contextFillRecommend {
		return layout.BorderLabel{Text: joinBorderParts(stats, ctx), Style: chrome}
	}
	return layout.BorderLabel{Spans: []layout.BorderSpan{
		{Text: stats + " ", Style: chrome},
		{Text: ctx, Style: pressure},
	}}
}

// joinBorderParts concatenates non-empty label fragments with a single space.
func joinBorderParts(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += " "
		}
		out += p
	}
	return out
}
