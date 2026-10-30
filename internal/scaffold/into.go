package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/config"
)

// Places a component can go, for --place.
var Places = []string{"stack", "tab", "screen"}

// Spliced is a config with add exec's result added to it.
type Spliced struct {
	Old, New []byte
	// Summary says what went where: "added source pods, table pods to
	// screen main".
	Summary string
	// Notes are things worth knowing about where it went.
	Notes []string
}

// SpliceInto adds res's source, components and layout to the config in
// old, by inserting lines into it: nothing else in the file is
// rewritten, so comments, quoting and layout survive. place is "" (by
// the config's shape) or one of Places. dir is the config's directory,
// for checking the result loads with its relative paths.
func SpliceInto(old []byte, res Result, place, dir string) (Spliced, error) {
	pt := res.parts
	if pt == nil {
		return Spliced{}, errors.New("nothing to add")
	}
	if err := refuseFile(old); err != nil {
		return Spliced{}, err
	}
	s := &splicer{text: string(old), parts: pt}
	if err := s.parse(); err != nil {
		return Spliced{}, err
	}
	if err := s.checkNames(); err != nil {
		return Spliced{}, err
	}
	steps := []func() error{s.addSource, s.addComponents, func() error { return s.place(place) }}
	for _, step := range steps {
		if err := step(); err != nil {
			return Spliced{}, err
		}
		if err := s.parse(); err != nil {
			return Spliced{}, fmt.Errorf("the edited config doesn't parse, which is a bug in add exec: %w", err)
		}
	}
	out := []byte(s.text)
	if err := verify(old, out, s); err != nil {
		return Spliced{}, fmt.Errorf("the edit isn't the old config plus the new entries, which is a bug in add exec: %w", err)
	}
	if err := loadsIn(out, dir); err != nil {
		return Spliced{}, fmt.Errorf("the edited config doesn't load: %w", err)
	}
	return Spliced{Old: old, New: out, Summary: s.summary(), Notes: s.notes}, nil
}

// refuseFile turns away files the splice can't edit safely.
func refuseFile(b []byte) error {
	if bytes.Contains(b, []byte("\r\n")) {
		return errors.New("the config has CRLF line endings; --into edits files with LF endings only")
	}
	for i, line := range strings.Split(string(b), "\n") {
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if strings.Contains(indent, "\t") {
			return fmt.Errorf("line %d is indented with a tab; --into edits space-indented files only", i+1)
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	docs := 0
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("the config doesn't parse: %w", err)
		}
		docs++
	}
	if docs > 1 {
		return errors.New("the config has more than one YAML document (---); --into edits single-document files only")
	}
	if docs == 0 {
		return errors.New("the config is empty")
	}
	return nil
}

// splicer holds the config's text as it's edited, and its parse.
type splicer struct {
	text  string
	parts *parts
	root  *yaml.Node // the document's top mapping
	cfg   map[string]any
	step  int // the file's indent step
	// edited are the paths whose values the placement changed or added,
	// besides the new source and components.
	edited [][]string
	where  string
	notes  []string
}

func (s *splicer) parse() error {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(s.text), &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("the config isn't a YAML mapping")
	}
	s.root = doc.Content[0]
	s.cfg = nil
	if err := yaml.Unmarshal([]byte(s.text), &s.cfg); err != nil {
		return err
	}
	s.step = indentStep(s.root)
	return nil
}

// indentStep is the file's indent step: the column of the first nested
// block mapping's keys. 2 when there's none.
func indentStep(root *yaml.Node) int {
	for i := 1; i < len(root.Content); i += 2 {
		v := root.Content[i]
		if v.Kind == yaml.MappingNode && v.Style&yaml.FlowStyle == 0 && len(v.Content) > 0 {
			if c := v.Content[0].Column - 1; c > 0 {
				return c
			}
		}
	}
	return 2
}

// get finds the value at a key path, or nil.
func get(n *yaml.Node, path ...string) *yaml.Node {
	for _, k := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}

func keyNode(n *yaml.Node, k string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == k {
			return n.Content[i]
		}
	}
	return nil
}

