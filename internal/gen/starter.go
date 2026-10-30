package gen

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// starter is a starter overrides file: every generated operation's key,
// commented out, with the hints it could take and its current title,
// group and columns, so the vocabulary is there to uncomment.
func starter(s *spec, opts Options, screens []*built, actions *yaml.Node) []byte {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Overrides for `tui-builder gen`, merged in every time it runs:")
	w("#   tui-builder gen %s --overrides <this file> -o <config> --force", s.name)
	w("#")
	w("# gen: hints are keyed by the spec's own names, so they survive")
	w("# regenerating. Any other key (app, data, actions, tui) is config merged")
	w("# onto the output, keyed by the generated names. Uncomment what you need.")
	w("gen:")
	w("  # name: %s", firstNonEmpty(opts.Doc.Name, snake(apiTitle(s.doc, ""))))
	w("  # layout: tabs            # or menu")
	w("  # base_url: https://…")
	w("  # order: [%s]", strings.Join(topKeys(screens), ", "))
	w("  operations:")
	for _, sc := range screens {
		w("    # %q:", sc.op.String())
		if sc.topLevel() {
			w("    #   title: %s", sc.label)
			if sc.group != "" {
				w("    #   group: %s", sc.group)
			}
			w("    #   hide: true")
		} else {
			w("    #   title: %s", sc.title)
		}
		if cols := get(sc.comp, "columns"); cols != nil {
			var paths []string
			for _, c := range cols.Content {
				if v := get(c, "value"); v != nil && get(c, "hidden") == nil {
					paths = append(paths, v.Value)
				}
			}
			w("    #   columns: [%s]", strings.Join(paths, ", "))
		}
		switch {
		case sc.onWind:
			w("    #   window: {search_param: name__icontains, filter_ops: {contains: \"${column.value}__icontains\"}}")
			if get(get(sc.source, "window"), "sort_fields") != nil {
				w("    #   sort_refuse: [a_field_the_server_won't_sort_by]")
			}
		case sc.walks:
			w("    #   max_pages: 20")
		}
		w("    #   skip: true")
	}
	for i := 0; i+1 < len(actions.Content); i += 2 {
		act := actions.Content[i+1]
		if get(act, "push") != nil {
			continue
		}
		method, url := get(act, "method"), get(act, "url")
		if method == nil || url == nil {
			continue
		}
		w("    # %q:   # %s", method.Value+" "+specPath(url.Value), actions.Content[i].Value)
		w("    #   confirm: true        # or false, or the question to ask")
	}
	w("")
	w("# Config merged on top, e.g. where a secret comes from:")
	w("# app:")
	w("#   env:")
	w("#     - {name: %s_TOKEN, from: {command: [pass, show, %s/token]}}", envName(firstNonEmpty(opts.Doc.Name, snake(apiTitle(s.doc, "")))), kebab(firstNonEmpty(opts.Doc.Name, snake(apiTitle(s.doc, "")))))
	return []byte(b.String())
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// topKeys are the top-level screens' operation keys, quoted.
func topKeys(screens []*built) []string {
	var out []string
	for _, sc := range screens {
		if sc.topLevel() {
			out = append(out, fmt.Sprintf("%q", sc.op.String()))
		}
	}
	return out
}

// specPath turns an action url back into the spec's path:
// /things/${inputs.thing_id} is /things/{thing_id}.
func specPath(url string) string {
	for {
		i := strings.Index(url, "${inputs.")
		if i < 0 {
			return url
		}
		j := strings.Index(url[i:], "}")
		url = url[:i] + "{" + url[i+len("${inputs."):i+j] + "}" + url[i+j+1:]
	}
}
