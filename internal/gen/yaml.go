package gen

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Nodes for the config being written. Strings are always strings,
// quoted when they'd read as anything else.

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func intNode(n int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(n)}
}

func boolean(v bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}
}

func mapping(kv ...any) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i < len(kv); i += 2 {
		add(m, kv[i].(string), kv[i+1].(*yaml.Node))
	}
	return m
}

func flow(n *yaml.Node) *yaml.Node {
	n.Style = yaml.FlowStyle
	return n
}

func seq(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Content: items}
}

func strs(items []string) *yaml.Node {
	s := seq()
	for _, it := range items {
		s.Content = append(s.Content, str(it))
	}
	return s
}

func add(m *yaml.Node, k string, v *yaml.Node) {
	m.Content = append(m.Content, str(k), v)
}

// keyed adds k: v with a comment above the key.
func keyed(m *yaml.Node, k string, v *yaml.Node, comment string) {
	key := str(k)
	key.HeadComment = comment
	m.Content = append(m.Content, key, v)
}

// comments collects commented-out lines to place inside a mapping or a
// list: yaml.v3 misplaces foot comments, so a placeholder entry marks
// the spot and render swaps the lines in at its indentation.
type comments struct{ blocks [][]string }

const placeholder = "__gen_comment_"

// in adds a placeholder entry for lines to the end of a mapping.
func (c *comments) in(m *yaml.Node, lines ...string) {
	add(m, fmt.Sprintf("%s%d__", placeholder, len(c.blocks)), str(""))
	c.blocks = append(c.blocks, lines)
}

// inSeq adds a placeholder item for lines to the end of a list.
func (c *comments) inSeq(s *yaml.Node, lines ...string) {
	s.Content = append(s.Content, str(fmt.Sprintf("%s%d__", placeholder, len(c.blocks))))
	c.blocks = append(c.blocks, lines)
}

// flowLine is a node written as one line of flow YAML.
func flowLine(n *yaml.Node) string {
	b, err := yaml.Marshal(n)
	if err != nil {
		panic(err) // nodes built here always marshal
	}
	return strings.TrimSpace(string(b))
}

// render encodes doc and swaps each placeholder for its lines.
func (c *comments) render(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		item := strings.TrimPrefix(trimmed, "- ")
		if !strings.HasPrefix(item, placeholder) {
			out = append(out, line)
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		var n int
		fmt.Sscanf(strings.TrimPrefix(item, placeholder), "%d", &n)
		for _, l := range c.blocks[n] {
			out = append(out, indent+"# "+l)
		}
	}
	return []byte(strings.Join(out, "\n")), nil
}