func (s *splicer) checkNames() error {
	pt := s.parts
	if get(s.root, "data", "sources", pt.name) != nil {
		return fmt.Errorf("source `%s` already exists — pass --name %s2", pt.name, pt.name)
	}
	comps := get(s.root, "tui", "components")
	for i := 0; i+1 < len(pt.components.Content); i += 2 {
		name := pt.components.Content[i].Value
		if get(comps, name) != nil {
			if name == pt.name {
				return fmt.Errorf("component `%s` already exists — pass --name %s2", name, name)
			}
			return fmt.Errorf("component `%s` (an input field) already exists — rename that --param", name)
		}
	}
	return nil
}

// addSource adds the source under data.sources, making data: or
// sources: when the config has none.
func (s *splicer) addSource() error {
	entry := mapping(s.parts.name, s.parts.source)
	entry.Content[0].HeadComment = "Added by `tui-builder add exec -- " + ShellJoin(s.parts.argv) + "`."
	data := get(s.root, "data")
	switch {
	case data == nil:
		// A data: block just before tui:, where configs keep it.
		tui := keyNode(s.root, "tui")
		if tui == nil {
			return errors.New("the config has no tui: block")
		}
		block := s.render(mapping("data", mapping("sources", entry)), 0)
		at := commentStart(s.lines(), tui.Line-1)
		s.insertLines(at, append(block, ""))
		return nil
	case get(data, "sources") == nil:
		return s.appendEntry(data, []string{"data"}, mapping("sources", entry))
	}
	return s.appendEntry(get(data, "sources"), []string{"data", "sources"}, entry)
}

// addComponents adds the component and any input fields under
// tui.components.
func (s *splicer) addComponents() error {
	tui := get(s.root, "tui")
	if tui == nil {
		return errors.New("the config has no tui: block")
	}
	comps := get(tui, "components")
	if comps == nil {
		return s.appendEntry(tui, []string{"tui"}, mapping("components", s.parts.components))
	}
	return s.appendEntry(comps, []string{"tui", "components"}, s.parts.components)
}

// place shows the component: in the screen's layout, as a tab, or as a
// screen of its own.
func (s *splicer) place(place string) error {
	tui := get(s.root, "tui")
	single, screens := get(tui, "screen"), get(tui, "screens")
	label := s.parts.title
	if screens == nil {
		if single == nil {
			return errors.New("the config has no tui.screen or tui.screens")
		}
		switch {
		case place == "screen":
			return errors.New("--place screen: this config has one screen (tui.screen), and add exec doesn't turn it into several")
		case place == "tab" || (place == "" && get(single, "tabs") != nil):
			return s.addTab(single, []string{"tui", "screen"}, label)
		}
		return s.stack(single, []string{"tui", "screen"}, "the screen")
	}
	initialName := ""
	if n := get(tui, "initial"); n != nil {
		initialName = n.Value
	}
	initial := get(screens, initialName)
	if initial == nil {
		return fmt.Errorf("tui.initial %q isn't a screen", initialName)
	}
	at := []string{"tui", "screens", initialName}
	switch place {
	case "stack":
		return s.stack(initial, at, "screen `"+initialName+"`")
	case "tab":
		return s.addTab(initial, at, label)
	}
	menu := s.menuOn(initial)
	if place == "" && menu == "" {
		s.notes = append(s.notes, fmt.Sprintf("screen `%s` has no menu to link a new screen from, so the %s went on it instead", initialName, s.parts.kind))
		if get(initial, "tabs") != nil {
			return s.addTab(initial, at, label)
		}
		return s.stack(initial, at, "screen `"+initialName+"`")
	}
	if get(screens, s.parts.name) != nil {
		return fmt.Errorf("screen `%s` already exists — pass --name %s2", s.parts.name, s.parts.name)
	}
	screen := mapping("title", scalar(label), "layout", s.parts.layout)
	if err := s.appendEntry(screens, []string{"tui", "screens"}, mapping(s.parts.name, screen)); err != nil {
		return err
	}
	s.edited = append(s.edited, []string{"tui", "screens", s.parts.name})
	s.where = "to screen `" + s.parts.name + "`"
	if menu == "" {
		s.notes = append(s.notes, fmt.Sprintf("nothing links to screen `%s` yet: add a menu item or a push action for it", s.parts.name))
		return nil
	}
	if err := s.parse(); err != nil {
		return err
	}
	items := get(s.root, "tui", "components", menu, "items")
	if items == nil || items.Kind != yaml.SequenceNode {
		return fmt.Errorf("menu `%s` has no items list to add to", menu)
	}
	item := mapping("label", scalar(label), "screen", scalar(s.parts.name))
	item.Style = yaml.FlowStyle
	if err := s.appendItem(items, item); err != nil {
		return err
	}
	s.edited = append(s.edited, []string{"tui", "components", menu, "items"})
	s.where += ", linked from menu `" + menu + "`"
	return nil
}

