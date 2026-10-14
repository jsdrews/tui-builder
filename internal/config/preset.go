package config

// Presets: named, partial leaves under `data.presets` that sources
// `extends:` to share settings (a base URL, headers, parameters).
// A preset produces nothing; resolvePresets folds each one into the
// sources that extend it, at load, before pipes expand or anything
// validates. After that, the rest of the code sees ordinary sources.
//
// The merge rule (a source over its preset), from plans/http-source.md:
//   - single values and lists: the source's replaces the preset's
//   - string maps (headers, env): merged key by key; "" drops a key
//   - structured blocks (paginate, window, …): replaced whole
//   - url: a relative source url is appended to the preset's path
//   - parameters: the union, the source's declaration winning
//   - unset: [field, …] drops inherited fields

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// resolvePresets merges every preset into the sources that extend it,
// replacing each such source in d.Sources with the merged result, and
// each preset in d.Presets with its own chain resolved.
func resolvePresets(d *DataBlock) error {
	names := sortedKeys(d.Presets)
	for _, name := range names {
		p := d.Presets[name]
		path := "data.presets." + name
		if p == nil {
			return fmt.Errorf("%s: empty preset", path)
		}
		if _, clash := d.Sources[name]; clash {
			return fmt.Errorf("%s: a source is also named %q — presets and sources share one namespace", path, name)
		}
		if len(p.Pipe) > 0 {
			return fmt.Errorf("%s: pipe: isn't allowed in a preset — a preset is never a pipeline", path)
		}
		if p.Type != "" && !leafTypes[p.Type] {
			return fmt.Errorf("%s: type %q isn't a leaf kind — a preset is never a pipeline", path, p.Type)
		}
	}

	resolved := map[string]*Source{}
	var resolve func(name string, stack []string) (*Source, error)
	resolve = func(name string, stack []string) (*Source, error) {
		if r, ok := resolved[name]; ok {
			return r, nil
		}
		for i, s := range stack {
			if s == name {
				return nil, fmt.Errorf("data.presets.%s: extends cycle %s", stack[0], strings.Join(append(stack[i:], name), " → "))
			}
		}
		p := d.Presets[name]
		var r *Source
		if p.Extends == "" {
			r = p.Clone()
			r.Unset = nil
			if len(p.Unset) > 0 {
				return nil, fmt.Errorf("data.presets.%s: unset: needs extends: — there's nothing to inherit", name)
			}
			r.Inherited = map[string]string{}
			for _, f := range setFields(r) {
				r.Inherited[f] = name
			}
		} else {
			if _, ok := d.Presets[p.Extends]; !ok {
				return nil, unknownPreset("data.presets."+name, p.Extends, d.Presets)
			}
			parent, err := resolve(p.Extends, append(stack, name))
			if err != nil {
				return nil, err
			}
			if r, err = mergeOverPreset(parent, p, p.Extends, "data.presets."+name); err != nil {
				return nil, err
			}
			// What it didn't take from its parent is its own.
			for _, f := range setFields(r) {
				if _, ok := r.Inherited[f]; !ok {
					r.Inherited[f] = name
				}
			}
		}
		resolved[name] = r
		return r, nil
	}
	for _, name := range names {
		if _, err := resolve(name, nil); err != nil {
			return err
		}
	}
	// Keep the resolved presets, so wrangl --describe can show one as
	// a source extending it would inherit it.
	for name, r := range resolved {
		d.Presets[name] = r
	}

	for _, name := range sortedKeys(d.Sources) {
		s := d.Sources[name]
		if s == nil {
			continue
		}
		path := "data.sources." + name
		for i, stage := range s.Pipe {
			if stage.Extends != "" {
				return fmt.Errorf("%s.pipe[%d]: extends: only works on a source, not a pipe stage", path, i)
			}
		}
		for _, up := range s.Upstreams() {
			if _, ok := d.Presets[up]; ok {
				return fmt.Errorf("%s: upstream %q is a preset, which produces nothing — read a source that extends it", path, up)
			}
		}
		if s.Extends == "" {
			if len(s.Unset) > 0 {
				return fmt.Errorf("%s: unset: needs extends: — there's nothing to inherit", path)
			}
			continue
		}
		parent, ok := resolved[s.Extends]
		if !ok {
			return unknownPreset(path, s.Extends, d.Presets)
		}
		merged, err := mergeOverPreset(parent, s, s.Extends, path)
		if err != nil {
			return err
		}
		if !merged.IsLeaf() {
			return fmt.Errorf("%s: type %q can't extend a preset — only leaf sources can", path, merged.Type)
		}
		d.Sources[name] = merged
	}
	return nil
}

