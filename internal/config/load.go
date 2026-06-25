package config

import (
	"fmt"
	"os"
	"strings"
	"time"

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
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return &c, nil
}

// Validate walks the config and returns the first structural error found.
// Component definitions are validated; layout references are checked to
// resolve and to be referenced exactly once per screen.
func (c *Config) Validate() error {
	for name, src := range c.DataSources {
		if src == nil {
			return fmt.Errorf("data_sources.%s: empty definition", name)
		}
		if err := src.validate("data_sources." + name); err != nil {
			return err
		}
		if src.Type == "merge" {
			for _, child := range src.Sources {
				if _, ok := c.DataSources[child]; !ok {
					return fmt.Errorf("data_sources.%s: merge child %q not defined in data_sources", name, child)
				}
			}
			for i, child := range src.Children {
				if child.Source == "" {
					return fmt.Errorf("data_sources.%s.children[%d]: source is required", name, i)
				}
				if _, ok := c.DataSources[child.Source]; !ok {
					return fmt.Errorf("data_sources.%s.children[%d]: source %q not defined in data_sources", name, i, child.Source)
				}
			}
		}
	}
	if err := c.checkMergeCycles(); err != nil {
		return err
	}
	if err := c.validatePipelines(); err != nil {
		return err
	}
	for name, comp := range c.Components {
		if comp == nil {
			return fmt.Errorf("components.%s: empty definition", name)
		}
		if err := comp.validate("components." + name); err != nil {
			return err
		}
		if comp.Source != "" && comp.Pipeline != "" {
			return fmt.Errorf("components.%s: set either `source:` or `pipeline:`, not both", name)
		}
		if comp.Source != "" {
			if _, ok := c.DataSources[comp.Source]; !ok {
				return fmt.Errorf("components.%s: source %q not defined in data_sources", name, comp.Source)
			}
		}
		if comp.Pipeline != "" {
			if _, ok := c.Pipelines[comp.Pipeline]; !ok {
				return fmt.Errorf("components.%s: pipeline %q not defined in pipelines", name, comp.Pipeline)
			}
		}
	}

	// Mode check: exactly one of screen / screens.
	hasSingle := c.Screen.Layout.set() > 0
	hasMulti := len(c.Screens) > 0
	switch {
	case hasSingle && hasMulti:
		return fmt.Errorf("config: set either `screen:` (single) or `screens:` (multi) — not both")
	case !hasSingle && !hasMulti:
		return fmt.Errorf("config: must set `screen:` (single) or `screens:` + `initial:` (multi)")
	}

	if hasSingle {
		refs := map[string]int{}
		if err := c.Screen.Layout.validate("screen.layout", c.Components, refs); err != nil {
			return err
		}
		for name, n := range refs {
			if n > 1 {
				return fmt.Errorf("components.%s: referenced %d times in screen.layout — each component may be placed only once per screen", name, n)
			}
		}
		if err := validateActions(c.Screen.Actions, refs, c.Components, "screen"); err != nil {
			return err
		}
		return nil
	}

	// Multi-screen.
	if c.Initial == "" {
		return fmt.Errorf("config: `initial:` is required when `screens:` is set")
	}
	if _, ok := c.Screens[c.Initial]; !ok {
		return fmt.Errorf("config: initial screen %q not defined in screens map", c.Initial)
	}
	for name, s := range c.Screens {
		if s == nil {
			return fmt.Errorf("screens.%s: empty definition", name)
		}
		refs := map[string]int{}
		if err := s.Layout.validate("screens."+name+".layout", c.Components, refs); err != nil {
			return err
		}
		for cname, n := range refs {
			if n > 1 {
				return fmt.Errorf("screens.%s: components.%s referenced %d times — each component may be placed only once per screen", name, cname, n)
			}
		}
		for i, b := range s.OnEnter {
			if b.Source == "" || b.Push == "" {
				return fmt.Errorf("screens.%s.on_enter[%d]: source and push are required", name, i)
			}
			if refs[b.Source] == 0 {
				return fmt.Errorf("screens.%s.on_enter[%d]: source %q not used in this screen's layout", name, i, b.Source)
			}
			if _, ok := c.Screens[b.Push]; !ok {
				return fmt.Errorf("screens.%s.on_enter[%d]: push %q not defined in screens map", name, i, b.Push)
			}
			src := c.Components[b.Source]
			if src.Type != "list" && src.Type != "table" {
				return fmt.Errorf("screens.%s.on_enter[%d]: source %q must be a list or table (got %s)", name, i, b.Source, src.Type)
			}
		}
		if err := validateActions(s.Actions, refs, c.Components, fmt.Sprintf("screens.%s", name)); err != nil {
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
		case "logview":
			// logview takes whatever the source returns: format:text bodies
			// are split on \n; JSON []string is used as-is. No per-line
			// mapping field needed.
		case "tree":
			return fmt.Errorf("%s: source binding not yet supported for %s", path, c.Type)
		}
	}
	return nil
}