// menuOn is the name of a menu component in a screen's layout, or "".
func (s *splicer) menuOn(screen *yaml.Node) string {
	var found string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil || found != "" {
			return
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == "component" {
					name := n.Content[i+1].Value
					if t := get(s.root, "tui", "components", name, "type"); t != nil && t.Value == "menu" {
						found = name
						return
					}
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(get(screen, "layout"))
	return found
}

// addTab appends a tab to a screen's tabs.
func (s *splicer) addTab(screen *yaml.Node, at []string, label string) error {
	tabs := get(screen, "tabs")
	if tabs == nil {
		return fmt.Errorf("--place tab: %s has no tabs", strings.Join(at, "."))
	}
	if tabs.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: tabs isn't a list", tabs.Line)
	}
	for _, t := range tabs.Content {
		if l := get(t, "label"); l != nil && l.Value == label {
			return fmt.Errorf("a tab labelled %q already exists — pass --title", label)
		}
	}
	if err := s.appendItem(tabs, mapping("label", scalar(label), "layout", s.parts.layout)); err != nil {
		return err
	}
	s.edited = append(s.edited, append(slices.Clone(at), "tabs"))
	s.where = fmt.Sprintf("as tab %q", label)
	return nil
}

// stack adds the layout to a screen's layout: a lone component becomes
// a vstack of the old and the new, and a vstack gets one more item.
func (s *splicer) stack(screen *yaml.Node, at []string, where string) error {
	if get(screen, "tabs") != nil {
		return fmt.Errorf("--place stack: %s has tabs; use --place tab", where)
	}
	layout := get(screen, "layout")
	if layout == nil || layout.Kind != yaml.MappingNode {
		return fmt.Errorf("%s has no layout to add to", where)
	}
	at = append(slices.Clone(at), "layout")
	s.edited = append(s.edited, at)
	s.where = "to " + where
	if vs := get(layout, "vstack"); vs != nil && len(layout.Content) == 2 {
		if vs.Style&yaml.FlowStyle != 0 {
			return fmt.Errorf("line %d: the layout's vstack is written inline ([...]); write it as a block list to add to it", vs.Line)
		}
		return s.appendItem(vs, s.parts.layout)
	}
	comp := get(layout, "component")
	if comp == nil || len(layout.Content) != 2 {
		return fmt.Errorf("line %d: the layout is an hstack or zstack; add exec adds to a lone component or a vstack (move it by hand, or use --place)", layout.Line)
	}
	// Replace the lone component with a vstack of it and the new one.
	vstack := mapping("vstack", &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{
		mapping("component", scalar(comp.Value)),
		s.parts.layout,
	}})
	lines := s.lines()
	key := keyNode(screen, "layout")
	if layout.Style&yaml.FlowStyle != 0 {
		// layout: {component: x} on the key's line.
		if layout.Line != key.Line || strings.Count(lines[key.Line-1], "{") != 1 || !strings.HasSuffix(strings.TrimSpace(stripComment(lines[key.Line-1])), "}") {
			return fmt.Errorf("line %d: the layout is written inline in a way add exec can't rewrite; write it as a block", key.Line)
		}
		indent := key.Column - 1
		block := s.render(vstack, indent+s.step)
		s.replaceLines(key.Line-1, key.Line, append([]string{strings.Repeat(" ", indent) + "layout:"}, block...))
		return nil
	}
	// layout:\n  component: x
	ck := keyNode(layout, "component")
	block := s.render(vstack, ck.Column-1)
	s.replaceLines(ck.Line-1, ck.Line, block)
	return nil
}

// stripComment drops a trailing # comment from a line (outside quotes,
// roughly: a # after a space).
func stripComment(line string) string {
	if i := strings.Index(line, " #"); i >= 0 {
		return line[:i]
	}
	return line
}

