package scaffold

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Each directory under testdata/exec is a recorded run: argv (one
// argument per line), the command's output.json, optionally root (as
// --root), and the expected want.yaml, or want.err for a refusal.
func TestExecGolden(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "exec", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no cases: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			read := func(name string) string {
				b, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				return string(b)
			}
			opts := ExecOptions{
				Argv: strings.Fields(read("argv")),
				Root: strings.TrimSpace(read("root")),
			}
			got, err := ExecConfig(opts, Output{Stdout: []byte(read("output.json"))})
			wantName, gotText := "want.yaml", string(got.Config)
			if err != nil {
				wantName, gotText = "want.err", err.Error()+"\n"
			}
			if *update {
				os.Remove(filepath.Join(dir, "want.yaml"))
				os.Remove(filepath.Join(dir, "want.err"))
				if err := os.WriteFile(filepath.Join(dir, wantName), []byte(gotText), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := read(wantName); gotText != want {
				t.Errorf("%s differs (go test ./internal/scaffold -update to accept):\n%s", wantName, gotText)
			}
		})
	}
}

func TestNoRunLoads(t *testing.T) {
	res, err := ExecConfigNoRun(ExecOptions{Argv: []string{"kubectl", "get", "pods", "-o", "json"}, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type: inspector", "auto: true", "timeout: 30s", "--no-run"} {
		if !bytes.Contains(res.Config, []byte(want)) {
			t.Errorf("--no-run config lacks %q:\n%s", want, res.Config)
		}
	}
}

func TestDecodeSample(t *testing.T) {
	for _, tc := range []struct {
		name, in  string
		truncated bool
		values    int
		err       string
	}{
		{"one document", `[{"a":1},{"a":2}]`, false, 1, ""},
		{"ndjson", "{\"a\":1}\n{\"a\":2}\n\n{\"a\":3}\n", false, 3, ""},
		{"not json", "NAME   READY\napi    1/1", false, 0, "isn't JSON"},
		{"bad line", "{\"a\":1}\nWARNING: x\n", false, 0, "has a line that isn't JSON"},
		{"cut ndjson keeps whole lines", "{\"a\":1}\n{\"a\":2}\n{\"a\":", true, 2, ""},
		{"cut document", `[{"a":1},{"a":`, true, 0, "isn't complete JSON"},
	} {
		values, err := decodeSample(Output{Stdout: []byte(tc.in), Truncated: tc.truncated})
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || len(values) != tc.values {
			t.Errorf("%s: %d values, %v; want %d", tc.name, len(values), err, tc.values)
		}
	}
	var many strings.Builder
	for range 80 {
		many.WriteString("{\"a\":1}\n")
	}
	if values, _ := decodeSample(Output{Stdout: []byte(many.String())}); len(values) != maxNDJSONRows {
		t.Errorf("80 NDJSON lines sampled %d rows, want %d", len(values), maxNDJSONRows)
	}
}

func TestFindRowsRefusals(t *testing.T) {
	for in, want := range map[string]string{
		`[]`:                          "empty",
		`{"items": []}`:               "items is empty",
		`[1, 2]`:                      "holds a number",
		`"hello"`:                     "single string",
		`{"a": [{"x":1}], "b": [{}]}`: "several lists of objects (a, b)",
	} {
		values, err := decodeSample(Output{Stdout: []byte(in)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := findRows(values, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", in, err, want)
		}
	}
	values, _ := decodeSample(Output{Stdout: []byte(`{"a": [{"x":1}], "n": 3}`)})
	if _, err := findRows(values, "n"); err == nil || !strings.Contains(err.Error(), "no list at n (its lists: a)") {
		t.Errorf("--root at a number: %v", err)
	}
	values, _ = decodeSample(Output{Stdout: []byte("{\"a\":1}\n{\"a\":2}")})
	if _, err := findRows(values, "items"); err == nil || !strings.Contains(err.Error(), "one JSON value per line") {
		t.Errorf("--root on NDJSON: %v", err)
	}
}

func TestWords(t *testing.T) {
	for in, want := range map[string]string{
		"creationTimestamp": "Creation timestamp",
		"owner_id":          "Owner id",
		"restart-count":     "Restart count",
		"podIP":             "Pod ip",
		"ID":                "Id",
		"HTTPStatus":        "Http status",
		"@timestamp":        "Timestamp",
		"Repository":        "Repository",
	} {
		if got := words(in); got != want {
			t.Errorf("words(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceName(t *testing.T) {
	for argv, want := range map[string]string{
		"kubectl get pods -o json":        "pods",
		"gh pr list --json number":        "pr",
		"docker ps --format json":         "ps",
		"cat data/users.json":             "users",
		"aws ec2 describe-instances":      "describe_instances",
		"./scripts/report.sh":             "report",
		"./books.sh 0 50":                 "books",
		"jq -n [1]":                       "jq",
		"kubectl get pods.v1.metrics.k8s": "pods_v1_metrics",
	} {
		if got := sourceName(strings.Fields(argv)); got != want {
			t.Errorf("sourceName(%s) = %q, want %q", argv, got, want)
		}
	}
}

func TestSortFor(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{`[{"v":1},{"v":2.5}]`, "number"},
		{`[{"v":"1,024"},{"v":"7"}]`, "number"},
		{`[{"v":"12Ki"},{"v":"3.5M"},{"v":"900"}]`, "si"},
		{`[{"v":"187MB"},{"v":"900kB"},{"v":"2GiB"}]`, "si"},
		{`[{"v":"12Ki"},{"v":"lots"}]`, ""},
		{`[{"v":true},{"v":false}]`, ""},
		{`[{"v":1},{"v":"x"}]`, ""},
	} {
		values, _ := decodeSample(Output{Stdout: []byte(tc.in)})
		sh, _ := findRows(values, "")
		if got := fields(sh.rows)[0].sort; got != tc.want {
			t.Errorf("%s: sort %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMarkKey(t *testing.T) {
	for in, want := range map[string]string{
		`[{"id":1,"name":"a"},{"id":2,"name":"b"}]`:             "id",
		`[{"id":1,"name":"a"},{"id":1,"name":"b"}]`:             "name",
		`[{"id":1,"name":"a"},{"name":"b"}]`:                    "name",
		`[{"metadata":{"uid":"u1"}},{"metadata":{"uid":"u2"}}]`: "metadata.uid",
		`[{"ID":"a","Names":"x"},{"ID":"b","Names":"y"}]`:       "ID",
		`[{"name":"a"},{"name":"a"}]`:                           "",
		`[{"id":null,"name":"a"},{"id":null,"name":"b"}]`:       "name",
		`[{"id":[1],"name":"a"},{"id":[2],"name":"b"}]`:         "name",
	} {
		values, _ := decodeSample(Output{Stdout: []byte(in)})
		sh, _ := findRows(values, "")
		if got := markKey(fields(sh.rows), len(sh.rows)); got != want {
			t.Errorf("%s: mark_key %q, want %q", in, got, want)
		}
	}
}

// Nested objects flatten two levels; lists of objects, deeper objects
// and fields null in every row are left out; lists of scalars stay.
func TestFields(t *testing.T) {
	values, _ := decodeSample(Output{Stdout: []byte(`[
		{"a": 1, "m": {"x": 1, "deep": {"y": 2}}, "objs": [{"k": 1}], "tags": ["p", "q"], "gone": null, "spec": {"name": "s"}, "name": "n"},
		{"a": 2, "m": {"x": 2}, "tags": [], "gone": null, "late": "z"}
	]`)})
	sh, _ := findRows(values, "")
	var got []string
	for _, f := range fields(sh.rows) {
		got = append(got, f.path+"="+f.title)
	}
	want := "a=A m.x=X tags=Tags spec.name=Spec name name=Name late=Late"
	if strings.Join(got, " ") != want {
		t.Errorf("fields:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	ctx := context.Background()
	out, err := Run(ctx, []string{"sh", "-c", `echo '[{"a":1}]'`}, 0, FollowAuto)
	if err != nil || strings.TrimSpace(string(out.Stdout)) != `[{"a":1}]` {
		t.Errorf("run → %q, %v", out.Stdout, err)
	}
	for script, want := range map[string]string{
		`echo one >&2; echo two >&2; exit 3`: "exited with status 3:\none\ntwo",
		`true`:                               "printed nothing to sample",
		`sleep 5`:                            "didn't finish within 200ms",
	} {
		if _, err := Run(ctx, []string{"sh", "-c", script}, 200*time.Millisecond, FollowAuto); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s → %v, want %q", script, err, want)
		}
	}
	if _, err := Run(ctx, []string{"no-such-command-xyz"}, 0, FollowAuto); err == nil {
		t.Error("a missing program ran")
	}
}

func TestCappedKeepsTheStart(t *testing.T) {
	c := &capped{max: 5}
	c.Write([]byte("abc"))
	c.Write([]byte("defgh"))
	c.Write([]byte("ij"))
	if c.buf.String() != "abcde" || !c.cut {
		t.Errorf("capped kept %q, cut %v", c.buf.String(), c.cut)
	}
}

func TestShellJoin(t *testing.T) {
	got := ShellJoin([]string{"sh", "-c", "ls | jq -s .", "it's", "a=b"})
	if want := `sh -c 'ls | jq -s .' 'it'\''s' a=b`; got != want {
		t.Errorf("ShellJoin = %s, want %s", got, want)
	}
}
