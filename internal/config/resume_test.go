package config

import (
	"strings"
	"testing"
)

func TestResumeValidation(t *testing.T) {
	stream := func(edit func(s *Source)) *Source {
		s := &Source{Type: "exec", Follow: true, Command: []string{"tail", "--since", ResumeSince},
			Resume: &ResumeSpec{Timestamp: `^(\S+) `}}
		if edit != nil {
			edit(s)
		}
		return s
	}
	cases := []struct {
		name string
		src  *Source
		want string
	}{
		{"exec stream", stream(nil), ""},
		{"websocket", &Source{Type: "websocket", URL: "ws://x?since=" + ResumeSince, Resume: &ResumeSpec{Timestamp: `"ts":"([^"]+)"`, Granularity: "1ms"}}, ""},
		{"not a stream", stream(func(s *Source) { s.Follow = false }), "only applies to a stream"},
		{"no timestamp", stream(func(s *Source) { s.Resume.Timestamp = "" }), "timestamp: required"},
		{"bad regex", stream(func(s *Source) { s.Resume.Timestamp = "([" }), "resume.timestamp"},
		{"no capture group", stream(func(s *Source) { s.Resume.Timestamp = `^\S+ ` }), "needs a capture group"},
		{"bad granularity", stream(func(s *Source) { s.Resume.Granularity = "soon" }), "granularity"},
		{"token without resume", stream(func(s *Source) { s.Resume = nil }), "declares no `resume:`"},
	}
	for _, tc := range cases {
		err := tc.src.Validate("data.sources.log")
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", tc.name, tc.want, err)
		}
	}
}
