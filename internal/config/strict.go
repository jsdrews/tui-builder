package config

// Strict decoding for the blocks that opt in to it. The loader
// otherwise ignores YAML keys it doesn't know, so a typo like `retyr:`
// is silently dropped; inside a strict block it's a load error that
// suggests the nearest known key.

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// checkStrict reports unknown keys in the blocks that decode strictly:
// every preset, and every source's or preset's paginate:. root is the
// whole document's node.
func checkStrict(root *yaml.Node) error {
	known := yamlFields(reflect.TypeOf(Source{}))
	paginate := yamlFields(reflect.TypeOf(PaginateConfig{}))
	for _, section := range []string{"presets", "sources"} {
		entries := mappingAt(root, "data", section)
		if entries == nil {
			continue
		}
		for i := 0; i+1 < len(entries.Content); i += 2 {
			name, body := entries.Content[i].Value, entries.Content[i+1]
			path := "data." + section + "." + name
			if section == "presets" {
				if err := checkKeys(body, path, known); err != nil {
					return err
				}
			}
			if err := checkKeys(mappingAt(body, "paginate"), path+".paginate", paginate); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkKeys reports the first key of mapping node n that isn't in known.
func checkKeys(n *yaml.Node, path string, known []string) error {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if !contains(known, k) {
			return unknownField(path, k, known)
		}
	}
	return nil
}

// unknownField is the error for a key that isn't one of known.
func unknownField(path, key string, known []string) error {
	msg := fmt.Sprintf("%s: unknown field %q", path, key)
	if s := suggest(key, known); s != "" {
		msg += fmt.Sprintf(" (did you mean %s?)", s)
	}
	return fmt.Errorf("%s", msg)
}

// mappingAt walks a document node down the given mapping keys.
func mappingAt(n *yaml.Node, keys ...string) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, key := range keys {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// yamlFields lists the YAML names of t's fields.
func yamlFields(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		if name := yamlName(t.Field(i)); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// yamlName is f's YAML key, or "" for a field YAML never sees.
func yamlName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" || !f.IsExported() {
		return ""
	}
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

// suggest returns the candidate closest to s, if it's close enough to
// be a likely typo.
func suggest(s string, candidates []string) string {
	best, bestD := "", len(s)/3+2
	for _, c := range candidates {
		if d := editDistance(s, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
