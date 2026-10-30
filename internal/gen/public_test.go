package gen

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// publicCase is one real-world spec and what generating it should give.
type publicCase struct {
	file   string
	cached bool // fetched by go generate, not committed
	args   Options
	budget time.Duration
	// screens and skipped are ranges for the counts the summary gives.
	screens, skipped, actions [2]int
}

var publicCases = []publicCase{
	{file: "petstore.yaml", args: Options{Doc: DocOptions{BaseURL: "https://petstore3.swagger.io/api/v3"}},
		budget: 2 * time.Second, screens: [2]int{3, 8}, skipped: [2]int{0, 20}, actions: [2]int{0, 12}},
	{file: "github.yaml", cached: true, budget: 5 * time.Second, screens: [2]int{150, 260}, skipped: [2]int{800, 1150}, actions: [2]int{40, 200}},
	{file: "stripe.yaml", cached: true, budget: 5 * time.Second, screens: [2]int{180, 300}, skipped: [2]int{100, 250}, actions: [2]int{150, 300}},
}

// Real specs generate a config that loads, within a time budget, with
// counts in the expected ranges.
func TestPublicSpecs(t *testing.T) {
	if testing.Short() {
		t.Skip("public specs are slow; skipped under -short")
	}
	for _, pc := range publicCases {
		t.Run(pc.file, func(t *testing.T) {
			path := publicPath(t, pc)
			opts := pc.args
			opts.Input.Spec = path
			start := time.Now()
			res, err := Generate(context.Background(), opts)
			took := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if took > pc.budget {
				t.Errorf("took %s, over the %s budget", took.Round(time.Millisecond), pc.budget)
			}
			n, skipped := counts(res.Summary)
			if n < pc.screens[0] || n > pc.screens[1] {
				t.Errorf("%d screens, want %d–%d (%s)", n, pc.screens[0], pc.screens[1], res.Summary)
			}
			if skipped < pc.skipped[0] || skipped > pc.skipped[1] {
				t.Errorf("%d skipped, want %d–%d", skipped, pc.skipped[0], pc.skipped[1])
			}
			if _, a, _ := summaryCounts(res.Summary); a < pc.actions[0] || a > pc.actions[1] {
				t.Errorf("%d actions, want %d–%d", a, pc.actions[0], pc.actions[1])
			}
			t.Logf("%s in %s; %d warnings, hints %q", res.Summary, took.Round(time.Millisecond), len(res.Warnings), res.Hints)
		})
	}
}

// --tag narrows a big spec, and its hint goes once it's small.
func TestPublicTagNarrows(t *testing.T) {
	if testing.Short() {
		t.Skip("public specs are slow; skipped under -short")
	}
	path := publicPath(t, publicCases[1])
	all, err := Generate(context.Background(), Options{Input: Input{Spec: path}})
	if err != nil {
		t.Fatal(err)
	}
	some, err := Generate(context.Background(), Options{Input: Input{Spec: path}, Select: Select{Tags: []string{"issues"}}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := counts(all.Summary)
	b, _ := counts(some.Summary)
	if b == 0 || b >= a/5 {
		t.Errorf("--tag issues gave %d screens of %d", b, a)
	}
	if len(all.Hints) == 0 || !strings.Contains(all.Hints[0], "--tag") {
		t.Errorf("no --tag hint on the whole spec: %q", all.Hints)
	}
	if len(some.Hints) != 0 {
		t.Errorf("hints on a narrowed spec: %q", some.Hints)
	}
}

func publicPath(t *testing.T, pc publicCase) string {
	t.Helper()
	path := filepath.Join("testdata", "public", pc.file)
	if pc.cached {
		path = filepath.Join("testdata", "public", ".cache", pc.file)
	}
	if _, err := os.Stat(path); err != nil {
		if pc.cached {
			t.Skipf("%s isn't fetched; go generate ./internal/gen", pc.file)
		}
		t.Fatal(err)
	}
	return path
}

// counts reads "N screens, N sources[, N actions][; skipped M operations]".
func counts(summary string) (screens, skipped int) {
	screens, _, skipped = summaryCounts(summary)
	return
}

var summaryRe = regexp.MustCompile(`^(\d+) screens?, \d+ sources?(?:, (\d+) actions?)?(?:; skipped (\d+) operations?)?$`)

func summaryCounts(summary string) (screens, actions, skipped int) {
	m := summaryRe.FindStringSubmatch(summary)
	if m == nil {
		return -1, -1, -1
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	return atoi(m[1]), atoi(m[2]), atoi(m[3])
}
