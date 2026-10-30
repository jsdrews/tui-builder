package gen

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// overrides is an --overrides file: gen: hints, keyed by the spec's
// own names so they survive regeneration, and a config fragment merged
// onto the output, keyed by the generated names.
type overrides struct {
	path     string
	gen      genHints
	fragment *yaml.Node // the file's mapping without gen:, or nil
	// ops are the operation hints by the operation they name.
	ops map[string]*opHints // by "METHOD /path"
}

// genHints are the gen: block. Every global flag but --header has a key;
// a flag given on the command line wins.
type genHints struct {
	Name              string              `yaml:"name"`
	Layout            string              `yaml:"layout"`
	BaseURL           string              `yaml:"base_url"`
	PromptServer      bool                `yaml:"prompt_server"`
	Security          string              `yaml:"security"`
	Timeout           string              `yaml:"timeout"`
	Tag               []string            `yaml:"tag"`
	ExcludeTag        []string            `yaml:"exclude_tag"`
	Operation         []string            `yaml:"operation"`
	ExcludeOperation  []string            `yaml:"exclude_operation"`
	ReadOnly          bool                `yaml:"read_only"`
	IncludeDeprecated bool                `yaml:"include_deprecated"`
	Order             []string            `yaml:"order"`
	Operations        map[string]*opHints `yaml:"operations"`
}

// opHints change what one operation generates.
type opHints struct {
	Title      string     `yaml:"title"`
	Group      string     `yaml:"group"`
	Hide       bool       `yaml:"hide"`
	Skip       bool       `yaml:"skip"`
	Root       string     `yaml:"root"`
	MaxPages   int        `yaml:"max_pages"`
	Columns    []string   `yaml:"columns"`
	Window     yaml.Node  `yaml:"window"`
	SortRefuse []string   `yaml:"sort_refuse"`
	Anchored   yaml.Node  `yaml:"anchored"`
	Confirm    yaml.Node  `yaml:"confirm"`
	// key is how the file named it; op is the operation it resolved to.
	key  string
	op   op
	used bool
}

var (
	hintKeys   = yamlKeys(genHints{})
	opHintKeys = yamlKeys(opHints{})
	// fragmentKeys are the config's own top-level keys.
	fragmentKeys = []string{"app", "data", "actions", "tui"}
)

func yamlKeys(v any) []string {
	var out []string
	b, _ := yaml.Marshal(v)
	var m map[string]any
	yaml.Unmarshal(b, &m)
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loadOverrides reads and checks an --overrides file. Unknown keys are
// errors with the nearest key suggested.
func loadOverrides(path string) (*overrides, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ov := &overrides{path: path}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return ov, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: an overrides file is a mapping: gen: hints and config keys", path)
	}
	frag := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value == "gen" {
			if err := checkKeys(path, "gen", v, hintKeys); err != nil {
				return nil, err
			}
			if ops := get(v, "operations"); ops != nil && ops.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(ops.Content); j += 2 {
					if err := checkKeys(path, "gen.operations."+ops.Content[j].Value, ops.Content[j+1], opHintKeys); err != nil {
						return nil, err
					}
				}
			}
			if err := v.Decode(&ov.gen); err != nil {
				return nil, fmt.Errorf("%s: gen: %w", path, err)
			}
			continue
		}
		if !slices.Contains(fragmentKeys, k.Value) {
			return nil, fmt.Errorf("%s:%d: %q isn't gen: or a config key (%s)%s", path, k.Line, k.Value, strings.Join(fragmentKeys, ", "), suggest(k.Value, append([]string{"gen"}, fragmentKeys...)))
		}
		frag.Content = append(frag.Content, k, v)
	}
	if len(frag.Content) > 0 {
		ov.fragment = frag
	}
	for key, h := range ov.gen.Operations {
		if h == nil {
			h = &opHints{}
			ov.gen.Operations[key] = h
		}
		h.key = key
		if h.Skip && (h.Title != "" || h.Group != "" || h.Hide || h.Root != "" || h.MaxPages != 0 || len(h.Columns) > 0 ||
			isSet(h.Window) || len(h.SortRefuse) > 0 || isSet(h.Anchored) || isSet(h.Confirm)) {
			return nil, fmt.Errorf("%s: gen.operations.%s: skip leaves nothing for its other hints to change", path, key)
		}
	}
	return ov, nil
}

// checkKeys refuses keys a block doesn't have.
func checkKeys(path, where string, n *yaml.Node, allowed []string) error {
	if n.Kind != yaml.MappingNode {
		if n.Tag == "!!null" {
			return nil
		}
		return fmt.Errorf("%s:%d: %s should be a mapping", path, n.Line, where)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if !slices.Contains(allowed, k.Value) {
			return fmt.Errorf("%s:%d: %s has no %q%s", path, k.Line, where, k.Value, suggest(k.Value, allowed))
		}
	}
	return nil
}

