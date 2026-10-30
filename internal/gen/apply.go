package gen

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/columns"
)

// listOnly are the hints that change how a list is fetched or shown.
func (h *opHints) listOnly() []string {
	var out []string
	if h.MaxPages != 0 {
		out = append(out, "max_pages")
	}
	if len(h.Columns) > 0 {
		out = append(out, "columns")
	}
	if isSet(h.Window) {
		out = append(out, "window")
	}
	if len(h.SortRefuse) > 0 {
		out = append(out, "sort_refuse")
	}
	if isSet(h.Anchored) {
		out = append(out, "anchored")
	}
	return out
}

// hintErr is an error about one operation's hint.
func (s *spec) hintErr(h *opHints, field, format string, a ...any) error {
	return fmt.Errorf("%s: gen.operations.%s.%s: %s", s.ov.path, h.key, field, fmt.Sprintf(format, a...))
}

// checkKind refuses hints that don't apply to what an operation became.
func (s *spec) checkKind(h *opHints, k kind) error {
	if k != kindList && k != kindSubList {
		if f := h.listOnly(); len(f) > 0 {
			return s.hintErr(h, f[0], "only a list takes it, and this is a %s", map[kind]string{kindDetail: "detail", kindSingleton: "singleton"}[k])
		}
	}
	if isSet(h.Confirm) {
		return s.hintErr(h, "confirm", "only a write takes it")
	}
	if (h.Hide || h.Group != "") && k != kindList && k != kindSingleton {
		return s.hintErr(h, "hide", "only a list or singleton is on the menu or tabs; this one is reached by a push")
	}
	return nil
}

// pickColumns is the columns hint: those fields, in that order, in place
// of the ranked top 8. The rest stay commented out.
func (s *spec) pickColumns(h *opHints, ranked []columns.Column, name string) ([]columns.Column, []columns.Column, error) {
	if len(h.Columns) == 0 {
		kept, rest := columns.Pick(ranked, columns.DefaultMax)
		return kept, rest, nil
	}
	var paths []string
	for _, c := range ranked {
		paths = append(paths, c.Path)
	}
	var kept []columns.Column
	for _, want := range h.Columns {
		i := slices.Index(paths, want)
		if i < 0 {
			return nil, nil, s.hintErr(h, "columns", "the rows have no field %s%s", want, suggest(want, paths))
		}
		kept = append(kept, ranked[i])
	}
	var rest []columns.Column
	for _, c := range ranked {
		if !slices.Contains(h.Columns, c.Path) {
			rest = append(rest, c)
		}
	}
	slices.SortStableFunc(rest, func(a, b columns.Column) int { return int(a.Rank) - int(b.Rank) })
	s.set("tui", "components", name, "columns")
	return kept, rest, nil
}

