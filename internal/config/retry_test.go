package config

import (
	"strings"
	"testing"
)

func TestResilienceErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"401 in on", `{type: http, url: "https://h", retry: {on: [503, 401]}}`, "on[1]: 401 is never retried"},
		{"2xx in on", `{type: http, url: "https://h", retry: {on: [204]}}`, "204 isn't a failure"},
		{"bad backoff", `{type: http, url: "https://h", retry: {backoff: soon}}`, `retry.backoff: invalid duration "soon"`},
		{"negative max", `{type: http, url: "https://h", retry: {max: -1}}`, "retry.max: must be >= 1"},
		{"typo", `{type: http, url: "https://h", retry: {max_wiat: 5s}}`, `retry: unknown field "max_wiat" (did you mean max_wait?)`},
		{"bad size", `{type: http, url: "https://h", max_body: lots}`, `max_body: invalid size "lots"`},
		{"retry on exec", `{type: exec, command: [echo], retry: {max: 2}}`, "retry: only applies to type: http"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadData(t, "sources:\n  issues: "+c.src+"\n")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"50MB": 50 << 20, "512kb": 512 << 10, "1GB": 1 << 30, "2048": 2048, "10 MB": 10 << 20} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0MB", "-5", "1.5MB", "MB"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should fail", bad)
		}
	}
}

// A preset can turn retry on for a whole API, and conditional: false
// overrides an inherited default.
func TestResilienceFromPreset(t *testing.T) {
	s := mustLoadData(t, `
presets:
  api: {type: http, url: "https://h", retry: {max: 5, backoff: 1s}}
sources:
  issues: {extends: api, conditional: false}
`)
	if s.Retry == nil || s.Retry.Max != 5 || s.Conditional == nil || *s.Conditional {
		t.Errorf("retry = %+v, conditional = %v", s.Retry, s.Conditional)
	}
}
