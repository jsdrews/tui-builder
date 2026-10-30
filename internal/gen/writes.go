package gen

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// writeOp is a POST, PUT, PATCH or DELETE attached to a list: a collection
// action (POST /things) or an item action on the focused row
// (PATCH /things/{id}, POST /things/{id}/restart).
type writeOp struct {
	op    op
	list  *built
	param string // the path parameter the row fills; "" for a collection action
}

// attachWrites maps each write to an action bound on its list's screen,
// and says why the rest are skipped.
func attachWrites(ctx context.Context, s *spec, p *preset, writes []op, screens []*built, actions *yaml.Node, title string) ([]skip, error) {
	lists := map[string]*built{}
	for _, b := range screens {
		if b.kind == kindList {
			path := b.op.path
			if b.orphan != "" {
				path = strings.Replace(path, "{"+b.orphan+"}", "{}", 1)
			}
			lists[path] = b
		}
	}
	var skips []skip
	taken := map[string]bool{}
	for i := 0; i+1 < len(actions.Content); i += 2 {
		taken[actions.Content[i].Value] = true
	}
	for _, o := range writes {
		h := s.ov.hint(o)
		if h.Title != "" || h.Group != "" || h.Hide || h.Root != "" || len(h.listOnly()) > 0 {
			return nil, fmt.Errorf("%s: gen.operations.%s: a write takes only confirm: (and skip:)", s.ov.path, h.key)
		}
		w, why := placeWrite(o, lists)
		if why == "" {
			why = w.attach(ctx, s, p, actions, taken, title)
		}
		if why != "" {
			skips = append(skips, skip{o, why})
		}
	}
	return skips, nil
}

// placeWrite finds the list a write belongs to: its own path (a
// collection action) or the list's path plus one parameter and any
// literal segments (an item action).
func placeWrite(o op, lists map[string]*built) (writeOp, string) {
	segs := strings.Split(strings.Trim(o.path, "/"), "/")
	// A collection action: the list's path exactly (an orphan's with its
	// startup parameter).
	if b := lists[o.path]; b != nil {
		return writeOp{op: o, list: b}, ""
	}
	if b := lists[orphanKey(o.path)]; b != nil && b.orphan != "" {
		return writeOp{op: o, list: b}, ""
	}
	for i, seg := range segs {
		if !strings.HasPrefix(seg, "{") {
			continue
		}
		b := lists["/"+strings.Join(segs[:i], "/")]
		if b == nil || b.orphan != "" {
			break
		}
		for _, rest := range segs[i+1:] {
			if strings.HasPrefix(rest, "{") {
				return writeOp{}, "needs a second path parameter, and a row fills only one: add it in overrides"
			}
		}
		return writeOp{op: o, list: b, param: strings.Trim(seg, "{}")}, ""
	}
	if strings.Contains(o.path, "{") {
		return writeOp{}, missingList(o.path)
	}
	return writeOp{}, "no list to attach it to"
}

// orphanKey is a path with its one parameter blanked, to match an
// orphan list.
func orphanKey(path string) string {
	return regexp.MustCompile(`\{[^}]*\}`).ReplaceAllString(path, "{}")
}

