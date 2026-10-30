package gen

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/columns"
)

// kind is what a GET becomes.
type kind int

const (
	kindList      kind = iota // a top-level list
	kindSubList               // a list pushed from a parent list's row
	kindDetail                // one item, pushed from its list's row
	kindSingleton             // one object, no path parameters
)

// read is a GET that maps to a screen, before it's built.
type read struct {
	op   op
	kind kind
	root string           // where the rows are, for a list
	item *openapi3.Schema // a list's row schema
	// parent is the list a detail or sub-list is pushed from, and param
	// the path parameter the parent's row fills.
	parent *read
	param  string
	// orphan is a sub-list's path parameter when its parent list isn't
	// generated (absent, or left out by --tag): it's top-level, and the
	// parameter is asked for at startup.
	orphan string
	name   string
}

// classify sorts GETs into lists, details, sub-lists and singletons,
// and says why the rest are skipped.
func classify(s *spec, gets []op) ([]*read, []skip, error) {
	var reads []*read
	var skips []skip
	// The paths that answer a list, for telling an object under an item
	// (/reports/{slug}/raw) from one with no list at all.
	lists := map[string]bool{}
	for _, o := range gets {
		if sh, _ := shapeOf(responseSchema(o.op)); sh == shapeList && !strings.Contains(o.path, "{") {
			lists[o.path] = true
		}
	}
	for _, o := range gets {
		schema := responseSchema(o.op)
		sh, root := shapeOf(schema)
		if h := s.ov.hint(o); h.Root != "" {
			// root: picks the rows, where several lists tie or the shape
			// doesn't say.
			body := unwrapNull(schema)
			_, props := properties(body)
			if p := props[h.Root]; body == nil || p == nil || !p.Type.Is("array") {
				return nil, nil, fmt.Errorf("%s: gen.operations.%s.root: the response has no list named %s", s.ov.path, h.key, h.Root)
			}
			sh, root = shapeList, h.Root
			s.hint(o)
		}
		endsInParam := strings.HasSuffix(o.path, "}")
		switch {
		case schema == nil:
			skips = append(skips, skip{o, "no response schema"})
		case endsInParam && (sh == shapeObject || sh == shapeList && !unwrapNull(schema).Type.Is("array")):
			// /things/{id} answering an object is a detail, even when
			// the object holds a list of its own.
			reads = append(reads, &read{op: o, kind: kindDetail})
		case sh == shapeList && root == "" && !unwrapNull(schema).Type.Is("array"):
			skips = append(skips, skip{o, "the response has several lists and none is named entries, items, data or results: set root in overrides"})
		case sh == shapeList:
			r := &read{op: o, kind: kindList, root: root, item: rowSchema(schema, root)}
			if strings.Contains(o.path, "{") {
				r.kind = kindSubList
			}
			reads = append(reads, r)
		case sh == shapeObject && !strings.Contains(o.path, "{"):
			reads = append(reads, &read{op: o, kind: kindSingleton})
		case sh == shapeObject && lists[listPrefix(o.path)]:
			skips = append(skips, skip{o, "one object under an item, neither a list nor the item itself: gen pushes lists and details"})
		case sh == shapeObject:
			skips = append(skips, skip{o, missingList(o.path)})
		case sh == shapeScalar:
			skips = append(skips, skip{o, "answers a single value, not an object or a list"})
		default:
			skips = append(skips, skip{o, "no response schema"})
		}
	}
	// Pair details and sub-lists with the top-level list whose rows they
	// come from.
	byPath := map[string]*read{}
	for _, r := range reads {
		if r.kind == kindList {
			byPath[r.op.path] = r
		}
	}
	var kept []*read
	for _, r := range reads {
		switch r.kind {
		case kindDetail:
			parent, param := parentOf(r.op.path, byPath, true)
			if parent == nil {
				skips = append(skips, skip{r.op, missingList(r.op.path)})
				continue
			}
			r.parent, r.param = parent, param
		case kindSubList:
			parent, param := parentOf(r.op.path, byPath, false)
			if parent == nil {
				if p := orphanParam(r.op.path); p != "" {
					r.kind, r.orphan = kindList, p
					break
				}
				skips = append(skips, skip{r.op, missingList(r.op.path)})
				continue
			}
			r.parent, r.param = parent, param
		}
		kept = append(kept, r)
	}
	return kept, skips, nil
}

