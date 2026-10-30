package gen

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Each directory under testdata is a case: spec.yaml or spec.json (or
// spec_path naming one elsewhere), args (one flag token per line), and
// the expected want.yaml or want.err, plus want.report: the skip lines,
// warnings and summary.
func TestGolden(t *testing.T) {
	dirs, _ := filepath.Glob(filepath.Join("testdata", "*"))
	if len(dirs) == 0 {
		t.Fatal("no cases")
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() || filepath.Base(dir) == "public" {
			continue
		}
		t.Run(filepath.Base(dir), func(t *testing.T) {
			res, err := runCase(t, dir)
			wantName, got := "want.yaml", string(res.Config)
			if err != nil {
				wantName, got = "want.err", err.Error()+"\n"
			}
			lines := append(append(res.Skipped, prefixed("warning: ", res.Warnings)...), res.Summary)
			lines = append(lines, prefixed("note: ", res.Notes)...)
			report := strings.Join(append(lines, prefixed("hint: ", res.Hints)...), "\n") + "\n"
			if *update {
				os.Remove(filepath.Join(dir, "want.yaml"))
				os.Remove(filepath.Join(dir, "want.err"))
				write(t, filepath.Join(dir, wantName), got)
				write(t, filepath.Join(dir, "want.report"), report)
			}
			if want := readFile(t, filepath.Join(dir, wantName)); got != want {
				t.Errorf("%s differs (go test ./internal/gen -update to accept):\n%s", wantName, got)
			}
			if want := readFile(t, filepath.Join(dir, "want.report")); report != want {
				t.Errorf("want.report differs:\n%s", report)
			}
		})
	}
}

func runCase(t *testing.T, dir string) (Result, error) {
	t.Helper()
	spec := filepath.Join(dir, "spec.yaml")
	if p := strings.TrimSpace(readFile(t, filepath.Join(dir, "spec_path"))); p != "" {
		spec = filepath.Join(dir, p)
	} else if _, err := os.Stat(spec); err != nil {
		spec = filepath.Join(dir, "spec.json")
	}
	var opts Options
	fs := pflag.NewFlagSet("gen", pflag.ContinueOnError)
	opts.Register(fs)
	args := strings.Fields(readFile(t, filepath.Join(dir, "args")))
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	opts.Input.Spec = filepath.ToSlash(spec)
	if ov := filepath.Join(dir, "overrides.yaml"); fileExists(ov) {
		opts.Overrides = filepath.ToSlash(ov)
		args = append(args, "--overrides", "overrides.yaml")
	}
	opts.Command = strings.Join(append([]string{"tui-builder", "gen", filepath.Base(spec)}, args...), " ")
	return Generate(context.Background(), opts)
}

func prefixed(p string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = p + l
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The same spec and flags give the same bytes.
func TestDeterministic(t *testing.T) {
	for _, dir := range []string{"singletons", "auth_mix", "menu"} {
		a, err := runCase(t, filepath.Join("testdata", dir))
		if err != nil {
			t.Fatal(err)
		}
		for range 5 {
			b, _ := runCase(t, filepath.Join("testdata", dir))
			if !bytes.Equal(a.Config, b.Config) {
				t.Fatalf("%s: two runs differ", dir)
			}
		}
	}
}

func TestNothingToGenerate(t *testing.T) {
	_, err := runCase(t, filepath.Join("testdata", "only_writes"))
	if !errors.Is(err, ErrNothing) {
		t.Errorf("a spec with only writes: %v", err)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// The header lists the spec's operations, and a later run reads them
// back to say what's new and what's gone.
func TestNewOperations(t *testing.T) {
	first, err := runCase(t, filepath.Join("testdata", "singletons"))
	if err != nil {
		t.Fatal(err)
	}
	prev, ok := previousOps(first.Config)
	if !ok || len(prev) != 7 || prev[0] != "GET /me" {
		t.Fatalf("previousOps: %v %v", ok, prev)
	}
	// As if the spec had gained /status and lost /me since.
	edited := strings.Replace(string(first.Config), "#   GET /me, ", "#   GET /gone, ", 1)
	res, err := Generate(context.Background(), Options{
		Input:    Input{Spec: filepath.Join("testdata", "singletons", "spec.yaml")},
		Previous: []byte(edited),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.New, " ") != "GET /me" || strings.Join(res.Gone, " ") != "GET /gone" {
		t.Errorf("new %q, gone %q", res.New, res.Gone)
	}
	// A file gen didn't write has no list: nothing is reported.
	if _, ok := previousOps([]byte("app: {title: x}\n")); ok {
		t.Error("a hand-written config read as having a list")
	}
}