// attach builds the action and its binding, or says why it can't.
func (w writeOp) attach(ctx context.Context, s *spec, p *preset, actions *yaml.Node, taken map[string]bool, title string) string {
	o, b := w.op, w.list
	body, kind, why := requestBody(o.op)
	if why != "" {
		return why
	}
	listSeg := lastLiteral(b.op.path)
	thing := singular(listSeg)
	item := w.param != ""
	extra := strings.TrimPrefix(o.path, b.op.path)
	if item {
		extra = strings.TrimPrefix(extra, "/{"+w.param+"}")
	}
	verb := ""
	switch {
	case o.method == "POST" && !item:
		verb = "create"
	case o.method == "PATCH" && item && extra == "":
		verb = "update"
	case o.method == "PUT" && item && extra == "":
		verb = "replace"
	case o.method == "DELETE" && item && extra == "":
		verb = "delete"
	}
	name := snake(verb + "_" + thing)
	label := map[string]string{"create": "", "update": "Edit", "replace": "Replace", "delete": "Delete"}[verb]
	if verb == "" {
		name = snake(strings.TrimSpace(o.op.Summary))
		if name == "api" || o.op.Summary == "" {
			name = snake(o.op.OperationID)
		}
		if name == "api" {
			name = snake(o.method + "_" + strings.ReplaceAll(o.path, "/", "_"))
		}
	}
	if label == "" {
		label = strings.TrimSpace(o.op.Summary)
		if label == "" {
			label = titled(name)
		}
	}
	for base, n := name, 2; taken[name]; n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}

	act := mapping("extends", str(p.name), "method", str(o.method))
	url := o.path
	if b.orphan != "" {
		url = strings.Replace(url, "{"+b.orphan+"}", "${env."+p.env+"_"+envName(snake(b.orphan))+"}", 1)
	}
	inputs := mapping()
	var notes []string
	order := 0
	nextOrder := func() *yaml.Node { order++; return intNode(order) }
	binding := mapping()
	if item {
		prop := idProperty(b.item, w.param, listSeg)
		if prop == "" {
			return fmt.Sprintf("can't tell which column of %s fills {%s}", b.name, w.param)
		}
		col := ensureColumn(b, prop)
		url = strings.Replace(url, "{"+w.param+"}", "${inputs."+w.param+"}", 1)
		add(inputs, w.param, flow(mapping("required", boolean(true), "segment", boolean(true))))
		add(binding, "from", str(b.name))
		add(binding, "bind", flow(mapping(w.param, str("${selection."+col+"}"))))
	}
	add(act, "url", str(url))

	// Query parameters are asked for, and sent in query:.
	query := flow(mapping())
	for _, prm := range params(o) {
		if prm.In != "query" {
			continue
		}
		in := inputFor(prm.Name, prm.Schema, prm.Required, prm.Description, prm.Example)
		add(in, "order", nextOrder())
		keyed(inputs, prm.Name, flow(in), workaround(prm.Schema, prm.Required))
		add(query, prm.Name, str("${inputs."+prm.Name+"}"))
	}
	if len(query.Content) > 0 {
		add(act, "query", query)
	}

	// The body's fields are asked for. An edit asks every one blank and
	// optional, so a PATCH sends only what was typed.
	if body != nil {
		fields := mapping()
		order, props := properties(body)
		for _, n := range order {
			f := props[n]
			required := slices.Contains(body.Required, n)
			switch {
			case f.ReadOnly:
				continue
			case !isScalar(f):
				what := "an object"
				if f.Type.Is("array") {
					what = "a list"
				}
				if required && verb != "update" {
					return fmt.Sprintf("`%s` is %s, which an input can't fill: add it in overrides", n, what)
				}
				notes = append(notes, fmt.Sprintf("%s is %s, so it isn't asked for: add it in overrides.", n, what))
				continue
			}
			if verb == "update" {
				required = false
			}
			in := inputFor(n, &openapi3.SchemaRef{Value: f}, required, f.Description, f.Example)
			add(in, "order", nextOrder())
			key := n
			if get(inputs, key) != nil {
				key = "body_" + n
			}
			keyed(inputs, key, flow(in), workaround(&openapi3.SchemaRef{Value: f}, required))
			add(fields, n, str("${inputs."+key+"}"))
		}
		if len(fields.Content) > 0 {
			add(act, kind, fields)
		}
	}
	if len(inputs.Content) > 0 {
		keyed(act, "inputs", inputs, strings.Join(notes, "\n"))
	}

	// What it says when it's done. There's no refresh after a writeOp yet,
	// so every message says how to get one.
	msg := map[string]string{"create": "created", "update": "updated", "replace": "replaced", "delete": "deleted"}[verb]
	if msg == "" {
		msg = strings.ToLower(label) + ": done"
	}
	if verb == "create" {
		if id := responseIdentity(o.op); id != "" {
			msg += " ${body." + id + "}"
		}
	}
	add(act, "message", str(msg+" (r to refresh)"))
	multi := b.markable && item && (verb == "delete" || o.method == "POST" && body == nil)
	if multi {
		add(act, "multi", boolean(true))
	}
	if unset, auth, comment, err := p.opAuth(ctx, s, o, title); err == nil {
		switch {
		case unset:
			keyed(act, "unset", flow(strs([]string{"auth"})), "This operation takes no auth.")
		case auth != nil:
			keyed(act, "auth", auth, strings.TrimSpace("This operation uses another scheme.\n"+comment))
		}
	}
	add(actions, name, act)
	taken[name] = true

	want := map[string]string{"create": "n", "update": "e", "delete": "d"}[verb]
	full := mapping("key", str(b.takeKey(want, name)), "action", str(name), "label", str(label))
	if k := get(full, "key"); k.Value == "" {
		full.Content = full.Content[2:]
	}
	full.Content = append(full.Content, binding.Content...)
	ident := "${selection." + identityTitle(b) + "}"
	if verb == "delete" {
		add(full, "confirm", str("Delete "+ident+"?"))
	}
	// confirm: false drops a confirm, true adds one, a string is the
	// question.
	if h := s.hint(o); isSet(h.Confirm) {
		dropKeys(full, "confirm")
		switch v := h.Confirm.Value; {
		case h.Confirm.Tag == "!!bool" && v == "false":
		case h.Confirm.Tag == "!!bool" && item:
			add(full, "confirm", str(label+" "+ident+"?"))
		case h.Confirm.Tag == "!!bool":
			add(full, "confirm", str(label+"?"))
		default:
			add(full, "confirm", str(v))
		}
	}
	b.bindings = append(b.bindings, flow(full))
	b.writes++
	return ""
}