// parentOf finds the list a path's one parameter comes from: for a
// detail, /things/{id} is /things plus the parameter; for a sub-list,
// /owners/{id}/things is a list, /owners, then the parameter, then
// literal segments. Only top-level lists are parents, since a pushed
// screen can't carry its own parameters on to another push.
func parentOf(path string, lists map[string]*read, detail bool) (*read, string) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	var params []int
	for i, s := range segs {
		if strings.HasPrefix(s, "{") {
			params = append(params, i)
		}
	}
	if len(params) != 1 {
		return nil, ""
	}
	i := params[0]
	if detail && i != len(segs)-1 || !detail && i == len(segs)-1 {
		return nil, ""
	}
	parent := lists["/"+strings.Join(segs[:i], "/")]
	if parent == nil {
		return nil, ""
	}
	return parent, strings.Trim(segs[i], "{}")
}

// orphanParam is a sub-list path's one parameter when it isn't last
// (/orgs/{org}/members), so it can be asked for at startup; "" otherwise.
func orphanParam(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	param := ""
	for i, s := range segs {
		if strings.HasPrefix(s, "{") {
			if param != "" || i == len(segs)-1 {
				return ""
			}
			param = strings.Trim(s, "{}")
		}
	}
	return param
}

// listPrefix is the path up to its first parameter: /reports for
// /reports/{slug}/raw.
func listPrefix(path string) string {
	i := strings.Index(path, "/{")
	if i < 0 {
		return ""
	}
	return path[:i]
}

// missingList is the skip reason for a path whose parameter has no list
// to pick it from.
func missingList(path string) string {
	var params []string
	for _, s := range strings.Split(path, "/") {
		if strings.HasPrefix(s, "{") {
			params = append(params, "`"+s+"`")
		}
	}
	if len(params) > 1 {
		return strings.Join(params, " and ") + " need a list to pick each from, and a push can carry only one: add it in overrides"
	}
	return strings.Join(params, "") + " has no list to pick it from: add one in overrides"
}

// rowSchema is a list's row schema: the array's items, under root.
func rowSchema(s *openapi3.Schema, root string) *openapi3.Schema {
	s = unwrapNull(s)
	if root != "" {
		_, props := properties(s)
		s = props[root]
	}
	if s == nil || s.Items == nil || s.Items.Value == nil {
		return nil
	}
	return unwrapNull(s.Items.Value)
}

// name names each read's screen from its path: /things is things,
// /things/{id} is thing, /owners/{id}/things is owner_things. A clash
// adds the method, then the operationId.
func nameReads(reads []*read, taken map[string]bool) {
	for _, r := range reads {
		name := pathName(r.op.path, r.kind == kindDetail)
		if taken[name] && r.op.op.OperationID != "" {
			name = snake(r.op.op.OperationID)
		}
		for n := 2; taken[name]; n++ {
			name = fmt.Sprintf("%s_%d", pathName(r.op.path, r.kind == kindDetail), n)
		}
		taken[name] = true
		r.name = name
	}
}

// pathName is a path's literal segments, every one but the last made
// singular (and the last too, for a detail).
func pathName(path string, detail bool) string {
	var lits []string
	for _, s := range strings.Split(path, "/") {
		if s != "" && !strings.HasPrefix(s, "{") {
			lits = append(lits, s)
		}
	}
	if len(lits) == 0 {
		return "root"
	}
	for i := range lits {
		if i < len(lits)-1 || detail {
			lits[i] = singular(lits[i])
		}
	}
	return snake(strings.Join(lits, "_"))
}

// singular is a plural noun's singular, roughly: things → thing,
// policies → policy, addresses → address.
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "shes"), strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "xes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "ss"), strings.HasSuffix(s, "us"):
		return s
	case strings.HasSuffix(s, "s") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s
}

// column is one plain column: a top-level scalar property.
type column struct {
	path, title string
	enum        []string // its declared values, for a window's distinct:
}

func isScalar(s *openapi3.Schema) bool {
	s = unwrapNull(s)
	for _, t := range []string{"string", "integer", "number", "boolean"} {
		if s.Type.Is(t) {
			return true
		}
	}
	return len(s.Enum) > 0 && s.Type == nil
}

// words turns a property name into a title: owner_id is "Owner id",
// createdAt "Created at".
func words(name string) string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.':
			flush()
			continue
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	if len(out) == 0 {
		return name
	}
	s := []rune(strings.Join(out, " "))
	s[0] = unicode.ToUpper(s[0])
	return string(s)
}

// idProperty is the row property that fills a path parameter: the
// parameter's own name, id, <singular>_id, or the parameter less its
// <singular>_ prefix (report_slug → slug). A row that's a plain value
// fills it itself (".").
func idProperty(item *openapi3.Schema, param, listSeg string) string {
	if item == nil {
		return ""
	}
	if !isObject(item) {
		return "."
	}
	_, props := properties(item)
	one := singular(listSeg)
	for _, want := range []string{param, "id", one + "_id", strings.TrimPrefix(param, one+"_")} {
		if p, ok := props[want]; ok && isScalar(p) {
			return want
		}
	}
	return ""
}