// mergeOverPreset returns child laid over its resolved preset parent.
// The result's Inherited maps each field taken from the preset to the
// preset that set it; ExtendsChain lists the presets, nearest first.
func mergeOverPreset(parent, child *Source, parentName, path string) (*Source, error) {
	out := parent.Clone()
	out.Inherited = map[string]string{}

	// unset: drops inherited fields before the child's own are laid on.
	for _, f := range child.Unset {
		if !unsettable(f) {
			return nil, unknownField(path+".unset", f, unsettableFields())
		}
		fieldByYAML(out, f).SetZero()
	}

	if child.Type != "" && out.Type != "" && child.Type != out.Type {
		return nil, fmt.Errorf("%s: type %q doesn't match preset %s's %q", path, child.Type, parentName, out.Type)
	}
	if child.Type != "" {
		out.Type = child.Type
	}
	out.URL = joinPresetURL(out.URL, child.URL)

	if len(child.Parameters) > 0 {
		params := make(map[string]*Parameter, len(out.Parameters)+len(child.Parameters))
		for k, v := range out.Parameters {
			params[k] = v
		}
		for k, v := range child.Parameters {
			params[k] = v
		}
		out.Parameters = params
	}

	ov, cv := reflect.ValueOf(out).Elem(), reflect.ValueOf(child).Elem()
	t := ov.Type()
	for i := 0; i < t.NumField(); i++ {
		name := yamlName(t.Field(i))
		switch name {
		case "", "type", "url", "parameters", "extends", "unset":
			continue
		}
		c := cv.Field(i)
		if c.IsZero() {
			continue
		}
		o := ov.Field(i)
		if isStringMap(c.Type()) && !o.IsZero() {
			merged := reflect.MakeMap(c.Type())
			for _, src := range []reflect.Value{o, c} {
				for it := src.MapRange(); it.Next(); {
					merged.SetMapIndex(it.Key(), it.Value())
				}
			}
			for it := c.MapRange(); it.Next(); {
				if dropsKey(it.Value()) {
					merged.SetMapIndex(it.Key(), reflect.Value{})
				}
			}
			o.Set(merged)
			continue
		}
		o.Set(c)
	}

	// What's set but wasn't set by the child came from the preset.
	for _, f := range setFields(out) {
		if !fieldByYAML(child, f).IsZero() {
			continue
		}
		if origin, ok := parent.Inherited[f]; ok {
			out.Inherited[f] = origin
		} else {
			out.Inherited[f] = parentName
		}
	}

	out.Extends = child.Extends
	out.Unset = nil
	out.ExtendsChain = append([]string{parentName}, parent.ExtendsChain...)
	return out.Clone(), nil
}

// isStringMap reports a map that merges key by key: string keys, and
// string or QueryValue values (headers, env, query, form).
func isStringMap(t reflect.Type) bool {
	return t.Kind() == reflect.Map && t.Key().Kind() == reflect.String &&
		(t.Elem().Kind() == reflect.String || t.Elem() == reflect.TypeOf(QueryValue(nil)))
}

// dropsKey reports a map value that removes its key in a merge: "".
func dropsKey(v reflect.Value) bool {
	if q, ok := v.Interface().(QueryValue); ok {
		return q.empty()
	}
	return v.String() == ""
}

// joinPresetURL joins a source url onto its preset's. An absolute url
// (one with a scheme, or one that starts with a token such as
// ${env.BASE}) replaces the preset's. A relative one is appended to the
// preset's path with exactly one "/" between them — OpenAPI's rule, not
// RFC 3986's, so a /v1 base path survives. Query strings written in
// both merge, the source's winning on a shared key.
func joinPresetURL(base, rel string) string {
	switch {
	case rel == "":
		return base
	case base == "", strings.Contains(rel, "://"), strings.HasPrefix(rel, "${"):
		return rel
	}
	bPath, bQuery, _ := strings.Cut(base, "?")
	rPath, rQuery, _ := strings.Cut(rel, "?")
	path := bPath
	if rPath != "" {
		path = strings.TrimRight(bPath, "/") + "/" + strings.TrimLeft(rPath, "/")
	}
	if q := mergeRawQuery(bQuery, rQuery); q != "" {
		return path + "?" + q
	}
	return path
}

// mergeRawQuery merges two raw (still templated) query strings: every
// key rel sets replaces all of base's pairs for that key. Pairs keep
// their order and their encoding.
func mergeRawQuery(base, rel string) string {
	if base == "" || rel == "" {
		return base + rel
	}
	key := func(pair string) string { k, _, _ := strings.Cut(pair, "="); return k }
	relKeys := map[string]bool{}
	for _, pair := range strings.Split(rel, "&") {
		relKeys[key(pair)] = true
	}
	var out []string
	for _, pair := range strings.Split(base, "&") {
		if pair != "" && !relKeys[key(pair)] {
			out = append(out, pair)
		}
	}
	for _, pair := range strings.Split(rel, "&") {
		if pair != "" {
			out = append(out, pair)
		}
	}
	return strings.Join(out, "&")
}

// setFields lists the YAML names of s's non-zero fields.
func setFields(s *Source) []string {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	var out []string
	for i := 0; i < t.NumField(); i++ {
		name := yamlName(t.Field(i))
		if name == "" || name == "extends" || name == "unset" {
			continue
		}
		if !v.Field(i).IsZero() {
			out = append(out, name)
		}
	}
	return out
}

// fieldByYAML returns the settable field of s whose YAML name is name.
func fieldByYAML(s *Source, name string) reflect.Value {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if yamlName(t.Field(i)) == name {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}

// unsettableFields are the fields `unset:` may name: every source field
// a preset can set.
func unsettableFields() []string {
	var out []string
	for _, f := range yamlFields(reflect.TypeOf(Source{})) {
		switch f {
		case "type", "extends", "unset", "pipe":
			continue
		}
		out = append(out, f)
	}
	return out
}

func unsettable(name string) bool {
	for _, f := range unsettableFields() {
		if f == name {
			return true
		}
	}
	return false
}

func unknownPreset(path, name string, presets map[string]*Source) error {
	msg := fmt.Sprintf("%s: extends %q, which isn't a preset under data.presets", path, name)
	if s := suggest(name, sortedKeys(presets)); s != "" {
		msg += fmt.Sprintf(" (did you mean %s?)", s)
	}
	return fmt.Errorf("%s", msg)
}

// presetHint explains a name that isn't a source because it's a preset.
func presetHint(presets map[string]*Source, name string) string {
	if _, ok := presets[name]; ok {
		return fmt.Sprintf(" (%q is a preset, which produces nothing — use a source that extends it)", name)
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
