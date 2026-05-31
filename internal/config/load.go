package config

import (
	"fmt"
	"os"
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
	}
	for name, comp := range c.Components {
		if comp == nil {
			return fmt.Errorf("components.%s: empty definition", name)
		}
		if err := comp.validate("components." + name); err != nil {
			return err
		}
		if comp.Source != "" {
			if _, ok := c.DataSources[comp.Source]; !ok {
				return fmt.Errorf("components.%s: source %q not defined in data_sources", name, comp.Source)
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
				if col.Value == "" {
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

func (d *DataSource) validate(path string) error {
	switch d.Type {
	case "http":
		if d.URL == "" {
			return fmt.Errorf("%s: http source needs url", path)
		}
	case "":
		return fmt.Errorf("%s: missing type", path)
	default:
		return fmt.Errorf("%s: unknown source type %q (want http)", path, d.Type)
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