// requestBody is a write's body schema and how it's sent (json or form),
// or why it can't be: a file upload or a type gen doesn't send.
func requestBody(o *openapi3.Operation) (*openapi3.Schema, string, string) {
	if o.RequestBody == nil || o.RequestBody.Value == nil || len(o.RequestBody.Value.Content) == 0 {
		return nil, "", ""
	}
	content := o.RequestBody.Value.Content
	types := make([]string, 0, len(content))
	for ct := range content {
		types = append(types, ct)
	}
	sort.Strings(types)
	for _, ct := range types {
		mt := content[ct]
		switch {
		case isJSON(ct):
			if mt.Schema == nil || mt.Schema.Value == nil {
				return nil, "", ""
			}
			s := unwrapNull(mt.Schema.Value)
			if !isObject(s) {
				return nil, "", "the body isn't an object, so there are no fields to ask for"
			}
			return s, "json", ""
		case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
			if mt.Schema == nil || mt.Schema.Value == nil {
				return nil, "", ""
			}
			return unwrapNull(mt.Schema.Value), "form", ""
		}
	}
	for _, ct := range types {
		if strings.HasPrefix(ct, "multipart/") {
			return nil, "", "a multipart body (a file upload), which gen doesn't send"
		}
	}
	return nil, "", fmt.Sprintf("a %s body, which gen doesn't send", types[0])
}

