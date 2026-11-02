package gen

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// listKeys are the lists whose items have an identity, by the key that
// holds the list: a fragment's item merges with the item it names
// rather than replacing the whole list.
var listKeys = map[string]string{
	"env":     "name",   // app.env
	"prompts": "key",    // app.prompts
	"columns": "title",  // a table's columns
	"actions": "action", // a screen's or tab's bindings
	"items":   "screen", // a menu's items
	"tabs":    "label",  // a screen's tabs
}

// merge lays a fragment over the generated config, JSON-merge-patch
// style: maps merge, scalars replace, null deletes, lists replace except
// those in listKeys, which merge item by item ({…, $drop: true} removes
// one). touched collects every path the fragment sets, for the conflict
// check with gen: hints.
func merge(dst, patch *yaml.Node, path []string, touched *[][]string) error {
	for i := 0; i+1 < len(patch.Content); i += 2 {
		k, v := patch.Content[i], patch.Content[i+1]
		at := append(slices.Clone(path), k.Value)
		j := slices.IndexFunc(pairs(dst), func(p int) bool { return dst.Content[p].Value == k.Value })
		switch {
		case v.Tag == "!!null":
			if j >= 0 {
				p := pairs(dst)[j]
				dst.Content = slices.Delete(dst.Content, p, p+2)
			}
			*touched = append(*touched, at)
		case j < 0:
			dst.Content = append(dst.Content, k, v)
			*touched = append(*touched, at)
		default:
			cur := dst.Content[pairs(dst)[j]+1]
			switch {
			case v.Kind == yaml.MappingNode && cur.Kind == yaml.MappingNode:
				if err := merge(cur, v, at, touched); err != nil {
					return err
				}
			case v.Kind == yaml.SequenceNode && cur.Kind == yaml.SequenceNode && listKeys[k.Value] != "" && isAction(path, k.Value):
				if err := mergeList(cur, v, listKeys[k.Value], at, touched); err != nil {
					return err
				}
			default:
				dst.Content[pairs(dst)[j]+1] = v
				*touched = append(*touched, at)
			}
		}
	}
	return nil
}

// isAction tells a screen's actions: list (bindings, merged by action)
// from the top-level actions: map, which merges as a map anyway.
func isAction(path []string, key string) bool {
	return key != "actions" || len(path) > 0
}

// pairs are the indexes of a mapping's keys.
func pairs(m *yaml.Node) []int {
	var out []int
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, i)
	}
	return out
}

// mergeList merges a list item by item on idKey.
func mergeList(dst, patch *yaml.Node, idKey string, path []string, touched *[][]string) error {
	for _, item := range patch.Content {
		id := get(item, idKey)
		if item.Kind != yaml.MappingNode || id == nil {
			return fmt.Errorf("%s: each item needs its %s:, to say which one it changes", strings.Join(path, "."), idKey)
		}
		at := append(slices.Clone(path), id.Value)
		drop := get(item, "$drop")
		i := slices.IndexFunc(dst.Content, func(n *yaml.Node) bool {
			cur := get(n, idKey)
			return n.Kind == yaml.MappingNode && cur != nil && cur.Value == id.Value
		})
		switch {
		case drop != nil && drop.Value == "true":
			if i < 0 {
				return fmt.Errorf("%s: $drop matches no item with %s %q", strings.Join(path, "."), idKey, id.Value)
			}
			dst.Content = slices.Delete(dst.Content, i, i+1)
		case i < 0:
			// Before any commented-out placeholder at the end.
			at := len(dst.Content)
			for at > 0 && dst.Content[at-1].Kind == yaml.ScalarNode && strings.HasPrefix(dst.Content[at-1].Value, placeholder) {
				at--
			}
			dst.Content = slices.Insert(dst.Content, at, item)
		default:
			// Merge into a block copy, so a one-line {…} item can grow.
			dst.Content[i].Style = 0
			if err := merge(dst.Content[i], item, at, touched); err != nil {
				return err
			}
			if len(dst.Content[i].Content) <= 8 {
				dst.Content[i].Style = yaml.FlowStyle
			}
		}
		*touched = append(*touched, at)
	}
	return nil
}
