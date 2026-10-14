package config

import (
	"strings"
	"testing"
)

func TestResponseErrors(t *testing.T) {
	cases := []struct{ name, data, want string }{
		{"empty_on not a status", `
sources:
  issues: {type: http, url: "https://h", empty_on: [44]}`, "empty_on[0]: 44 isn't an HTTP status"},
		{"empty_on success", `
sources:
  issues: {type: http, url: "https://h", empty_on: [204]}`, "is a success"},
		{"error_path on exec", `
sources:
  issues: {type: exec, command: [echo], error_path: message}`, "error_path: only applies to type: http"},
		{"empty locator", `
sources:
  issues: {type: http, url: "https://h", error_path: "header:"}`, `names nothing after header:`},
		{"header on window.cursor", `
sources:
  issues:
    type: http
    url: "https://h"
    window: {cursor: "header:X-Cursor", search_param: q, older: {a: b}, newer: {c: d}}`, "window.cursor: header: locators only work on response-level fields"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadData(t, c.data)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestLocatorOnColumnIsAnError(t *testing.T) {
	c := windowedConfig()
	c.TUI.Components["books"].Columns[0].Value = Path{"link:next"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "columns[0].value: link: locators only work") {
		t.Errorf("err = %v", err)
	}
}

func TestResponseLocatorsAccepted(t *testing.T) {
	if _, err := loadData(t, `
sources:
  issues:
    type: http
    url: "https://h"
    empty_on: [404, 410]
    error_path: "header:X-Error"
    paginate: {strategy: link, next_path: "link:next"}`); err != nil {
		t.Fatal(err)
	}
}