// inputFor describes an input from a schema: its type, required,
// options for an enum, a check for its constraints, a mask for a
// password, and its description and example. A spec default goes in the
// placeholder, never default: (an input with a default is never asked).
func inputFor(name string, ref *openapi3.SchemaRef, required bool, description string, example any) *yaml.Node {
	in := mapping()
	var s *openapi3.Schema
	if ref != nil && ref.Value != nil {
		s = unwrapNull(ref.Value)
	}
	if s == nil {
		if required {
			add(in, "required", boolean(true))
		}
		return in
	}
	enum := enumValues(s)
	switch {
	case len(enum) > 0:
		if !required {
			enum = append([]string{""}, enum...) // leave it out
		}
		add(in, "options", flow(strs(enum)))
	case s.Type.Is("boolean"):
		if required {
			add(in, "type", str("bool"))
		} else {
			// A bool input is always true or false; blank leaves it out.
			add(in, "options", flow(strs([]string{"", "true", "false"})))
		}
	case s.Type.Is("integer"):
		add(in, "type", str("int"))
	}
	if required {
		add(in, "required", boolean(true))
	}
	if s.Format == "password" || s.WriteOnly {
		if len(enum) == 0 {
			add(in, "mask", boolean(true))
		}
	}
	if check, why := validation(s); check != "" {
		add(in, "validate", str(check))
		add(in, "invalid", str(why))
	}
	placeholder := ""
	if example != nil {
		placeholder = fmt.Sprint(example)
	} else if s.Example != nil {
		placeholder = fmt.Sprint(s.Example)
	}
	if s.Default != nil {
		placeholder = fmt.Sprintf("default: %v", s.Default)
	}
	if placeholder != "" && len(enum) == 0 && !s.Type.Is("boolean") {
		add(in, "placeholder", str(placeholder))
	} else if s.Default != nil && (len(enum) > 0 || s.Type.Is("boolean")) {
		// A select shows no placeholder, so its label says it.
		add(in, "label", str(fmt.Sprintf("%s (default: %v)", words(name), s.Default)))
	}
	d := strings.TrimSpace(description)
	if d == "" {
		d = strings.TrimSpace(s.Description)
	}
	if d != "" {
		add(in, "description", str(strings.SplitN(d, "\n", 2)[0]))
	}
	return in
}

// workaround is the comment on an input that stands in for a type the
// runtime's inputs don't have: an optional boolean (always true or
// false) is a select with a blank, and a non-integer number is text.
func workaround(ref *openapi3.SchemaRef, required bool) string {
	if ref == nil || ref.Value == nil {
		return ""
	}
	s := unwrapNull(ref.Value)
	switch {
	case s.Type.Is("boolean") && !required:
		return "Optional true/false: blank leaves it out. (A bool input is always one or the other.)"
	case s.Type.Is("number"):
		return "A number, typed as text: the server has to accept it as a string."
	}
	return ""
}

// validation is an expression for a schema's constraints, and what to
// say when it fails.
func validation(s *openapi3.Schema) (string, string) {
	var checks, says []string
	if s.Type.Is("string") {
		if s.MaxLength != nil {
			checks = append(checks, fmt.Sprintf("len(value) <= %d", *s.MaxLength))
			says = append(says, fmt.Sprintf("at most %d characters", *s.MaxLength))
		}
		if s.MinLength > 0 {
			checks = append(checks, fmt.Sprintf("len(value) >= %d", s.MinLength))
			says = append(says, fmt.Sprintf("at least %d characters", s.MinLength))
		}
		if s.Pattern != "" && !strings.Contains(s.Pattern, "'") {
			checks = append(checks, fmt.Sprintf("value matches '%s'", s.Pattern))
			says = append(says, "must match "+s.Pattern)
		}
	}
	if s.Type.Is("integer") {
		if s.Min != nil {
			checks = append(checks, fmt.Sprintf("int(value) >= %d", int(*s.Min)))
			says = append(says, fmt.Sprintf("at least %d", int(*s.Min)))
		}
		if s.Max != nil {
			checks = append(checks, fmt.Sprintf("int(value) <= %d", int(*s.Max)))
			says = append(says, fmt.Sprintf("at most %d", int(*s.Max)))
		}
	}
	return strings.Join(checks, " && "), strings.Join(says, ", ")
}

// responseIdentity is the field of a write's response that names what
// it made: a name-like field, else id.
func responseIdentity(o *openapi3.Operation) string {
	s := responseSchema(o)
	if s == nil || !isObject(s) {
		return ""
	}
	_, props := properties(unwrapNull(s))
	for _, want := range append(slices.Clone(nameLike), "id") {
		if p := props[want]; p != nil && isScalar(p) {
			return want
		}
	}
	return ""
}

// identityTitle is the title of a table's identity column: the bold one,
// else the first.
func identityTitle(b *built) string {
	cols := get(b.comp, "columns")
	for _, c := range cols.Content {
		if bold := get(c, "bold"); bold != nil && bold.Value == "true" {
			return get(c, "title").Value
		}
	}
	return get(cols.Content[0], "title").Value
}
