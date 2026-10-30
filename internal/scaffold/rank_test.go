package scaffold

import (
	"strings"
	"testing"

	"github.com/jsdrews/tui-builder/internal/columns"
)

func classified(t *testing.T, sample string) map[string]columns.Column {
	t.Helper()
	values, err := decodeSample(Output{Stdout: []byte(sample)})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := findRows(values, "")
	if err != nil {
		t.Fatal(err)
	}
	fs := fields(sh.rows)
	out := map[string]columns.Column{}
	for _, c := range classify(fs, markKey(fs, len(sh.rows))) {
		out[c.Path] = c
	}
	return out
}

func TestClassify(t *testing.T) {
	long := strings.Repeat("a long free-text message ", 3)
	got := classified(t, `[
		{"uid": "u1", "name": "a", "kind": "Pod", "region": "eu", "seen": "2026-10-01T10:00:00Z", "when": "2026-10-01 10:00:00", "n": 1, "size": "3Ki", "ok": true, "msg": "`+long+`", "note": "", "host": "h1"},
		{"uid": "u2", "name": "b", "kind": "Pod", "region": "us", "seen": "2026-10-02T10:00:00Z", "when": "2026-10-02 10:00:00", "n": 2, "size": "4Mi", "ok": false, "msg": "`+long+`", "note": "", "host": "h2"},
		{"uid": "u3", "name": "c", "kind": "Pod", "region": "eu", "seen": "2026-10-03T10:00:00Z", "when": "2026-10-03 10:00:00", "n": 3, "size": "5Gi", "ok": true, "msg": "`+long+`", "note": "", "host": "h3"}
	]`)
	for path, want := range map[string]columns.Rank{
		"name":   columns.Identity, // a name beats the uid mark key
		"uid":    columns.Short,
		"kind":   columns.Short, // the same in every row: not a state
		"region": columns.Status,
		"seen":   columns.Time, // by name and by value
		"when":   columns.Time, // by value
		"n":      columns.Number,
		"size":   columns.Number,
		"ok":     columns.Status,
		"msg":    columns.Long,
		"note":   columns.Empty,
		"host":   columns.Short, // every value different
	} {
		if got[path].Rank != want {
			t.Errorf("%s ranked %d, want %d", path, got[path].Rank, want)
		}
	}
	if !got["ok"].Bool {
		t.Error("ok isn't marked boolean")
	}
}

// Without name or title, the mark key's field is the identity.
func TestIdentityFallsBackToMarkKey(t *testing.T) {
	got := classified(t, `[{"ID": "a", "Image": "x"}, {"ID": "b", "Image": "y"}]`)
	if got["ID"].Rank != columns.Identity {
		t.Errorf("ID ranked %d, want identity", got["ID"].Rank)
	}
}

func TestMaxColumnsAndNoStyle(t *testing.T) {
	sample := Output{Stdout: []byte(`[{"name":"a","status":"Running","n":1,"x":"p","y":"q"},{"name":"b","status":"Failed","n":2,"x":"r","y":"s"}]`)}
	res, err := ExecConfig(ExecOptions{Argv: []string{"tool", "things"}, MaxColumns: 2}, sample)
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Config)
	if strings.Count(out, "\n        - ") != 2 || !strings.Contains(out, "title: Status") {
		t.Errorf("max 2 kept the wrong columns:\n%s", out)
	}
	for _, rest := range []string{"# - {title: N, value: n", "# - {title: X, value: x", "# - {title: Y, value: y"} {
		if !strings.Contains(out, rest) {
			t.Errorf("missing commented-out %q:\n%s", rest, out)
		}
	}
	res, err = ExecConfig(ExecOptions{Argv: []string{"tool", "things"}, NoStyle: true}, sample)
	if err != nil {
		t.Fatal(err)
	}
	for _, styled := range []string{"bold", "color_rules", "flex", "max_width", "align"} {
		if strings.Contains(string(res.Config), styled) {
			t.Errorf("--no-style wrote %s:\n%s", styled, res.Config)
		}
	}
	if !strings.Contains(string(res.Config), "- {title: Name, value: name, sortable: true}\n        - {title: Status") {
		t.Errorf("--no-style should still rank:\n%s", res.Config)
	}
}
