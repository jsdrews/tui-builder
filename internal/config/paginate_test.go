package config

import (
	"strings"
	"testing"
)

func TestPaginateErrors(t *testing.T) {
	cases := []struct{ name, paginate, extra, want string }{
		{"missing strategy", `{next_path: next}`, "", "`strategy:` is required (one of: link, cursor, offset, page)"},
		{"unknown strategy", `{strategy: keyset}`, "", `unknown strategy "keyset"`},
		{"foreign field", `{strategy: offset, offset_param: o, cursor_param: c}`, "", "cursor_param belongs to strategy cursor, not offset"},
		{"page_start on cursor", `{strategy: cursor, cursor_path: n, cursor_param: c, page_start: 0}`, "", "page_start belongs to strategy page, not cursor"},
		{"cursor without path", `{strategy: cursor, cursor_param: c}`, "", "cursor_path: required for strategy cursor"},
		{"cursor nowhere to go", `{strategy: cursor, cursor_path: n}`, "", "needs cursor_param"},
		{"patch on link", `{strategy: link, next_path: next, patch: {a: b}}`, "", "patch: doesn't apply to strategy link"},
		{"patch with raw body", `{strategy: cursor, cursor_path: n, patch: {after: "${paginate.cursor}"}}`, `body: "{}"`, "can't merge into a raw body"},
		{"patch and shorthand", `{strategy: cursor, cursor_path: n, cursor_param: after, patch: {after: "${paginate.cursor}"}}`, "", `"after" is also set by cursor_param`},
		{"owned in query", `{strategy: page, page_param: page}`, `query: {page: "2"}`, `"page" is set by paginate (page_param)`},
		{"bad more_when", `{strategy: page, page_param: p, more_when: "a +"}`, "", "more_when"},
		{"typo", `{strategy: page, page_parm: p}`, "", `paginate: unknown field "page_parm" (did you mean page_param?)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := "sources:\n  issues:\n    type: http\n    url: https://h\n    paginate: " + c.paginate + "\n"
			if c.extra != "" {
				data += "    " + c.extra + "\n"
			}
			_, err := loadData(t, data)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestPaginateStrategiesLoad(t *testing.T) {
	for _, p := range []string{
		`{strategy: cursor, cursor_path: "last:id", cursor_param: starting_after, more_when: has_more}`,
		`{strategy: offset, offset_param: _start, limit_param: _limit, page_size: 50, total_path: "header:X-Total-Count"}`,
		`{strategy: page, page_param: page, page_start: 0, more_when: "page + 1 < nbPages"}`,
		`{strategy: link, next_path: "link:next", stop_on_short_page: true}`,
	} {
		if _, err := loadData(t, "sources:\n  issues:\n    type: http\n    url: https://h\n    paginate: "+p+"\n"); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := loadData(t, `
sources:
  issues:
    type: http
    url: https://h
    json: {page_size: 100}
    paginate: {strategy: cursor, cursor_path: next_cursor, patch: {start_cursor: "${paginate.cursor}"}}
`); err != nil {
		t.Error(err)
	}
}
