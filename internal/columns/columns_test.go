package columns

import (
	"strings"
	"testing"
)

func cols(spec string) []Column {
	var out []Column
	for _, f := range strings.Fields(spec) {
		name, rank, _ := strings.Cut(f, ":")
		r := map[string]Rank{"id": Identity, "st": Status, "t": Time, "n": Number, "s": Short, "l": Long, "e": Empty}[rank]
		out = append(out, Column{Path: name, Title: name, Rank: r})
	}
	return out
}

func names(cs []Column) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return strings.Join(out, " ")
}

func TestPick(t *testing.T) {
	for _, tc := range []struct {
		in         string
		max        int
		kept, rest string
	}{
		// Rank order, stable within a rank.
		{"b:s a:id d:n c:st e:t", 8, "a c e d b", ""},
		// The long-text column takes the last slot, displacing the last
		// short string.
		{"id:id s1:s s2:s s3:s n1:n desc:l", 4, "id n1 s1 desc", "s2 s3"},
		// With no short string kept, it displaces the last kept.
		{"id:id st:st t:t n:n desc:l", 3, "id st desc", "t n"},
		// Already kept: nothing moves.
		{"id:id desc:l", 3, "id desc", ""},
		// Empty columns go last, after long text.
		{"e:e id:id x:l", 2, "id x", "e"},
	} {
		kept, rest := Pick(cols(tc.in), tc.max)
		if names(kept) != tc.kept || names(rest) != tc.rest {
			t.Errorf("Pick(%s, %d) = [%s] [%s], want [%s] [%s]", tc.in, tc.max, names(kept), names(rest), tc.kept, tc.rest)
		}
	}
}

func TestStyles(t *testing.T) {
	kept := []Column{
		{Path: "name", Rank: Identity},
		{Path: "status.phase", Rank: Status, Values: []string{"Running", "Mystery"}},
		{Path: "ready", Rank: Status, Bool: true},
		{Path: "cached", Rank: Status, Bool: true},
		{Path: "createdAt", Rank: Time},
		{Path: "restarts", Rank: Number},
		{Path: "size", Rank: Number},
	}
	s := Styles(kept)
	if !s[0].Bold || s[0].Flex != 1 {
		t.Errorf("identity: %+v, want bold and flex (no long text)", s[0])
	}
	if s[1].MaxWidth != 20 || len(s[1].Rules) != 3 || s[1].Rules[0] != (Rule{"Running", "theme:success"}) {
		t.Errorf("status: %+v", s[1])
	}
	if len(s[2].Rules) != 2 || len(s[3].Rules) != 0 {
		t.Errorf("booleans: ready %+v, cached %+v; only a check is coloured", s[2], s[3])
	}
	if s[4].Align != "right" || s[6].Align != "right" {
		t.Error("timestamps and numbers should be right-aligned")
	}
	if len(s[5].Rules) != 1 || s[5].Rules[0] != (Rule{"> 0", "theme:warning"}) || len(s[6].Rules) != 0 {
		t.Errorf("failure count: restarts %+v, size %+v", s[5], s[6])
	}
	// With long text kept, it takes the flex and the identity doesn't.
	s = Styles([]Column{{Path: "name", Rank: Identity}, {Path: "message", Rank: Long}})
	if s[0].Flex != 0 || s[1].Flex != 1 {
		t.Errorf("flex: identity %d, long %d", s[0].Flex, s[1].Flex)
	}
}

func TestStatusRules(t *testing.T) {
	got := StatusRules([]string{"Running", "running", "CrashLoopBackOff", "Pending", "Mystery", ""})
	want := []Rule{
		{"Running", "theme:success"},
		{"CrashLoopBackOff", "theme:error"},
		{"Pending", "theme:warning"},
		{errorFamily, "theme:error"},
		{warningFamily, "theme:warning"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rule %d = %v, want %v", i, got[i], want[i])
		}
	}
	if r := StatusRules([]string{"eu-west", "us-east"}); r != nil {
		t.Errorf("no vocabulary word seen, but rules %v", r)
	}
}

func TestNames(t *testing.T) {
	for _, tc := range []struct {
		match func(string) bool
		yes   []string
		no    []string
	}{
		{IsStatusName, []string{"status", "Phase", "ready", "conclusion"}, []string{"status_code", "name"}},
		{IsTimeName, []string{"created_at", "createdAt", "creationTimestamp", "ts", "@timestamp", "updated", "lastSeen"}, []string{"name", "timeout", "state", "format", "data", "Repeat", "chat"}},
		{IsCheckName, []string{"ready", "is_healthy", "enabled", "OK"}, []string{"cached", "deleted"}},
		{IsFailureCount, []string{"restarts", "restartCount", "errors", "failures", "retry_count"}, []string{"size", "count"}},
	} {
		for _, n := range tc.yes {
			if !tc.match(n) {
				t.Errorf("%s should match", n)
			}
		}
		for _, n := range tc.no {
			if tc.match(n) {
				t.Errorf("%s shouldn't match", n)
			}
		}
	}
}