// suggest is " (did you mean X?)" for the nearest of choices, or "".
func suggest(got string, choices []string) string {
	best, dist := "", 1<<30
	for _, c := range choices {
		if d := levenshtein(strings.ToLower(got), strings.ToLower(c)); d < dist {
			best, dist = c, d
		}
	}
	if best == "" || dist > max(2, len(got)/3) {
		return ""
	}
	return fmt.Sprintf(" (did you mean %s?)", best)
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// applyGlobals sets the options the file names, where no flag did.
func (ov *overrides) applyGlobals(opts *Options) error {
	g := ov.gen
	set := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	set(&opts.Doc.Name, g.Name)
	set(&opts.Layout, g.Layout)
	set(&opts.Doc.BaseURL, g.BaseURL)
	set(&opts.Doc.Security, g.Security)
	opts.Doc.PromptServer = opts.Doc.PromptServer || g.PromptServer
	opts.Select.ReadOnly = opts.Select.ReadOnly || g.ReadOnly
	opts.Select.IncludeDeprecated = opts.Select.IncludeDeprecated || g.IncludeDeprecated
	lists := []struct {
		dst *[]string
		v   []string
	}{{&opts.Select.Tags, g.Tag}, {&opts.Select.ExcludeTags, g.ExcludeTag}, {&opts.Select.Operations, g.Operation}, {&opts.Select.ExcludeOperations, g.ExcludeOperation}}
	for _, l := range lists {
		if len(*l.dst) == 0 {
			*l.dst = l.v
		}
	}
	if g.Timeout != "" && opts.Input.Timeout == DefaultTimeout {
		d, err := parseDuration(g.Timeout)
		if err != nil {
			return fmt.Errorf("%s: gen.timeout: %w", ov.path, err)
		}
		opts.Input.Timeout = d
	}
	return nil
}

// resolve matches each operation hint to the spec: by "METHOD /path" or
// operationId. A hint naming nothing is an error, or with prune a
// warning.
func (ov *overrides) resolve(ops []op, prune bool) ([]string, error) {
	byKey := map[string]op{}
	var keys []string
	for _, o := range ops {
		byKey[o.String()] = o
		keys = append(keys, o.String())
		if id := o.op.OperationID; id != "" {
			byKey[id] = o
			keys = append(keys, id)
		}
	}
	ov.ops = map[string]*opHints{}
	var warnings []string
	for _, key := range sortedKeys(ov.gen.Operations) {
		h := ov.gen.Operations[key]
		o, ok := byKey[normalOpKey(key)]
		if !ok {
			msg := fmt.Sprintf("%s: gen.operations.%s: the spec has no such operation%s", ov.path, key, suggest(key, keys))
			if !prune {
				return nil, fmt.Errorf("%s (pass --prune-overrides to go on without it)", msg)
			}
			warnings = append(warnings, msg)
			continue
		}
		if other := ov.ops[o.String()]; other != nil {
			return nil, fmt.Errorf("%s: gen.operations: %s and %s are the same operation", ov.path, other.key, key)
		}
		h.op = o
		ov.ops[o.String()] = h
	}
	return warnings, nil
}

// normalOpKey upper-cases a "get /things" key's method.
func normalOpKey(key string) string {
	if m, path, ok := strings.Cut(key, " "); ok && strings.HasPrefix(path, "/") {
		return strings.ToUpper(m) + " " + path
	}
	return key
}

// hint is the operation's hints, or an empty set.
func (ov *overrides) hint(o op) *opHints {
	if ov != nil && ov.ops != nil {
		if h := ov.ops[o.String()]; h != nil {
			return h
		}
	}
	return &opHints{}
}

// unused warns about hints for operations nothing was generated from:
// they're left out by --tag or the like, or skipped for a reason no hint
// changes.
func (ov *overrides) unused() []string {
	var out []string
	for _, key := range sortedKeys(ov.gen.Operations) {
		if h := ov.gen.Operations[key]; h.op.method != "" && !h.used {
			out = append(out, fmt.Sprintf("%s: gen.operations.%s: nothing was generated from it, so its hints do nothing", ov.path, key))
		}
	}
	return out
}

// conflicts refuses a fragment that sets what a gen: hint set: one of
// them would quietly win.
func conflicts(file string, hinted, touched [][]string) error {
	under := func(a, b []string) bool { // a is b or inside it
		return len(a) >= len(b) && slices.Equal(a[:len(b)], b)
	}
	for _, h := range hinted {
		for _, t := range touched {
			if under(t, h) || under(h, t) {
				return fmt.Errorf("%s: %s is set by a gen: hint and by the config too; keep one", file, strings.Join(t, "."))
			}
		}
	}
	return nil
}

func parseDuration(s string) (time.Duration, error) { return time.ParseDuration(s) }

// resolveOrder turns gen.order's keys into "METHOD /path".
func (ov *overrides) resolveOrder(ops []op) ([]string, error) {
	var out []string
	for _, key := range ov.gen.Order {
		k := normalOpKey(key)
		i := slices.IndexFunc(ops, func(o op) bool { return o.String() == k || o.op.OperationID == key })
		if i < 0 {
			var keys []string
			for _, o := range ops {
				keys = append(keys, o.String())
			}
			return nil, fmt.Errorf("%s: gen.order: the spec has no %s%s", ov.path, key, suggest(key, keys))
		}
		out = append(out, ops[i].String())
	}
	return out, nil
}

// isSet is whether a yaml.Node hint was given.
func isSet(n yaml.Node) bool { return n.Kind != 0 }