// validatePipelines checks that every pipeline declares exactly one
// operator (passthrough `from:`, `filter:`, etc.), that all upstream
// references resolve to a defined source or pipeline, and that the
// reference graph is acyclic. Mutual recursion between pipelines
// would otherwise cause infinite delegation at Fetch time.
func (c *Config) validatePipelines() error {
	for name, p := range c.Pipelines {
		if p == nil {
			return fmt.Errorf("pipelines.%s: empty definition", name)
		}
		// Validate any declared parameters — same rules as data
		// source parameters (mutual exclusion of required+default,
		// known type). Reusing the source param validator keeps the
		// two surfaces consistent.
		if err := validateParametersMap(p.Parameters, fmt.Sprintf("pipelines.%s", name)); err != nil {
			return err
		}
		// Exactly one operator must be set. As operators are added,
		// extend the count and the union list. The validator catches
		// "no operator set" and "multiple operators set" symmetrically.
		set := 0
		if p.From != "" {
			set++
		}
		if p.Filter != nil {
			set++
		}
		if p.Project != nil {
			set++
		}
		if p.Derive != nil {
			set++
		}
		if p.Sort != nil {
			set++
		}
		if p.Union != nil {
			set++
		}
		if p.Compose != nil {
			set++
		}
		if p.Join != nil {
			set++
		}
		if p.Cache != nil {
			set++
		}
		switch set {
		case 0:
			return fmt.Errorf("pipelines.%s: no operator set; choose one of `from:` (passthrough), `filter:`, `project:`, `derive:`, `sort:`, `union:`, `compose:`, `join:`, `cache:`", name)
		case 1:
			// ok
		default:
			return fmt.Errorf("pipelines.%s: multiple operators set; pick exactly one", name)
		}

		// Per-operator validation + upstream resolution.
		upstream := ""
		switch {
		case p.From != "":
			upstream = p.From
		case p.Filter != nil:
			if p.Filter.From == "" {
				return fmt.Errorf("pipelines.%s.filter: `from:` is required", name)
			}
			if strings.TrimSpace(p.Filter.Where) == "" {
				return fmt.Errorf("pipelines.%s.filter: `where:` is required (predicate expression)", name)
			}
			upstream = p.Filter.From
		case p.Project != nil:
			if p.Project.From == "" {
				return fmt.Errorf("pipelines.%s.project: `from:` is required", name)
			}
			if len(p.Project.Keep) == 0 {
				return fmt.Errorf("pipelines.%s.project: `keep:` is required (non-empty map of output_name -> expression)", name)
			}
			for k, v := range p.Project.Keep {
				if k == "" {
					return fmt.Errorf("pipelines.%s.project.keep: empty output key", name)
				}
				if strings.TrimSpace(v) == "" {
					return fmt.Errorf("pipelines.%s.project.keep.%s: empty expression", name, k)
				}
			}
			upstream = p.Project.From
		case p.Derive != nil:
			if p.Derive.From == "" {
				return fmt.Errorf("pipelines.%s.derive: `from:` is required", name)
			}
			if len(p.Derive.Compute) == 0 {
				return fmt.Errorf("pipelines.%s.derive: `compute:` is required (non-empty map of output_name -> expression)", name)
			}
			for k, v := range p.Derive.Compute {
				if k == "" {
					return fmt.Errorf("pipelines.%s.derive.compute: empty output key", name)
				}
				if strings.TrimSpace(v) == "" {
					return fmt.Errorf("pipelines.%s.derive.compute.%s: empty expression", name, k)
				}
			}
			upstream = p.Derive.From
		case p.Sort != nil:
			if p.Sort.From == "" {
				return fmt.Errorf("pipelines.%s.sort: `from:` is required", name)
			}
			if strings.TrimSpace(p.Sort.By) == "" {
				return fmt.Errorf("pipelines.%s.sort: `by:` is required (key expression)", name)
			}
			switch strings.ToLower(p.Sort.Order) {
			case "", "asc", "desc":
			default:
				return fmt.Errorf("pipelines.%s.sort.order: unknown %q (want asc|desc)", name, p.Sort.Order)
			}
			upstream = p.Sort.From
		case p.Union != nil:
			hasSources := len(p.Union.Sources) > 0
			hasChildren := len(p.Union.Children) > 0
			switch {
			case hasSources && hasChildren:
				return fmt.Errorf("pipelines.%s.union: set either `sources:` (shorthand) or `children:` (per-child tags), not both", name)
			case !hasSources && !hasChildren:
				return fmt.Errorf("pipelines.%s.union: needs `sources:` or `children:`", name)
			case hasChildren && p.Union.TagField != "":
				return fmt.Errorf("pipelines.%s.union: `tag_field:` is only valid with `sources:`; with `children:` each child declares its own `tags:`", name)
			}
			switch p.Union.OnError {
			case "", "fail", "skip":
			default:
				return fmt.Errorf("pipelines.%s.union: unknown on_error %q (want fail|skip)", name, p.Union.OnError)
			}
			for i, ch := range p.Union.Children {
				if ch.Source == "" {
					return fmt.Errorf("pipelines.%s.union.children[%d]: source is required", name, i)
				}
			}
			// Union has multiple upstreams; validate each below.
		case p.Compose != nil:
			if len(p.Compose.Parts) == 0 {
				return fmt.Errorf("pipelines.%s.compose: `parts:` is required (non-empty map of output_key -> input_name)", name)
			}
			for k, v := range p.Compose.Parts {
				if k == "" {
					return fmt.Errorf("pipelines.%s.compose.parts: empty output key", name)
				}
				if v == "" {
					return fmt.Errorf("pipelines.%s.compose.parts.%s: empty input name", name, k)
				}
			}
			switch p.Compose.OnError {
			case "", "fail", "skip":
			default:
				return fmt.Errorf("pipelines.%s.compose: unknown on_error %q (want fail|skip)", name, p.Compose.OnError)
			}
		case p.Cache != nil:
			if p.Cache.From == "" {
				return fmt.Errorf("pipelines.%s.cache: `from:` is required", name)
			}
			if strings.TrimSpace(p.Cache.TTL) == "" {
				return fmt.Errorf("pipelines.%s.cache: `ttl:` is required (duration string, e.g. 30s)", name)
			}
			if _, err := time.ParseDuration(p.Cache.TTL); err != nil {
				return fmt.Errorf("pipelines.%s.cache: invalid ttl %q: %w", name, p.Cache.TTL, err)
			}
			upstream = p.Cache.From
		case p.Join != nil:
			if p.Join.Driver.From == "" {
				return fmt.Errorf("pipelines.%s.join.driver: `from:` is required", name)
			}
			if len(p.Join.Lookups) == 0 {
				return fmt.Errorf("pipelines.%s.join: at least one lookup is required", name)
			}
			for lname, look := range p.Join.Lookups {
				if lname == "" {
					return fmt.Errorf("pipelines.%s.join.lookups: empty lookup name", name)
				}
				if look.From == "" {
					return fmt.Errorf("pipelines.%s.join.lookups.%s: `from:` is required", name, lname)
				}
				// Lookup must reference a SOURCE (not a pipeline) with
				// declared parameters — joins re-invoke lookups per
				// driver row, which the source-level BindParams
				// supports but pipelines don't yet expose.
				src, ok := c.DataSources[look.From]
				if !ok {
					return fmt.Errorf("pipelines.%s.join.lookups.%s: `from: %q` is not a defined source (pipelines aren't supported as lookups yet)", name, lname, look.From)
				}
				if len(src.Parameters) == 0 {
					return fmt.Errorf("pipelines.%s.join.lookups.%s: source %q must declare `parameters:` so the join can bind per-row values to it", name, lname, look.From)
				}
				if len(look.On) == 0 {
					return fmt.Errorf("pipelines.%s.join.lookups.%s: `on:` is required (map of lookup param → expression against driver row)", name, lname)
				}
				for paramName, eexpr := range look.On {
					if paramName == "" {
						return fmt.Errorf("pipelines.%s.join.lookups.%s.on: empty param name", name, lname)
					}
					if _, ok := src.Parameters[paramName]; !ok {
						return fmt.Errorf("pipelines.%s.join.lookups.%s.on.%s: source %q has no parameter %q", name, lname, paramName, look.From, paramName)
					}
					if strings.TrimSpace(eexpr) == "" {
						return fmt.Errorf("pipelines.%s.join.lookups.%s.on.%s: empty expression", name, lname, paramName)
					}
				}
			}
			switch p.Join.Emit {
			case "", "separate", "merged":
			default:
				return fmt.Errorf("pipelines.%s.join: unknown emit %q (want separate|merged)", name, p.Join.Emit)
			}
			switch p.Join.OnError {
			case "", "fail", "skip":
			default:
				return fmt.Errorf("pipelines.%s.join: unknown on_error %q (want fail|skip)", name, p.Join.OnError)
			}
		}

		// Resolve every upstream the operator reads from. Most
		// operators have one; union has N. Centralised via upstreamsOf
		// so cycle detection (below) walks the same edges.
		for _, up := range upstreamsOf(p, upstream) {
			_, isSource := c.DataSources[up]
			_, isPipeline := c.Pipelines[up]
			if !isSource && !isPipeline {
				return fmt.Errorf("pipelines.%s: upstream %q not defined in data_sources or pipelines", name, up)
			}
		}
	}
	// Cycle check: walk pipeline → pipeline references via DFS coloring.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(c.Pipelines))
	var dfs func(name string, stack []string) error
	dfs = func(name string, stack []string) error {
		switch color[name] {
		case gray:
			return fmt.Errorf("pipelines: cyclic reference: %v -> %s", stack, name)
		case black:
			return nil
		}
		color[name] = gray
		stack = append(stack, name)
		if p := c.Pipelines[name]; p != nil {
			// Walk every upstream the operator reads from so cycle
			// detection covers all operator families (including the
			// multi-input ones like union).
			for _, up := range upstreamsOf(p, "") {
				if _, isPipe := c.Pipelines[up]; isPipe {
					if err := dfs(up, stack); err != nil {
						return err
					}
				}
			}
		}
		color[name] = black
		return nil
	}
	for name := range c.Pipelines {
		if err := dfs(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// UpstreamsOf is the exported alias for the internal upstreamsOf
// helper — callers outside this package (wrangl's --list lifecycle
// inference) need to walk pipeline → upstream edges with the same
// knowledge of every operator family. Pass "" for `single` to use
// the default single-upstream resolution path.
func UpstreamsOf(p *Pipeline) []string { return upstreamsOf(p, "") }

// upstreamsOf returns every source/pipeline an operator reads from.
// Most operators have a single upstream (returned as a one-element
// slice); union has N. The fallback `single` arg is the single-upstream
// case already computed in Validate so we don't recompute it.
//
// Defined here (not in internal/pipeline) so config-level cycle
// detection and validation can walk the same edges Build will resolve.
func upstreamsOf(p *Pipeline, single string) []string {
	if p == nil {
		return nil
	}
	if p.Union != nil {
		out := make([]string, 0, len(p.Union.Sources)+len(p.Union.Children))
		out = append(out, p.Union.Sources...)
		for _, ch := range p.Union.Children {
			out = append(out, ch.Source)
		}
		return out
	}
	if p.Compose != nil {
		out := make([]string, 0, len(p.Compose.Parts))
		for _, in := range p.Compose.Parts {
			out = append(out, in)
		}
		return out
	}
	if p.Join != nil {
		// Only the driver counts as an upstream for build / cycle
		// detection. Lookups are referenced by name but the joinSource
		// re-invokes them per row internally — it doesn't subscribe
		// to a built version through the registry.
		return []string{p.Join.Driver.From}
	}
	if p.Cache != nil {
		return []string{p.Cache.From}
	}
	if single != "" {
		return []string{single}
	}
	// Fall back to single-upstream resolution for the cycle-detection
	// callers that don't pre-compute it.
	switch {
	case p.From != "":
		return []string{p.From}
	case p.Filter != nil:
		return []string{p.Filter.From}
	case p.Project != nil:
		return []string{p.Project.From}
	case p.Derive != nil:
		return []string{p.Derive.From}
	case p.Sort != nil:
		return []string{p.Sort.From}
	}
	return nil
}

// checkMergeCycles walks the merge → children graph and rejects cycles
// (which would otherwise cause infinite recursion at Build time).
func (c *Config) checkMergeCycles() error {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(c.DataSources))
	var dfs func(name string, stack []string) error
	dfs = func(name string, stack []string) error {
		switch color[name] {
		case gray:
			return fmt.Errorf("data_sources: cyclic merge reference: %v -> %s",
				stack, name)
		case black:
			return nil
		}
		color[name] = gray
		stack = append(stack, name)
		def := c.DataSources[name]
		if def != nil && def.Type == "merge" {
			for _, child := range def.Sources {
				if err := dfs(child, stack); err != nil {
					return err
				}
			}
			for _, child := range def.Children {
				if err := dfs(child.Source, stack); err != nil {
					return err
				}
			}
		}
		color[name] = black
		return nil
	}
	for name := range c.DataSources {
		if err := dfs(name, nil); err != nil {
			return err
		}
	}
	return nil
}

func (d *DataSource) validate(path string) error {
	if err := d.validateParameters(path); err != nil {
		return err
	}
	switch d.Type {
	case "http":
		if d.URL == "" {
			return fmt.Errorf("%s: http source needs url", path)
		}
	case "exec":
		if len(d.Command) == 0 {
			return fmt.Errorf("%s: exec source needs command (non-empty argv)", path)
		}
	case "file":
		if d.Path == "" {
			return fmt.Errorf("%s: file source needs path", path)
		}
	case "merge":
		hasSources := len(d.Sources) > 0
		hasChildren := len(d.Children) > 0
		switch {
		case hasSources && hasChildren:
			return fmt.Errorf("%s: set either `sources:` (shorthand) or `children:` (per-child tags), not both", path)
		case !hasSources && !hasChildren:
			return fmt.Errorf("%s: merge source needs sources or children", path)
		case hasChildren && d.TagField != "":
			return fmt.Errorf("%s: `tag_field:` is only valid with `sources:`; with `children:` each child declares its own `tags:`", path)
		}
		switch d.OnError {
		case "", "fail", "skip":
		default:
			return fmt.Errorf("%s: unknown on_error %q (want fail|skip)", path, d.OnError)
		}
	case "websocket":
		if d.URL == "" {
			return fmt.Errorf("%s: websocket source needs url (ws:// or wss://)", path)
		}
	case "":
		return fmt.Errorf("%s: missing type", path)
	default:
		return fmt.Errorf("%s: unknown source type %q (want http|exec|file|merge|websocket)", path, d.Type)
	}
	switch d.Format {
	case "", "json", "text":
	default:
		return fmt.Errorf("%s: unknown format %q (want json|text)", path, d.Format)
	}
	if d.Refresh != "" {
		if _, err := time.ParseDuration(d.Refresh); err != nil {
			return fmt.Errorf("%s: invalid refresh %q: %w", path, d.Refresh, err)
		}
	}
	if d.Timeout != "" {
		if _, err := time.ParseDuration(d.Timeout); err != nil {
			return fmt.Errorf("%s: invalid timeout %q: %w", path, d.Timeout, err)
		}
	}
	return nil
}

// validateParameters enforces the per-parameter rules: known type,
// required+default mutual exclusion. The full lifecycle inference (is
// the source polled / streamed / on-demand?) happens later when params
// are bound — at config-load time we only check the schema is sane.
func (d *DataSource) validateParameters(path string) error {
	return validateParametersMap(d.Parameters, path)
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

// Clone returns a deep copy of d so callers can mutate the copy
// (typically via BindParams substitutions) without affecting the
// original config. Reference fields (Headers, Env, Command,
// Sources, Children, InitialMessages, Parameters) are duplicated;
// scalar fields are copied by the struct assignment.
//
// Used by the join operator, which builds per-driver-row instances
// of a parameterized lookup source — each row's lookup needs its
// own bound URL / argv / etc. without altering the cfg.DataSource
// other consumers share.
func (d *DataSource) Clone() *DataSource {
	if d == nil {
		return nil
	}
	out := *d
	if d.Headers != nil {
		out.Headers = make(map[string]string, len(d.Headers))
		for k, v := range d.Headers {
			out.Headers[k] = v
		}
	}
	if d.Env != nil {
		out.Env = make(map[string]string, len(d.Env))
		for k, v := range d.Env {
			out.Env[k] = v
		}
	}
	if d.Command != nil {
		out.Command = append([]string(nil), d.Command...)
	}
	if d.Sources != nil {
		out.Sources = append([]string(nil), d.Sources...)
	}
	if d.Children != nil {
		out.Children = append([]MergeChild(nil), d.Children...)
	}
	if d.InitialMessages != nil {
		out.InitialMessages = append([]string(nil), d.InitialMessages...)
	}
	// Parameters can stay aliased — callers never mutate the
	// declared schema; only resolved values flow through BindParams.
	return &out
}

// ResolveParams applies the standard resolution rules — declared
// default → caller value → error if required — and returns a
// resolved name → value map. Extra keys in `supplied` that aren't in
// `declared` are rejected so silent typos don't hide bugs.
//
// Used by both DataSource.BindParams (which then substitutes
// ${params.X} into URL templates) and the pipeline layer (which
// makes the resolved values available as `params.X` in operator
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
func (d *DataSource) BindParams(params map[string]string) error {
	if len(d.Parameters) == 0 {
		if len(params) > 0 {
			return fmt.Errorf("source has no `parameters:` declared (got: %v)", paramKeys(params))
		}
		return nil
	}
	resolved, err := ResolveParams(d.Parameters, params)
	if err != nil {
		return err
	}
	d.URL = substituteParams(d.URL, resolved)
	d.Body = substituteParams(d.Body, resolved)
	d.Method = substituteParams(d.Method, resolved)
	d.Path = substituteParams(d.Path, resolved)
	for k, v := range d.Headers {
		d.Headers[k] = substituteParams(v, resolved)
	}
	for i, c := range d.Command {
		d.Command[i] = substituteParams(c, resolved)
	}
	for k, v := range d.Env {
		d.Env[k] = substituteParams(v, resolved)
	}
	for i, m := range d.InitialMessages {
		d.InitialMessages[i] = substituteParams(m, resolved)
	}
	return nil
}

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