// reserved are keys a sub-list push can't take: the shell's, the
// action menu's, a table's, and n / e / d, which writes will use.
var reserved = "qrtToakjgGxXADnedhlfs/[]"

// pushKey is the next free key from a name's letters.
func pushKey(name string, taken map[string]bool) string {
	for _, r := range name + "bcimpuvwyz123456789" {
		k := string(unicode.ToLower(r))
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) || strings.Contains(reserved, k) || taken[k] {
			continue
		}
		taken[k] = true
		return k
	}
	return ""
}

// built is a read made into config: its source, component, screen and,
// for a top-level list, the bindings that push from it.
type built struct {
	*read
	// label names it in a tab or menu, and on its component; title is
	// its screen's, which a pushed screen fills from the row.
	label    string
	title    string
	group    string
	describe string
	source   *yaml.Node
	comp     *yaml.Node
	bindings []*yaml.Node
	prompts  []*yaml.Node
	note     string // about how it's paged
	walks    bool
	// more are a table's ranked columns past the cap, written commented
	// out; sortable / distinct say how they'd be written.
	more             []columns.Column
	sortable, onWind bool
	// keys are the action-menu keys its bindings have taken.
	keys map[string]bool
	// markable is set on a table that marks rows (off a window).
	markable bool
	// writes counts the write actions bound here, for action_groups.
	writes int
	// hidden is the overrides' hide: generated, but not on the menu or
	// tabs.
	hidden bool
}

// takeKey is the next free key for a binding on this screen: want when
// it's free, else one from name's letters.
func (b *built) takeKey(want, name string) string {
	if b.keys == nil {
		b.keys = map[string]bool{}
	}
	if want != "" && !b.keys[want] {
		b.keys[want] = true
		return want
	}
	return pushKey(name, b.keys)
}

func (b *built) topLevel() bool { return b.kind == kindList || b.kind == kindSingleton }

// build makes each read into a source and a component, and wires each
// detail and sub-list to a push from its parent's rows.
func build(ctx context.Context, s *spec, p *preset, reads []*read, title string, warn func(string)) ([]*built, *yaml.Node, []skip, error) {
	byRead := map[*read]*built{}
	var out []*built
	var skips []skip
	for _, r := range reads {
		if r.parent != nil && byRead[r.parent] == nil {
			skips = append(skips, skip{r.op, "its list " + r.parent.op.String() + " wasn't generated"})
			continue
		}
		b, why, err := buildRead(ctx, s, p, r, title)
		if err != nil {
			return nil, nil, nil, err
		}
		if why != "" {
			skips = append(skips, skip{r.op, why})
			continue
		}
		byRead[r] = b
		out = append(out, b)
	}
	actions := mapping()
	for _, b := range out {
		if b.parent == nil {
			continue
		}
		parent := byRead[b.parent]
		seg := lastLiteral(b.parent.op.path)
		prop := idProperty(b.parent.item, b.param, seg)
		if prop == "" {
			warn(fmt.Sprintf("%s: can't tell which column of %s fills {%s}, so the screen is written but nothing opens it", b.op, parent.name, b.param))
			continue
		}
		colTitle := ensureColumn(parent, prop)
		shown := colTitle
		if n := nameColumn(parent.comp); n != "" {
			shown = n
		}
		switch {
		case s.ov.hint(b.op).Title != "":
		case b.kind == kindDetail:
			b.title = singularTitle(b.parent.op.path) + " ${selection." + shown + "}"
		default:
			b.title = "${selection." + shown + "}'s " + strings.ToLower(b.label)
		}
		action := "open_" + b.name
		add(actions, action, flow(mapping("push", str(b.name))))
		key := "enter"
		if b.kind == kindSubList {
			key = parent.takeKey("", lastLiteral(b.op.path))
		} else if hasEnter(parent.bindings) {
			// Two details for one list: the second goes in the action
			// menu only.
			key = ""
		}
		bind := flow(mapping(b.param, str("${selection."+colTitle+"}")))
		binding := mapping()
		if key != "" {
			add(binding, "key", str(key))
		}
		add(binding, "action", str(action))
		add(binding, "label", str(b.label))
		add(binding, "from", str(parent.name))
		add(binding, "bind", bind)
		parent.bindings = append(parent.bindings, flow(binding))
	}
	return out, actions, skips, nil
}

