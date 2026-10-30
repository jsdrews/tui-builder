// Package columns ranks and styles the columns tui-builder writes for
// you. The evidence differs by caller (a JSON sample for `add exec`, a
// schema for the OpenAPI generator), so callers classify each column;
// this package holds the vocabularies, the ranking and the style.
package columns

import (
	"regexp"
	"slices"
	"strings"
)

// Rank is how important a column is; lower comes first.
type Rank int

const (
	Identity Rank = iota // what a row is: its name, title or id
	Status               // a small set of states, or a boolean
	Time                 // timestamps
	Number               // counts, sizes, amounts
	Short                // other short text
	Long                 // long text: descriptions, messages
	Empty                // empty in every row seen
)

// Column is one column as the caller found it.
type Column struct {
	Path  string
	Title string
	Rank  Rank
	// Bool is a boolean column (true/false values).
	Bool bool
	// Values are the distinct values seen (a sample) or declared (an
	// enum), for status colours.
	Values []string
	// Sort is the comparator: "", "number" or "si".
	Sort string
}

// DefaultMax is how many columns are kept; the rest are written
// commented out.
const DefaultMax = 8

// Pick orders cols by rank (keeping the given order within a rank) and
// keeps the first max, with one slot guaranteed to the top long-text
// column: it takes the last slot, displacing the last short one (or,
// with none, the last kept). It returns the kept columns and the rest.
func Pick(cols []Column, max int) (kept, rest []Column) {
	ordered := slices.Clone(cols)
	slices.SortStableFunc(ordered, func(a, b Column) int { return int(a.Rank) - int(b.Rank) })
	if max <= 0 || len(ordered) <= max {
		return ordered, nil
	}
	kept = slices.Clone(ordered[:max])
	rest = slices.Clone(ordered[max:])
	if i := slices.IndexFunc(rest, func(c Column) bool { return c.Rank == Long }); i >= 0 {
		long := rest[i]
		out := len(kept) - 1
		for j := len(kept) - 1; j >= 0; j-- {
			if kept[j].Rank == Short {
				out = j
				break
			}
		}
		displaced := kept[out]
		kept = append(slices.Delete(kept, out, out+1), long)
		rest = slices.Delete(rest, i, i+1)
		rest = slices.Insert(rest, 0, displaced)
		slices.SortStableFunc(rest, func(a, b Column) int { return int(a.Rank) - int(b.Rank) })
	}
	return kept, rest
}

// Rule is one colour rule: when the cell matches When, it's drawn in
// Color.
type Rule struct {
	When  string
	Color string
}

// Style is how a kept column is drawn.
type Style struct {
	Bold     bool
	Flex     int
	MaxWidth int
	Align    string // "" or "right"
	Rules    []Rule
}

// Styles styles the kept columns: bold identity; status colours from
// the vocabulary, for values seen; booleans coloured only when named as
// a check; failure counts warn above zero; numbers and timestamps right-
// aligned; flex on the long-text column, else on the identity; a
// max_width of 20 on status columns.
func Styles(kept []Column) []Style {
	styles := make([]Style, len(kept))
	flex := slices.IndexFunc(kept, func(c Column) bool { return c.Rank == Long })
	if flex < 0 {
		flex = slices.IndexFunc(kept, func(c Column) bool { return c.Rank == Identity })
	}
	for i, c := range kept {
		s := &styles[i]
		name := lastSegment(c.Path)
		switch c.Rank {
		case Identity:
			s.Bold = true
		case Status:
			s.MaxWidth = 20
			switch {
			case c.Bool && IsCheckName(name):
				s.Rules = []Rule{{"true", "theme:success"}, {"false", "theme:error"}}
			case !c.Bool:
				s.Rules = StatusRules(c.Values)
			}
		case Time, Number:
			s.Align = "right"
		}
		if c.Rank == Number && IsFailureCount(name) {
			s.Rules = []Rule{{"> 0", "theme:warning"}}
		}
		if i == flex {
			s.Flex = 1
			s.MaxWidth = 0
		}
	}
	return styles
}

