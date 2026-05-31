package build

import (
	"fmt"

	"github.com/jsdrews/tuilib/pkg/layout"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Tree owns the live components and the layout config they're wired into.
// Components are keyed by the name used in the layout's `component:` refs;
// Order keeps an iteration order for fanout (Update/SetTheme/Help/etc.).
type Tree struct {
	Root       *cfg.Node
	Components map[string]*Component
	Order      []string // names, in the order encountered while walking Root
}

// Build instantiates every component defined in the config (lazily — only
// names referenced by the layout are built) and wires the layout tree.
func Build(root *cfg.Node, defs map[string]*cfg.Component, th theme.Theme) (*Tree, error) {
	t := &Tree{Root: root, Components: map[string]*Component{}}
	if err := t.walk(root, defs); err != nil {
		return nil, err
	}
	for _, name := range t.Order {
		comp := t.Components[name]
		built, err := NewComponent(comp.Cfg, th)
		if err != nil {
			return nil, fmt.Errorf("components.%s: %w", name, err)
		}
		*comp = *built
	}
	return t, nil
}

// All returns components in walk order — handy for screen-level fanout.
func (t *Tree) All() []*Component {
	out := make([]*Component, 0, len(t.Order))
	for _, name := range t.Order {
		out = append(out, t.Components[name])
	}
	return out
}

func (t *Tree) walk(n *cfg.Node, defs map[string]*cfg.Component) error {
	switch {
	case n.VStack != nil:
		for i := range n.VStack {
			if err := t.walk(&n.VStack[i].Node, defs); err != nil {
				return fmt.Errorf("vstack[%d]: %w", i, err)
			}
		}
	case n.HStack != nil:
		for i := range n.HStack {
			if err := t.walk(&n.HStack[i].Node, defs); err != nil {
				return fmt.Errorf("hstack[%d]: %w", i, err)
			}
		}
	case n.ZStack != nil:
		if err := t.walk(&n.ZStack.Base, defs); err != nil {
			return fmt.Errorf("zstack.base: %w", err)
		}
		if err := t.walk(&n.ZStack.Overlay, defs); err != nil {
			return fmt.Errorf("zstack.overlay: %w", err)
		}
	case n.Component != "":
		def, ok := defs[n.Component]
		if !ok {
			return fmt.Errorf("component %q not defined", n.Component)
		}
		t.Components[n.Component] = &Component{Cfg: def}
		t.Order = append(t.Order, n.Component)
	}
	return nil
}

// RenderNode walks the config tree producing a live layout.Node tree.
// Component leaves are resolved by name through Tree.Components.
func (t *Tree) RenderNode() layout.Node {
	var visit func(n *cfg.Node) layout.Node
	visit = func(n *cfg.Node) layout.Node {
		switch {
		case n.VStack != nil:
			items := make([]layout.Item, len(n.VStack))
			for i, it := range n.VStack {
				items[i] = sizing(it.Flex, it.Fixed, visit(&n.VStack[i].Node))
				_ = it
			}
			return layout.VStack(items...)
		case n.HStack != nil:
			items := make([]layout.Item, len(n.HStack))
			for i, it := range n.HStack {
				items[i] = sizing(it.Flex, it.Fixed, visit(&n.HStack[i].Node))
				_ = it
			}
			return layout.HStack(items...)
		case n.ZStack != nil:
			return layout.ZStack(visit(&n.ZStack.Base), visit(&n.ZStack.Overlay))
		case n.Component != "":
			return componentNode(t.Components[n.Component])
		}
		return layout.RenderFunc(func(w, h int) string { return "" })
	}
	return visit(t.Root)
}

// sizing picks layout.Fixed or layout.Flex based on the item's hint. When
// both are zero, a Flex weight of 1 is assumed so the screen still renders.
func sizing(flex, fixed int, node layout.Node) layout.Item {
	if fixed > 0 {
		return layout.Fixed(fixed, node)
	}
	w := flex
	if w <= 0 {
		w = 1
	}
	return layout.Flex(w, node)
}

// componentNode wraps a Component for participation in the layout engine.
func componentNode(c *Component) layout.Node {
	switch c.Kind {
	case KList:
		return layout.Sized(c.List)
	case KTable:
		return layout.Sized(c.Table)
	case KLogview:
		return layout.Sized(c.Logview)
	case KTree:
		return layout.Sized(c.Tree)
	case KInspector:
		return layout.Sized(c.Inspector)
	}
	return layout.RenderFunc(func(w, h int) string { return "" })
}
