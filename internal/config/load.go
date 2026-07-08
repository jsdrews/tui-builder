package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads a YAML file at path and returns a validated Config.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Desugar `pipe:` chains before validation runs.
	if err := ExpandSourcesPipe(c.Data.Sources); err != nil {
		return nil, fmt.Errorf("expand %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	// Env-var check runs after Validate so schema errors surface
	// first. Applies defaults, errors on missing required vars,
	// warns on undeclared-and-unset references.
	if err := checkEnv(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Validate walks the config and returns the first structural error found.
// Per-entry checks delegate to each Source's Validate method; cross-
// entry checks (upstream resolution, cycle detection, join-lookup
// constraints, component bindings) walk the unified Sources map.
func (c *Config) Validate() error {
	// 1. Per-entry structural validation.
	for name, s := range c.Data.Sources {
		if s == nil {
			return fmt.Errorf("data.sources.%s: empty entry", name)
		}
		if err := s.Validate("data.sources." + name); err != nil {
			return err
		}
	}
	// 2. Upstream resolution + cycle detection across the source graph.
	if err := validateSourceUpstreams(c.Data.Sources); err != nil {
		return err
	}
	// 3. Join lookups must reference leaf entries that declare parameters
	//    (so per-row BindParams has something to bind to).
	if err := validateJoinLookups(c.Data.Sources); err != nil {
		return err
	}
	// 4. Component bindings: `source:` must name an entry.
	for name, comp := range c.TUI.Components {
		if comp == nil {
			return fmt.Errorf("tui.components.%s: empty definition", name)
		}
		if err := comp.validate("tui.components." + name); err != nil {
			return err
		}
		if ref := comp.Source; ref != "" {
			if _, ok := c.Data.Sources[ref]; !ok {
				return fmt.Errorf("tui.components.%s: source %q not defined in data.sources", name, ref)
			}
		}
	}

	// Mode check: exactly one of screen / screens.
	hasSingle := c.TUI.Screen.Layout.set() > 0
	hasMulti := len(c.TUI.Screens) > 0
	switch {
	case hasSingle && hasMulti:
		return fmt.Errorf("config: set either `tui.screen:` (single) or `tui.screens:` (multi) — not both")
	case !hasSingle && !hasMulti:
		return fmt.Errorf("config: must set `tui.screen:` (single) or `tui.screens:` + `tui.initial:` (multi)")
	}

	if hasSingle {
		refs := map[string]int{}
		if err := c.TUI.Screen.Layout.validate("tui.screen.layout", c.TUI.Components, refs); err != nil {
			return err
		}
		for name, n := range refs {
			if n > 1 {
				return fmt.Errorf("tui.components.%s: referenced %d times in tui.screen.layout — each component may be placed only once per screen", name, n)
			}
		}
		if err := validateActions(c.TUI.Screen.Actions, refs, c.TUI.Components, "tui.screen"); err != nil {
			return err
		}
		return nil
	}

	// Multi-screen.
	if c.TUI.Initial == "" {
		return fmt.Errorf("config: `tui.initial:` is required when `tui.screens:` is set")
	}
	if _, ok := c.TUI.Screens[c.TUI.Initial]; !ok {
		return fmt.Errorf("config: initial screen %q not defined in tui.screens map", c.TUI.Initial)
	}
	for name, s := range c.TUI.Screens {
		if s == nil {
			return fmt.Errorf("tui.screens.%s: empty definition", name)
		}
		refs := map[string]int{}
		if err := s.Layout.validate("tui.screens."+name+".layout", c.TUI.Components, refs); err != nil {
			return err
		}
		for cname, n := range refs {
			if n > 1 {
				return fmt.Errorf("tui.screens.%s: components.%s referenced %d times — each component may be placed only once per screen", name, cname, n)
			}
		}
		for i, b := range s.OnEnter {
			if b.Source == "" || b.Push == "" {
				return fmt.Errorf("tui.screens.%s.on_enter[%d]: source and push are required", name, i)
			}
			if refs[b.Source] == 0 {
				return fmt.Errorf("tui.screens.%s.on_enter[%d]: source %q not used in this screen's layout", name, i, b.Source)
			}
			if _, ok := c.TUI.Screens[b.Push]; !ok {
				return fmt.Errorf("tui.screens.%s.on_enter[%d]: push %q not defined in screens map", name, i, b.Push)
			}
			src := c.TUI.Components[b.Source]
			if src.Type != "list" && src.Type != "table" {
				return fmt.Errorf("tui.screens.%s.on_enter[%d]: source %q must be a list or table (got %s)", name, i, b.Source, src.Type)
			}
		}
		if err := validateActions(s.Actions, refs, c.TUI.Components, fmt.Sprintf("tui.screens.%s", name)); err != nil {
			return err
		}
	}
	return nil
}

func validateActions(actions []Action, refs map[string]int, components map[string]*Component, path string) error {
	for i, a := range actions {
		if a.Key == "" {
			return fmt.Errorf("%s.actions[%d]: key is required", path, i)
		}
		if a.Source == "" {
			return fmt.Errorf("%s.actions[%d]: source is required", path, i)
		}
		if len(a.Run) == 0 {
			return fmt.Errorf("%s.actions[%d]: run is required (non-empty argv)", path, i)
		}
		if refs[a.Source] == 0 {
			return fmt.Errorf("%s.actions[%d]: source %q not used in this screen's layout", path, i, a.Source)
		}
		src := components[a.Source]
		if src.Type != "list" && src.Type != "table" {
			return fmt.Errorf("%s.actions[%d]: source %q must be a list or table (got %s)", path, i, a.Source, src.Type)
		}
		for j, p := range a.Prompts {
			if p.Key == "" {
				return fmt.Errorf("%s.actions[%d].prompts[%d]: key is required", path, i, j)
			}
			switch p.Type {
			case "", "text", "select", "confirm":
			default:
				return fmt.Errorf("%s.actions[%d].prompts[%d]: unknown type %q (want text|select|confirm)", path, i, j, p.Type)
			}
			if p.Type == "select" && len(p.Options) == 0 {
				return fmt.Errorf("%s.actions[%d].prompts[%d]: select prompt needs options", path, i, j)
			}
		}
	}
	return nil
}

// set returns the number of tagged-union fields set on a Node — used by
// the validator to detect empty / over-set nodes.
func (n *Node) set() int {
	set := 0
	if n.VStack != nil {
		set++
	}
	if n.HStack != nil {
		set++
	}
	if n.ZStack != nil {
		set++
	}
	if n.Component != "" {
		set++
	}
	return set
}

func (n *Node) validate(path string, components map[string]*Component, refs map[string]int) error {
	switch n.set() {
	case 0:
		return fmt.Errorf("%s: layout node must set one of vstack/hstack/zstack/component", path)
	case 1:
	default:
		return fmt.Errorf("%s: layout node must set exactly one of vstack/hstack/zstack/component", path)
	}

	switch {
	case n.VStack != nil:
		for i, it := range n.VStack {
			if err := it.Node.validate(fmt.Sprintf("%s.vstack[%d]", path, i), components, refs); err != nil {
				return err
			}
		}
	case n.HStack != nil:
		for i, it := range n.HStack {
			if err := it.Node.validate(fmt.Sprintf("%s.hstack[%d]", path, i), components, refs); err != nil {
				return err
			}
		}
	case n.ZStack != nil:
		if err := n.ZStack.Base.validate(path+".zstack.base", components, refs); err != nil {
			return err
		}
		if err := n.ZStack.Overlay.validate(path+".zstack.overlay", components, refs); err != nil {
			return err
		}
	case n.Component != "":
		if _, ok := components[n.Component]; !ok {
			return fmt.Errorf("%s: component %q not defined in components map", path, n.Component)
		}
		refs[n.Component]++
	}
	return nil
}

func (c *Component) validate(path string) error {
	switch c.Type {
	case "list", "table", "logview", "tree", "inspector":
	case "":
		return fmt.Errorf("%s: missing type", path)
	default:
		return fmt.Errorf("%s: unknown component type %q (want list|table|logview|tree|inspector)", path, c.Type)
	}
	if c.Type == "table" {
		if len(c.Columns) == 0 {
			return fmt.Errorf("%s: table needs columns", path)
		}
		for i, col := range c.Columns {
			switch col.Sort {
			case "", "string", "number", "si":
			default:
				return fmt.Errorf("%s.columns[%d]: unknown sort %q (want string|number|si)", path, i, col.Sort)
			}
			for j, r := range col.ColorRules {
				if r.Color == "" {
					return fmt.Errorf("%s.columns[%d].color_rules[%d]: color is required", path, i, j)
				}
			}
		}
	}
	if c.Type == "tree" && c.Root == nil && c.Source == "" {
		return fmt.Errorf("%s: tree needs root", path)
	}
	if c.Auto {
		if c.Type != "inspector" {
			return fmt.Errorf("%s: `auto: true` is only valid on inspector components (got %q)", path, c.Type)
		}
		if c.Source == "" {
			return fmt.Errorf("%s: inspector `auto: true` needs a `source:` — nothing to derive fields from otherwise", path)
		}
	}
	// Data-source-bound components: enforce per-shape mapping fields.
	if c.Source != "" {
		switch c.Type {
		case "list":
			if c.Item == "" {
				return fmt.Errorf("%s: list bound to source %q needs `item:` (dot-path to display string)", path, c.Source)
			}
		case "table":
			for i, col := range c.Columns {
				if len(col.Value) == 0 || col.Value[0] == "" {
					return fmt.Errorf("%s.columns[%d]: table bound to source %q needs `value:` (dot-path) on every column", path, i, c.Source)
				}
			}
		case "inspector":
			// Path fields validated lazily — empty path keeps the static Value.
			// Auto and Fields are mutex: user is either declaring the record
			// shape or asking for auto-derive from whatever comes back.
			if c.Auto && len(c.Fields) > 0 {
				return fmt.Errorf("%s: inspector `auto: true` and declared `fields:` are mutually exclusive — pick one", path)
			}
		case "logview":
			// logview takes whatever the source returns: format:text bodies
			// are split on \n; JSON []string is used as-is. No per-line
			// mapping field needed.
		case "tree":
			if len(c.Label) == 0 || c.Label[0] == "" {
				return fmt.Errorf("%s: tree bound to source %q needs `label:` (dot-path to each leaf's display label)", path, c.Source)
			}
		}
	}
	return nil
}

// validateSourceUpstreams walks every source's Upstreams() and
// checks each reference resolves to a defined entry, then runs a DFS
// cycle detector across the unified graph. Join lookup references
// count as edges too — they don't form a build-time dep (joinSource
// re-fetches per row), but they DO form a name-resolution dep, and
// since lookups must be leaves (validateJoinLookups enforces) the
// cycle walk terminates correctly without false positives.
func validateSourceUpstreams(sources map[string]*Source) error {
	for name, s := range sources {
		for _, up := range s.Upstreams() {
			if _, ok := sources[up]; !ok {
				return fmt.Errorf("data.sources.%s: upstream %q not defined in data.sources", name, up)
			}
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(sources))
	var dfs func(name string, stack []string) error
	dfs = func(name string, stack []string) error {
		switch color[name] {
		case gray:
			return fmt.Errorf("data.sources: cyclic reference: %v -> %s", stack, name)
		case black:
			return nil
		}
		color[name] = gray
		stack = append(stack, name)
		if s := sources[name]; s != nil {
			for _, up := range s.Upstreams() {
				if err := dfs(up, stack); err != nil {
					return err
				}
			}
		}
		color[name] = black
		return nil
	}
	for name := range sources {
		if err := dfs(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// validateJoinLookups enforces the constraint that every join's
// lookup references a leaf entry with declared parameters. Joins
// re-invoke lookups per driver row via BindParams; that pattern only
// works against leaf sources (operators don't carry templated
// fields) and requires the lookup to declare what params it accepts.
func validateJoinLookups(sources map[string]*Source) error {
	for name, s := range sources {
		if s.Type != "join" {
			continue
		}
		for lname, look := range s.Lookups {
			target, ok := sources[look.From]
			if !ok {
				return fmt.Errorf("data.sources.%s.join.lookups.%s: `from: %q` is not a defined entry", name, lname, look.From)
			}
			if !target.IsLeaf() {
				return fmt.Errorf("data.sources.%s.join.lookups.%s: `from: %q` is not a leaf source (pipelines aren't supported as lookups yet)", name, lname, look.From)
			}
			if len(target.Parameters) == 0 {
				return fmt.Errorf("data.sources.%s.join.lookups.%s: source %q must declare `parameters:` so the join can bind per-row values to it", name, lname, look.From)
			}
			for paramName := range look.On {
				if _, ok := target.Parameters[paramName]; !ok {
					return fmt.Errorf("data.sources.%s.join.lookups.%s.on.%s: source %q has no parameter %q", name, lname, paramName, look.From, paramName)
				}
			}
		}
	}
	return nil
}

// validateParametersMap is the shared schema validator for any
// parameter declaration block (DataSource, Pipeline). Mutual
// exclusion rules (required + default) and the type whitelist are
// the same for every consumer; extracting the body keeps the rules
// in one place.
func validateParametersMap(params map[string]*Parameter, path string) error {
	for name, p := range params {
		if p == nil {
			return fmt.Errorf("%s.parameters.%s: empty definition", path, name)
		}
		switch p.Type {
		case "", "string", "int", "bool", "duration":
		default:
			return fmt.Errorf("%s.parameters.%s: unknown type %q (want string|int|bool|duration)", path, name, p.Type)
		}
		if p.Required && p.Default != "" {
			return fmt.Errorf("%s.parameters.%s: `required: true` and `default:` are mutually exclusive — defaults imply optional", path, name)
		}
	}
	return nil
}

// ResolveParams applies the standard resolution rules — declared
// default → caller value → error if required — and returns a
// resolved name → value map. Extra keys in `supplied` that aren't in
// `declared` are rejected so silent typos don't hide bugs.
//
// Used by cfg.BindLeafParams (which then substitutes ${params.X}
// into leaf-source URL/Command/etc. templates) and the pipeline
// layer (which makes the resolved values available as `params.X` in
// operator
// expressions). One resolver, two consumers.
func ResolveParams(declared map[string]*Parameter, supplied map[string]string) (map[string]string, error) {
	for k := range supplied {
		if _, ok := declared[k]; !ok {
			return nil, fmt.Errorf("parameter %q not declared", k)
		}
	}
	resolved := make(map[string]string, len(declared))
	for name, spec := range declared {
		if v, ok := supplied[name]; ok {
			resolved[name] = v
			continue
		}
		if spec.Default != "" {
			resolved[name] = spec.Default
			continue
		}
		if spec.Required {
			return nil, fmt.Errorf("required parameter %q not provided", name)
		}
		resolved[name] = ""
	}
	return resolved, nil
}

// BindParams resolves caller-supplied parameter values against the
// source's declared parameter schema (via ResolveParams) and
// substitutes ${params.<name>} tokens in every templated string
// field (URL, Body, Headers, Command, Env, Path, InitialMessages).
//
// Mutates the receiver in place; callers that want to reuse the
// undecorated source should pass a copy.

// substituteParams replaces ${params.NAME} tokens with their resolved
// values. Tokens for params not in the map are left as-is so the
// source author can spot the typo at fetch time (404 / connection
// failure) rather than silently turning into an empty string.
func substituteParams(s string, params map[string]string) string {
	for name, val := range params {
		s = replaceAll(s, "${params."+name+"}", val)
	}
	return s
}

// replaceAll is a tiny strings.ReplaceAll alias kept local so the
// package's dependency surface stays narrow. (strings is already
// imported via fmt; keeping this here avoids a one-shot import.)
func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func paramKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
