// migrate_yaml rewrites tuiquery example YAMLs from the legacy
// data.sources + data.pipelines split into the unified polymorphic
// shape (one data.sources map, every entry carries a `type:`).
//
// Run:
//
//	go run scripts/migrate_yaml.go examples/*.yaml
//
// Per-file transforms:
//
//   - For each entry in `data.pipelines:`:
//     - Identify the operator (from / filter / project / derive / sort /
//       union / compose / join / cache). `from:` alone → passthrough.
//     - Lift the operator's sub-fields up onto the entry.
//     - Add `type: <op>` at the head of the entry.
//     - Move the entry into `data.sources:`.
//   - For inline `pipe:` stages on any entry:
//     - Convert `{filter: {where: X}}` → `{type: filter, where: X}` (same
//       lift + type-prepend).
//   - For components: rename `pipeline: X` → `source: X`.
//   - Delete the `data.pipelines:` block entirely.
//
// The yaml.v3 Node API preserves comments through edit + round-trip,
// so the rewritten files keep their headers and inline annotations.
package main

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// operatorKeys are the YAML keys that today identify a pipeline's
// operator (which arm of the pipeline tagged union is set). Order
// matters for first-match selection in convertPipelineEntry — we
// expect exactly one to be set per entry, but iterating gives stable
// behaviour even if a malformed config sets more than one.
var operatorKeys = []string{
	"filter", "project", "derive", "sort",
	"union", "compose", "join", "cache",
}

// stageOps are the operator keys allowed in a `pipe:` stage. Pipe
// stages are single-input transforms only — multi-input ops
// (union / compose / join) aren't allowed.
var stageOps = []string{"filter", "project", "derive", "sort", "cache"}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate_yaml <file.yaml> [file.yaml ...]")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		changed, err := migrate(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", path, err)
			os.Exit(1)
		}
		if changed {
			fmt.Printf("rewrote %s\n", path)
		} else {
			fmt.Printf("unchanged %s\n", path)
		}
	}
}

func migrate(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}
	changed := transform(&root)
	if !changed {
		return false, nil
	}
	// yaml.Marshal defaults to 4-space indent; the project's existing
	// YAMLs use 2. Use an Encoder to override.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return false, fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return false, fmt.Errorf("close encoder: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// transform applies the migration to the root document. Returns true
// when something changed (so callers can report rewrote-vs-unchanged).
func transform(root *yaml.Node) bool {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return false
	}

	changed := false

	// Walk data: { sources, pipelines }
	if data := mapGet(doc, "data"); data != nil && data.Kind == yaml.MappingNode {
		sources := mapGet(data, "sources")
		pipelines := mapGet(data, "pipelines")

		if pipelines != nil && pipelines.Kind == yaml.MappingNode {
			// Build sources if it doesn't exist yet so we have somewhere
			// to put the migrated pipeline entries.
			if sources == nil {
				sources = newMappingNode()
				mapSet(data, "sources", sources)
			}
			// Transfer each pipeline entry's ORIGINAL key node + value
			// pair into sources. Reusing the original key node
			// preserves head comments ("# Passthrough — stable
			// addressable name", etc.) that yaml.v3 attaches to keys.
			for i := 0; i+1 < len(pipelines.Content); i += 2 {
				keyNode := pipelines.Content[i]
				valNode := pipelines.Content[i+1]
				converted := convertPipelineEntry(valNode)
				appendPair(sources, keyNode, converted)
			}
			mapDelete(data, "pipelines")
			changed = true
		}

		// Walk every entry in sources to handle inline pipe stages
		// (applies to both pre-existing leaf-with-pipe entries AND the
		// freshly migrated pipeline entries that came with their own
		// pipe stages).
		if sources != nil && sources.Kind == yaml.MappingNode {
			mapForEach(sources, func(_ string, entry *yaml.Node) {
				if convertPipeStages(entry) {
					changed = true
				}
			})
		}
	}

	// Rename component pipeline: -> source:
	if tui := mapGet(doc, "tui"); tui != nil && tui.Kind == yaml.MappingNode {
		if components := mapGet(tui, "components"); components != nil && components.Kind == yaml.MappingNode {
			mapForEach(components, func(_ string, comp *yaml.Node) {
				if comp == nil || comp.Kind != yaml.MappingNode {
					return
				}
				if p := mapGet(comp, "pipeline"); p != nil {
					mapSet(comp, "source", p)
					mapDelete(comp, "pipeline")
					changed = true
				}
			})
		}
	}

	return changed
}

