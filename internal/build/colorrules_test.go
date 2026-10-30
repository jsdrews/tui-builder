package build

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// A palette written in hex still colors a rule's `theme:` token, matched
// to whatever the terminal can show; with no color it stays plain.
func TestColorRuleHexThemeTokenFollowsProfile(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	th := theme.New(palette.CatppuccinMocha())
	rules := []cfg.ColorRule{{When: "Running", Color: "theme:success"}}

	for _, tc := range []struct {
		profile termenv.Profile
		want    string // the foreground's SGR prefix
	}{
		{termenv.TrueColor, "\x1b[38;2;"},
		{termenv.ANSI256, "\x1b[38;5;"},
		{termenv.Ascii, ""},
	} {
		lipgloss.SetColorProfile(tc.profile)
		got := applyColorRules("Running", rules, th)
		if tc.want == "" {
			if got != "Running" {
				t.Errorf("profile %v: got %q, want plain", tc.profile, got)
			}
			continue
		}
		if !strings.HasPrefix(got, tc.want) || !strings.HasSuffix(got, "Running\x1b[39m") {
			t.Errorf("profile %v: got %q, want %q… with a foreground-only reset", tc.profile, got, tc.want)
		}
	}
}

// success, warning and error resolve on every built-in palette, and
// they're three different colors.
func TestStatusThemeTokens(t *testing.T) {
	for _, p := range palette.All() {
		th := theme.New(p)
		seen := map[string]string{}
		for _, tok := range []string{"success", "warning", "error"} {
			c := resolveThemeToken(tok, th)
			if c == nil {
				t.Fatalf("%s: theme:%s resolves to nothing", p.Name, tok)
			}
			if c, ok := c.(lipgloss.Color); ok {
				if other, dup := seen[string(c)]; dup {
					t.Errorf("%s: theme:%s and theme:%s are both %s", p.Name, tok, other, c)
				}
				seen[string(c)] = tok
			}
		}
	}
}