func hasEnter(bindings []*yaml.Node) bool {
	for _, b := range bindings {
		if k := get(b, "key"); k != nil && k.Value == "enter" {
			return true
		}
	}
	return false
}

func lastLiteral(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if !strings.HasPrefix(segs[i], "{") {
			return segs[i]
		}
	}
	return ""
}

// ensureColumn returns the title of the column showing prop. A push
// binds by column title, so one past the cap is written hidden rather
// than commented out.
func ensureColumn(b *built, prop string) string {
	cols := get(b.comp, "columns")
	for _, c := range cols.Content {
		if get(c, "value").Value == prop {
			return get(c, "title").Value
		}
	}
	title := words(prop)
	if i := slices.IndexFunc(b.more, func(c columns.Column) bool { return c.Path == prop }); i >= 0 {
		title = b.more[i].Title
		b.more = slices.Delete(b.more, i, i+1)
	}
	cols.Content = append(cols.Content, flow(mapping("title", str(title), "value", str(prop), "hidden", boolean(true))))
	return title
}

// buildRead makes one read's source and component.
// buildRead makes one read's source and component, or says why it can't.
func buildRead(ctx context.Context, s *spec, p *preset, r *read, title string) (*built, string, error) {
	o := r.op
	// Lists are named for what they hold; a detail for its one item
	// (titled from the row once its push is wired); a singleton by its
	// summary.
	var label string
	switch r.kind {
	case kindList, kindSubList:
		label = words(lastLiteral(o.path))
	case kindDetail:
		label = singularTitle(o.path)
	default:
		label = strings.TrimSpace(o.op.Summary)
		if label == "" {
			label = words(lastLiteral(o.path))
		}
	}
	h := s.hint(o)
	if err := s.checkKind(h, r.kind); err != nil {
		return nil, "", err
	}
	b := &built{read: r, label: label, title: label, describe: o.String()}
	if len(o.op.Tags) > 0 {
		b.group = o.op.Tags[0]
	}
	src := mapping("extends", str(p.name))
	url := o.path
	if r.param != "" {
		url = strings.Replace(url, "{"+r.param+"}", "${params."+r.param+"}", 1)
	}
	if r.orphan != "" {
		key := p.env + "_" + envName(snake(r.orphan))
		url = strings.Replace(url, "{"+r.orphan+"}", "${env."+key+"}", 1)
		for _, prm := range params(o) {
			if prm.In == "path" && prm.Name == r.orphan {
				b.prompts = append(b.prompts, promptFor(key, &openapi3.Parameter{Name: prm.Name, Schema: prm.Schema, Description: prm.Description, Required: true}))
			}
		}
		if len(b.prompts) == 0 {
			b.prompts = append(b.prompts, promptFor(key, &openapi3.Parameter{Name: r.orphan, Required: true}))
		}
	}
	if servers := opServers(o); len(servers) > 0 {
		base, err := serverURL(s, servers[0])
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", o, err)
		}
		url = base + url
	}
	add(src, "url", str(url))
	if r.param != "" {
		add(src, "parameters", mapping(r.param, flow(mapping("required", boolean(true), "segment", boolean(true)))))
	}
	if r.root != "" {
		add(src, "root", str(r.root))
	}
	query := flow(mapping())
	for _, prm := range params(o) {
		if !prm.Required || prm.In != "query" {
			continue
		}
		key := p.env + "_" + envName(snake(prm.Name))
		add(query, prm.Name, str("${env."+key+"}"))
		b.prompts = append(b.prompts, promptFor(key, prm))
	}
	if len(query.Content) > 0 {
		add(src, "query", query)
	}
	unset, auth, comment, err := p.opAuth(ctx, s, o, title)
	if err != nil {
		return nil, "", err
	}
	switch {
	case unset:
		keyed(src, "unset", flow(strs([]string{"auth"})), "This operation takes no auth.")
	case auth != nil:
		keyed(src, "auth", auth, strings.TrimSpace("This operation uses another scheme.\n"+comment))
	case comment != "":
		src.Content[0].HeadComment = comment
	}
	b.source = src

	switch r.kind {
	case kindList, kindSubList:
		fs := schemaFields(r.item)
		if len(fs) == 0 {
			return nil, "the rows have no plain fields to show (every field is an object or a list)", nil
		}
		ranked := rank(fs, identityOf(fs, lastLiteral(o.path)))
		plain := make([]column, len(ranked))
		for i, c := range ranked {
			plain[i] = column{path: c.Path, title: c.Title, enum: c.Values}
		}
		pg := pagingFor(o, plain)
		kept, rest, err := s.pickColumns(h, ranked, r.name)
		if err != nil {
			return nil, "", err
		}
		if err := s.applyPaging(h, &pg, kept, r.name); err != nil {
			return nil, "", err
		}
		switch {
		case pg.window != nil:
			add(src, "window", pg.window)
		case pg.paginate != nil:
			add(src, "paginate", pg.paginate)
		}
		b.note, b.walks = pg.note, pg.walk
		// Off a window every column sorts what the table holds; on one,
		// sort_fields decides, and enum columns list their values.
		b.sortable, b.onWind = pg.window == nil, pg.window != nil
		b.more = rest
		styles := columns.Styles(kept)
		cols := seq()
		for i, c := range kept {
			cols.Content = append(cols.Content, columnNode(c, styles[i], b.sortable, b.onWind))
		}
		filterable := pg.window == nil || pg.filterable
		b.comp = mapping("type", str("table"), "title", str(label), "source", str(r.name))
		if filterable {
			add(b.comp, "filterable", boolean(true))
		}
		key := markKey(r.item, fs, lastLiteral(o.path))
		if pg.anchored && key == "" {
			key = identityOf(fs, lastLiteral(o.path))
		}
		switch {
		case pg.anchored && key == "":
			return nil, "", s.hintErr(h, "anchored", "a table over an anchored window needs each row's id, and the rows have none")
		case key != "" && (pg.window == nil || pg.anchored):
			// Marks hold by key: off a window, or on a span table.
			add(b.comp, "markable", boolean(true))
			add(b.comp, "mark_key", str(key))
			b.markable = !pg.anchored
		}
		add(b.comp, "columns", cols)
		if r.orphan != "" {
			b.label = words(strings.Join(literalsAround(o.path, r.orphan), " "))
			b.title = b.label
			get(b.comp, "title").Value = b.label
		}
		s.applyScreen(h, b)
	default:
		b.comp = mapping("type", str("inspector"), "title", str(label), "source", str(r.name), "auto", boolean(true))
		if rules := inspectorRules(unwrapNull(responseSchema(o.op))); rules != nil {
			add(b.comp, "color_rules", rules)
		}
		s.applyScreen(h, b)
	}
	return b, "", nil
}