// appendEntry adds key/value pairs (a mapping node) to the end of a
// block mapping, or under a key with no value yet.
func (s *splicer) appendEntry(parent *yaml.Node, at []string, entries *yaml.Node) error {
	where := strings.Join(at, ".")
	if err := refuseParent(parent, where); err != nil {
		return err
	}
	lines := s.lines()
	if parent.Kind == yaml.ScalarNode && parent.Tag == "!!null" {
		// `sources:` with nothing under it.
		key := s.keyFor(at)
		if key == nil {
			return fmt.Errorf("%s: can't find where it is", where)
		}
		s.insertLines(key.Line, s.render(entries, key.Column-1+s.step))
		return nil
	}
	if parent.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: %s isn't a mapping", parent.Line, where)
	}
	col := parent.Content[0].Column - 1
	end := blockEnd(lines, parent.Content[0].Line-1, col, false)
	block := s.render(entries, col)
	if spaced(lines, parent) {
		block = append([]string{""}, block...)
	}
	s.insertLines(end, block)
	return nil
}

// appendItem adds an item to the end of a block sequence.
func (s *splicer) appendItem(seq *yaml.Node, item *yaml.Node) error {
	if err := refuseParent(seq, fmt.Sprintf("the list at line %d", seq.Line)); err != nil {
		return err
	}
	if len(seq.Content) == 0 {
		return fmt.Errorf("line %d: the list is empty, so there's no item to line the new one up with", seq.Line)
	}
	lines := s.lines()
	first := seq.Content[0]
	dash := strings.Index(lines[first.Line-1], "-")
	if dash < 0 || dash >= first.Column-1 {
		return fmt.Errorf("line %d: can't find the list's dash", first.Line)
	}
	end := blockEnd(lines, first.Line-1, dash, true)
	s.insertLines(end, s.render(&yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{item}}, dash))
	return nil
}

// refuseParent turns away a parent the splice can't add to safely.
func refuseParent(n *yaml.Node, where string) error {
	switch {
	case n.Kind == yaml.AliasNode:
		return fmt.Errorf("line %d: %s is an alias (*%s); editing it would change its anchor's other uses", n.Line, where, n.Value)
	case n.Style&yaml.FlowStyle != 0:
		return fmt.Errorf("line %d: %s is written inline ({...} or [...]); write it as a block to add to it", n.Line, where)
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "<<" {
				return fmt.Errorf("line %d: %s pulls entries in with <<:; editing it would change the merge", n.Content[i].Line, where)
			}
		}
	}
	if len(n.Content) > 0 {
		last := n.Content[len(n.Content)-1]
		for last.Kind != yaml.ScalarNode && len(last.Content) > 0 {
			last = last.Content[len(last.Content)-1]
		}
		if last.Kind == yaml.ScalarNode && (last.Style == yaml.LiteralStyle || last.Style == yaml.FoldedStyle) && keepChomped(last) {
			return fmt.Errorf("line %d: %s ends in a |+ or >+ value, whose trailing blank lines are part of it; add a line after it by hand first", last.Line, where)
		}
	}
	return nil
}

// keepChomped is a block scalar whose value ends in more than one
// newline: the |+ / >+ case, where blank lines after it belong to it.
// (yaml.v3 doesn't keep the indicator; the value says it.)
func keepChomped(n *yaml.Node) bool { return strings.HasSuffix(n.Value, "\n\n") }

// keyFor is the key node of the value at path.
func (s *splicer) keyFor(path []string) *yaml.Node {
	parent := s.root
	if len(path) > 1 {
		parent = get(s.root, path[:len(path)-1]...)
	}
	if parent == nil {
		return nil
	}
	return keyNode(parent, path[len(path)-1])
}

// spaced is true when a mapping's entries are separated by blank lines.
func spaced(lines []string, m *yaml.Node) bool {
	if len(m.Content) < 4 {
		return false
	}
	second := m.Content[2].Line - 1
	return second > 0 && strings.TrimSpace(lines[second-1]) == ""
}

// blockEnd is the index of the line after a block's last line, given
// its first entry's line and the column its entries start at. Comments
// at or deeper than the column belong to it; trailing blank lines don't.
// In a sequence, a line at the dash column that isn't an item ends it.
func blockEnd(lines []string, first, col int, seq bool) int {
	last := first
	for i := first + 1; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		ind := len(l) - len(strings.TrimLeft(l, " "))
		if ind < col {
			break
		}
		if seq && ind == col && !strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "#") {
			break
		}
		last = i
	}
	return last + 1
}