// The status vocabulary, matched case-insensitively against whole
// values.
var (
	successWords = []string{"running", "ready", "succeeded", "success", "healthy", "active", "ok", "true", "pass", "passed", "passing", "complete", "completed", "done", "up", "available", "bound", "online", "enabled", "green"}
	warningWords = []string{"pending", "waiting", "progressing", "degraded", "unknown", "warn", "warning", "starting", "stopping", "terminating", "creating", "updating", "queued", "paused", "yellow"}
	errorWords   = []string{"failed", "failure", "error", "errored", "crashloopbackoff", "imagepullbackoff", "errimagepull", "oomkilled", "unhealthy", "false", "fatal", "critical", "down", "offline", "lost", "evicted", "red", "rejected", "denied"}
)

// The catch-alls written after the seen words, for values the sample
// didn't show.
const (
	errorFamily   = "~(fail|error|crash|backoff|unhealthy|fatal|critical|denied|reject|oom|evict)"
	warningFamily = "~(pend|wait|progress|degrad|unknown|warn|terminat|queue|retry)"
)

// StatusRules writes colour rules for the vocabulary words among
// values, in the order seen, then the bad and warning catch-alls. No
// rule for a value outside the vocabulary, and none at all when no
// value is in it: unknown values render plain.
func StatusRules(values []string) []Rule {
	var rules []Rule
	seen := map[string]bool{}
	for _, v := range values {
		lv := strings.ToLower(strings.TrimSpace(v))
		if lv == "" || seen[lv] {
			continue
		}
		seen[lv] = true
		var color string
		switch {
		case slices.Contains(successWords, lv):
			color = "theme:success"
		case slices.Contains(warningWords, lv):
			color = "theme:warning"
		case slices.Contains(errorWords, lv):
			color = "theme:error"
		default:
			continue
		}
		rules = append(rules, Rule{v, color})
	}
	if len(rules) == 0 {
		return nil
	}
	return append(rules, Rule{errorFamily, "theme:error"}, Rule{warningFamily, "theme:warning"})
}

var (
	statusName = regexp.MustCompile(`(?i)^(status|state|phase|health|level|severity|result|ready|condition|outcome|conclusion)$`)
	// timeName is a whole word or a snake_case suffix, any case;
	// timeSuffix a camelCase one, so "format" isn't read as …At.
	timeName    = regexp.MustCompile(`(?i)^@?(time|timestamp|ts|date|datetime|created|updated|modified|deleted|started|finished|expires|seen)$|(?i)_(at|on|time|timestamp|date|ts)$`)
	timeSuffix  = regexp.MustCompile(`[a-z0-9](At|On|Time|Timestamp|Date|Seen)$`)
	checkName   = regexp.MustCompile(`(?i)^(is_?)?(ready|healthy|ok|enabled|active|available|passing|up|online|valid)$`)
	failureName = regexp.MustCompile(`(?i)^(restarts?|restart_?count|errors?|error_?count|failures?|failure_?count|failed|retries|retry_?count)$`)
	longName    = regexp.MustCompile(`(?i)^(description|body|notes?|message|msg|comment|summary|details?|text)$`)
	identity    = []string{"name", "title", "id"}
)

// IsStatusName reads a field name as a status: status, state, phase…
func IsStatusName(name string) bool { return statusName.MatchString(name) }

// IsTimeName reads a field name as a timestamp: created_at, updatedAt,
// creationTimestamp, ts…
func IsTimeName(name string) bool { return timeName.MatchString(name) || timeSuffix.MatchString(name) }

// IsCheckName reads a boolean's name as a check worth colouring: ready,
// healthy, ok, enabled…
func IsCheckName(name string) bool { return checkName.MatchString(name) }

// IsFailureCount reads a number's name as a count of failures:
// restarts, errors, failures…
func IsFailureCount(name string) bool { return failureName.MatchString(name) }

// IsLongName reads a field name as long text: description, message…
func IsLongName(name string) bool { return longName.MatchString(name) }

// IdentityNames are the field names an identity is looked for under,
// in order, after any caller-specific choice.
func IdentityNames() []string { return slices.Clone(identity) }

func lastSegment(path string) string { return path[strings.LastIndex(path, ".")+1:] }