// applyPaging is the max_pages, window, sort_refuse and anchored hints.
// kept are the columns being written, for filter_except.
func (s *spec) applyPaging(h *opHints, pg *paging, kept []columns.Column, name string) error {
	src := []string{"data", "sources", name}
	if h.MaxPages != 0 {
		if pg.paginate == nil {
			return s.hintErr(h, "max_pages", "the list doesn't walk its pages, so there's no max_pages to set")
		}
		get(pg.paginate, "max_pages").Value = fmt.Sprint(h.MaxPages)
		s.set(append(src, "paginate", "max_pages")...)
	}
	if len(h.SortRefuse) > 0 {
		fields := get2(pg.window, "sort_fields")
		if fields == nil {
			return s.hintErr(h, "sort_refuse", "the list has no server sort to refuse")
		}
		for _, f := range h.SortRefuse {
			f = strings.TrimPrefix(f, "-")
			i := slices.IndexFunc(fields.Content, func(n *yaml.Node) bool { return n.Value == f })
			if i < 0 {
				var have []string
				for _, n := range fields.Content {
					have = append(have, n.Value)
				}
				return s.hintErr(h, "sort_refuse", "%s isn't one of the sort fields (%s)", f, strings.Join(have, ", "))
			}
			fields.Content = slices.Delete(fields.Content, i, i+1)
			delete(pg.sorted, f)
			if sorts := get(pg.window, "sorts"); sorts != nil {
				for j := 0; j+1 < len(sorts.Content); j += 2 {
					if sorts.Content[j+1].Value == f {
						sorts.Content = slices.Delete(sorts.Content, j, j+2)
						break
					}
				}
			}
		}
		if len(fields.Content) == 0 {
			dropKeys(pg.window, "sort_param", "sort_fields", "sort_desc_prefix", "sorts")
		}
		s.set(append(src, "window", "sort_fields")...)
	}
	if isSet(h.Anchored) {
		if pg.window == nil {
			return s.hintErr(h, "anchored", "only a list the spec pages can walk from an edge; this one isn't paged")
		}
		if h.Anchored.Kind != yaml.MappingNode || get(&h.Anchored, "cursor") == nil {
			return s.hintErr(h, "anchored", "needs at least cursor: (each item's cursor), plus older: and newer:")
		}
		// An anchored window has no offsets, total or user sort.
		dropKeys(pg.window, "page_param", "page_start", "offset_param", "total_path", "sort_param", "sort_fields", "sort_desc_prefix", "sorts", "prefetch")
		var touched [][]string
		if err := merge(pg.window, &h.Anchored, nil, &touched); err != nil {
			return s.hintErr(h, "anchored", "%v", err)
		}
		pg.sorted, pg.anchored = map[string]bool{}, true
		s.set(append(src, "window")...)
	}
	if isSet(h.Window) {
		if pg.window == nil {
			return s.hintErr(h, "window", "the list isn't a window, so there's nothing to merge into (it walks, or is one request)")
		}
		if h.Window.Kind != yaml.MappingNode {
			return s.hintErr(h, "window", "should be a mapping of window fields")
		}
		var touched [][]string
		if err := merge(pg.window, &h.Window, nil, &touched); err != nil {
			return s.hintErr(h, "window", "%v", err)
		}
		for _, t := range touched {
			s.set(append(append(slices.Clone(src), "window"), t...)...)
			if t[0] == "search_param" || t[0] == "filter_ops" || t[0] == "filters" {
				pg.filterable = true
			}
		}
		// filter_ops needs filter_except: every column it can't match
		// on, which the schema says (numbers, booleans, enums, ids,
		// timestamps).
		if get(pg.window, "filter_ops") != nil && get(&h.Window, "filter_except") == nil {
			var except []string
			for _, c := range kept {
				if c.Rank == columns.Status || c.Rank == columns.Time || c.Rank == columns.Number || c.Path == "id" || strings.HasSuffix(c.Path, "_id") {
					except = append(except, c.Title)
				}
			}
			if len(except) > 0 {
				dropKeys(pg.window, "filter_except")
				add(pg.window, "filter_except", flow(strs(except)))
			}
		}
	}
	return nil
}

// get2 is get on a node that may be nil.
func get2(m *yaml.Node, k string) *yaml.Node {
	if m == nil {
		return nil
	}
	return get(m, k)
}

func dropKeys(m *yaml.Node, keys ...string) {
	for i := 0; i+1 < len(m.Content); {
		if slices.Contains(keys, m.Content[i].Value) {
			m.Content = slices.Delete(m.Content, i, i+2)
			continue
		}
		i += 2
	}
}

// applyScreen is the title, group and hide hints.
func (s *spec) applyScreen(h *opHints, b *built) {
	if h.Title != "" {
		b.label, b.title = h.Title, h.Title
		if t := get(b.comp, "title"); t != nil {
			t.Value = h.Title
		}
		s.set("tui", "components", b.name, "title")
	}
	if h.Group != "" {
		b.group = h.Group
	}
	b.hidden = h.Hide
}