// commentStart is where the comments directly above a line begin.
func commentStart(lines []string, i int) int {
	for i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "#") {
		i--
	}
	return i
}

// render writes a node as lines at the file's indent step, each
// prefixed with indent spaces.
func (s *splicer) render(n *yaml.Node, indent int) []string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(s.step)
	if err := enc.Encode(n); err != nil {
		panic(err) // nodes built here always encode
	}
	enc.Close()
	text := string(expandMoreColumns(buf.Bytes(), s.parts.more))
	var out []string
	pad := strings.Repeat(" ", indent)
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, pad+l)
	}
	return out
}

func (s *splicer) lines() []string { return strings.Split(s.text, "\n") }

func (s *splicer) insertLines(at int, block []string) {
	lines := s.lines()
	s.text = strings.Join(slices.Insert(lines, at, block...), "\n")
}

func (s *splicer) replaceLines(from, to int, block []string) {
	lines := s.lines()
	s.text = strings.Join(slices.Replace(lines, from, to, block...), "\n")
}

func (s *splicer) summary() string {
	var comps []string
	for i := 0; i+1 < len(s.parts.components.Content); i += 2 {
		comps = append(comps, s.parts.components.Content[i].Value)
	}
	what := fmt.Sprintf("%s `%s`", s.parts.kind, s.parts.name)
	if len(comps) > 1 {
		what += fmt.Sprintf(" (and input fields %s)", strings.Join(comps[1:], ", "))
	}
	return fmt.Sprintf("added source `%s`, %s %s", s.parts.name, what, s.where)
}

// verify checks new is old plus exactly the added entries: the new
// source and components, and the placement's edited paths.
func verify(oldB, newB []byte, s *splicer) error {
	var oldV, newV map[string]any
	if err := yaml.Unmarshal(oldB, &oldV); err != nil {
		return err
	}
	if err := yaml.Unmarshal(newB, &newV); err != nil {
		return err
	}
	if got := dig(newV, "data", "sources", s.parts.name); got == nil {
		return errors.New("the source isn't in the result")
	}
	del(newV, "data", "sources", s.parts.name)
	for i := 0; i+1 < len(s.parts.components.Content); i += 2 {
		name := s.parts.components.Content[i].Value
		if dig(newV, "tui", "components", name) == nil {
			return fmt.Errorf("component %s isn't in the result", name)
		}
		del(newV, "tui", "components", name)
	}
	for _, p := range s.edited {
		del(newV, p...)
		del(oldV, p...)
	}
	prune(newV, oldV)
	if !reflect.DeepEqual(oldV, newV) {
		return errors.New("something outside the new entries changed")
	}
	return nil
}

func dig(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func del(v map[string]any, path ...string) {
	parent, ok := dig(v, path[:len(path)-1]...).(map[string]any)
	if len(path) == 1 {
		parent, ok = v, true
	}
	if ok {
		delete(parent, path[len(path)-1])
	}
}

// prune drops maps left empty in new that old didn't have (a data: or
// sources: block made for the new source).
func prune(newV, oldV map[string]any) {
	for k, v := range newV {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		om, _ := oldV[k].(map[string]any)
		prune(m, om)
		if _, had := oldV[k]; len(m) == 0 && !had {
			delete(newV, k)
		}
	}
}

// loadsIn checks b loads as a config in dir, where relative paths in it
// resolve as they will once it's written. Required env vars that aren't
// set are stood in for while it loads: they're the user's to supply at
// runtime, and the old config needed them too.
func loadsIn(b []byte, dir string) error {
	var head struct {
		App struct {
			Env []config.EnvSpec `yaml:"env"`
		} `yaml:"app"`
	}
	if err := yaml.Unmarshal(b, &head); err == nil {
		for _, e := range head.App.Env {
			if _, set := os.LookupEnv(e.Name); e.Required && !set {
				os.Setenv(e.Name, "stand-in")
				defer os.Unsetenv(e.Name)
			}
		}
	}
	f, err := os.CreateTemp(dir, ".tui-builder-check-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, err = config.LoadTo(f.Name(), io.Discard)
	return err
}

// WriteAtomic replaces path with b: a temporary file beside it, given
// path's mode, renamed over it.
func WriteAtomic(path string, b []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(info.Mode().Perm()); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