// convertPipelineEntry takes a value node from `data.pipelines:` and
// returns the same node, mutated into the new unified shape:
//
//   - exactly one of the operatorKeys was set as a sub-mapping; its
//     children are lifted up to entry level
//   - the original operator key is removed
//   - a fresh `type: <op>` pair is prepended
//   - if no operator key was found but `from:` is present, the entry
//     is treated as a passthrough (type prepended; nothing lifted)
//
// The returned node is the same pointer as the input. Comments on
// the lifted sub-fields are preserved by reusing the same yaml.Node
// pointers via mapSet.
func convertPipelineEntry(entry *yaml.Node) *yaml.Node {
	if entry == nil || entry.Kind != yaml.MappingNode {
		return entry
	}
	for _, op := range operatorKeys {
		opNode := mapGet(entry, op)
		if opNode == nil {
			continue
		}
		if opNode.Kind == yaml.MappingNode {
			// Lift the operator's children onto the entry.
			for i := 0; i < len(opNode.Content); i += 2 {
				mapSet(entry, opNode.Content[i].Value, opNode.Content[i+1])
			}
		}
		// Recurse into any `pipe:` field the operator brought with it,
		// or that already existed on the entry, so the stages get
		// rewritten too.
		mapDelete(entry, op)
		prependType(entry, op)
		convertPipeStages(entry)
		return entry
	}
	// No operator key — must be a bare `from:` passthrough.
	if mapGet(entry, "from") != nil {
		prependType(entry, "passthrough")
		convertPipeStages(entry)
	}
	return entry
}

// convertPipeStages walks any `pipe:` field on entry and rewrites
// each stage from the old `{filter: {where: ...}}` shape into the
// new `{type: filter, where: ...}` shape. Returns true when anything
// changed.
func convertPipeStages(entry *yaml.Node) bool {
	pipe := mapGet(entry, "pipe")
	if pipe == nil || pipe.Kind != yaml.SequenceNode {
		return false
	}
	changed := false
	for _, stage := range pipe.Content {
		if stage == nil || stage.Kind != yaml.MappingNode {
			continue
		}
		// Skip stages already in the new shape.
		if mapGet(stage, "type") != nil {
			continue
		}
		for _, op := range stageOps {
			opNode := mapGet(stage, op)
			if opNode == nil {
				continue
			}
			if opNode.Kind == yaml.MappingNode {
				for i := 0; i < len(opNode.Content); i += 2 {
					mapSet(stage, opNode.Content[i].Value, opNode.Content[i+1])
				}
			}
			mapDelete(stage, op)
			prependType(stage, op)
			changed = true
			break
		}
	}
	return changed
}

// ──── yaml.Node mapping helpers ──────────────────────────────────────

// mapGet returns the value node paired with key in m, or nil. m must
// be a MappingNode (caller's responsibility).
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet sets m[key] = value. If key exists, the value is replaced;
// otherwise the (key, value) pair is appended. Order-preserving.
func mapSet(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, keyNode, value)
}

// mapDelete removes key from m. No-op if absent.
func mapDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// mapForEach iterates over key/value pairs in m. Callback receives
// the key as a string and the value node. Iteration order matches
// the YAML source.
func mapForEach(m *yaml.Node, fn func(key string, val *yaml.Node)) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		fn(m.Content[i].Value, m.Content[i+1])
	}
}

// prependType inserts `type: <kind>` at the head of m's content so
// it renders first when serialized. Preserves all existing pairs in
// their relative order. Replaces any pre-existing `type:` (defensive
// — shouldn't fire on well-formed input).
func prependType(m *yaml.Node, kind string) {
	mapDelete(m, "type")
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "type"}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kind}
	m.Content = append([]*yaml.Node{keyNode, valNode}, m.Content...)
}

func newMappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// appendPair appends (keyNode, valNode) to m. Unlike mapSet (which
// creates a fresh key node from a string), this preserves the
// original key node — important when comments are attached to keys.
func appendPair(m *yaml.Node, keyNode, valNode *yaml.Node) {
	m.Content = append(m.Content, keyNode, valNode)
}
