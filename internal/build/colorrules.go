package build

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jsdrews/tuilib/pkg/ansi"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// applyColorRules wraps value with ansi.CellColor when one of the rules
// matches. Rules evaluate in order; first match wins. Returns value
// unchanged when no rule matches or the matched rule's color spec is
// unparseable. Designed for table cells — foreground-only (see
// cellColor) so the selected-row background passes through.
func applyColorRules(value string, rules []cfg.ColorRule, th theme.Theme) string {
	if len(rules) == 0 {
		return value
	}
	for _, r := range rules {
		if matchRule(value, r.When) {
			return cellColor(r.Color, value, th)
		}
	}
	return value
}

// cellColor wraps text in spec's foreground with a foreground-only
// reset. A palette index (named, 0-255, or a theme token that is one)
// goes through ansi.CellColor; a hex color — a literal, or a theme token
// from a palette written in hex — is downsampled to the terminal's color
// profile, so it renders on 256- and 16-color terminals too. An
// unparseable spec, or a terminal with no color, leaves text plain.
func cellColor(spec, text string, th theme.Theme) string {
	if n := colorIndexFromSpec(spec, th); n >= 0 {
		return ansi.CellColor(n, text)
	}
	c, ok := parseColor(spec, th).(lipgloss.Color)
	if !ok || !strings.HasPrefix(string(c), "#") {
		return text
	}
	seq := lipgloss.ColorProfile().Color(string(c)).Sequence(false)
	if seq == "" {
		return text
	}
	return "\x1b[" + seq + "m" + text + "\x1b[39m"
}

// matchRule reports whether the cell value satisfies the rule's `when:`.
// Recognised syntax:
//
//	""            wildcard — always matches (use as a terminal default rule)
//	"Running"     exact case-insensitive match (whitespace trimmed)
//	"~^Run"       case-insensitive regex (strip the ~ prefix)
//	">5"          numeric comparison: > >= < <= == !=
//	">=2M"        comparison with SI suffix (K/M/B/G/T — same parser as
//	              the `sort: si` column comparator)
func matchRule(value, when string) bool {
	when = strings.TrimSpace(when)
	if when == "" {
		return true
	}
	if strings.HasPrefix(when, "~") {
		re, err := regexp.Compile("(?i)" + strings.TrimPrefix(when, "~"))
		if err == nil && re.MatchString(value) {
			return true
		}
		return false
	}
	if op, rhs, ok := parseComparison(when); ok {
		a := parseSI(value)
		b := parseSI(rhs)
		switch op {
		case ">":
			return a > b
		case ">=":
			return a >= b
		case "<":
			return a < b
		case "<=":
			return a <= b
		case "==":
			return a == b
		case "!=":
			return a != b
		}
	}
	return strings.EqualFold(strings.TrimSpace(value), when)
}

// parseComparison extracts the leading comparison operator and the
// right-hand side from a `when:` value. Two-char operators are checked
// first so `">="` doesn't get parsed as `">" + "=…"`.
func parseComparison(when string) (op, rhs string, ok bool) {
	for _, o := range []string{">=", "<=", "==", "!="} {
		if strings.HasPrefix(when, o) {
			return o, strings.TrimSpace(strings.TrimPrefix(when, o)), true
		}
	}
	for _, o := range []string{">", "<"} {
		if strings.HasPrefix(when, o) {
			return o, strings.TrimSpace(strings.TrimPrefix(when, o)), true
		}
	}
	return "", "", false
}