// markKey is the row's id, for marking rows off a window: id or
// <singular>_id, when the schema requires it.
func markKey(item *openapi3.Schema, fs []field, listSeg string) string {
	if item == nil || !isObject(item) {
		return ""
	}
	for _, want := range []string{"id", singular(listSeg) + "_id"} {
		if slices.Contains(item.Required, want) && slices.ContainsFunc(fs, func(f field) bool { return f.path == want && !f.list }) {
			return want
		}
	}
	return ""
}

// promptFor asks for a required query parameter at startup.
func promptFor(key string, prm *openapi3.Parameter) *yaml.Node {
	pr := mapping("key", str(key), "label", str(words(prm.Name)))
	if prm.Schema != nil && prm.Schema.Value != nil {
		sch := unwrapNull(prm.Schema.Value)
		switch {
		case len(sch.Enum) > 0:
			var opts []string
			for _, e := range sch.Enum {
				opts = append(opts, fmt.Sprint(e))
			}
			add(pr, "options", flow(strs(opts)))
		case sch.Type.Is("integer"):
			add(pr, "type", str("int"))
		case sch.Type.Is("boolean"):
			add(pr, "type", str("bool"))
		}
	}
	if d := strings.TrimSpace(prm.Description); d != "" {
		add(pr, "description", str(strings.SplitN(d, "\n", 2)[0]))
	}
	if get(pr, "type") == nil || get(pr, "type").Value != "bool" {
		add(pr, "required", boolean(true))
	}
	return pr
}

// nameColumn is the title of a table's name-like column, or "".
func nameColumn(table *yaml.Node) string {
	for _, want := range []string{"name", "title", "display_name", "displayName", "full_name", "login", "label"} {
		for _, c := range get(table, "columns").Content {
			if get(c, "value").Value == want && get(c, "hidden") == nil {
				return get(c, "title").Value
			}
		}
	}
	return ""
}

// singularTitle is the item a detail path shows: /things/{id} is "Thing".
func singularTitle(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if !strings.HasPrefix(segs[i], "{") {
			return words(singular(segs[i]))
		}
	}
	return "Item"
}

// literalsAround names an orphan list from its path: /orgs/{org}/members
// is "Org members".
func literalsAround(path, param string) []string {
	var out []string
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segs {
		if strings.HasPrefix(s, "{") {
			continue
		}
		if i+1 < len(segs) && segs[i+1] == "{"+param+"}" {
			s = singular(s)
		}
		out = append(out, s)
	}
	return out
}
