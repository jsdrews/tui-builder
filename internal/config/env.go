package config

// Env-var validation. Two independent checks fired by Load after
// Validate succeeds:
//
//  1. Every declared `app.env` entry with `required: true` and no
//     env-side value (and no `default:`) is an error. All missing
//     required vars are collected into one message so the user
//     fixes the whole batch in one edit.
//  2. Every `${env.X}` reference in the config is scanned. If X is
//     neither declared under `app.env` nor set in the environment,
//     we emit a stderr warning. Warning, not error — empty-string
//     substitution is a legitimate pattern for some fields
//     (feature-flag env vars, optional headers). Users who want
//     the reference elevated to required declare it in `app.env`.
//
// The check runs from Load in a single pass; wrangl / tui-builder /
// tests all inherit the behavior for free.

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// envTokenRe matches `${env.NAME}` occurrences in a templated
// string. Duplicated from internal/build/template.go's tokenRe
// (which handles selection / env / prompt in the TUI layer);
// keeping a small local copy avoids coupling config to build.
var envTokenRe = regexp.MustCompile(`\$\{env\.([A-Za-z0-9_]+)\}`)

// checkEnv is the load-time env-var validator. Runs after
// c.Validate() so schema errors surface first, and before Load
// returns so downstream consumers never see a config with unset
// required vars.
//
// Behavior split:
//   - Declared + required + unset (no default) → collected into
//     err. Message names every missing var + its description.
//   - Declared + default + unset → os.Setenv applied so ${env.X}
//     substitution picks up the default.
//   - Undeclared + referenced + unset → stderr warning. Every ref
//     shows once (deduplicated) with the paths that mention it,
//     so users can trace where the reference lives.
//
// stderr is io.Writer via package-level for testability.
func checkEnv(c *Config) error {
	if err := validateEnvSpecs(c.App.Env); err != nil {
		return err
	}
	// Apply defaults for unset declared vars first — so the
	// undeclared-refs scan below doesn't warn about a var that
	// was implicitly satisfied by a declared default.
	declared := map[string]bool{}
	for _, spec := range c.App.Env {
		declared[spec.Name] = true
		if spec.Default != "" && os.Getenv(spec.Name) == "" {
			os.Setenv(spec.Name, spec.Default)
		}
	}
	// app.prompts fires a boot-time form modal (tui-builder only)
	// that os.Setenv's each prompt's Key when the user submits. For
	// the ref-check that means those names are "will be provided by
	// the app before fetches happen" — don't warn on them. Under
	// wrangl there's no prompt path, so a user who forgets to set
	// the env falls through to empty-string substitution, same as
	// today (no regression, but no elevation to hard error either).
	for _, p := range c.App.Prompts {
		if p.Key != "" {
			declared[p.Key] = true
		}
	}
	// Collect required-but-unset. Sort for stable error output.
	var missing []EnvSpec
	for _, spec := range c.App.Env {
		if spec.Required && os.Getenv(spec.Name) == "" {
			missing = append(missing, spec)
		}
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i].Name < missing[j].Name })
		return formatMissingEnv(missing)
	}
	// Scan every templated string in the config for ${env.X}
	// references, warn on undeclared+unset ones.
	refs := collectEnvRefs(c)
	var undeclared []string
	for name := range refs {
		if declared[name] {
			continue
		}
		if os.Getenv(name) == "" {
			undeclared = append(undeclared, name)
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		fmt.Fprintf(stderr, "warning: %s references %d env var(s) that are unset and not declared under `app.env:`:\n", c.App.Title, len(undeclared))
		for _, name := range undeclared {
			fmt.Fprintf(stderr, "  ${env.%s}\n", name)
		}
		fmt.Fprintf(stderr, "Substitution will produce empty strings. Add them under `app.env:` (with required/default/description) to silence this or fail loudly.\n")
	}
	return nil
}

// formatMissingEnv produces the multi-var error message. Named vars
// first, one per line with description; then a hint on how to set
// them. Multi-line messages read better than "missing X" one-at-a-time.
func formatMissingEnv(missing []EnvSpec) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("missing %d required env var(s):\n", len(missing)))
	// Widths for aligned two-column output.
	nw := 0
	for _, m := range missing {
		if len(m.Name) > nw {
			nw = len(m.Name)
		}
	}
	for _, m := range missing {
		b.WriteString(fmt.Sprintf("  %-*s   %s\n", nw, m.Name, m.Description))
	}
	b.WriteString("\nSet them in your environment before running, e.g.:\n")
	for _, m := range missing {
		b.WriteString(fmt.Sprintf("  export %s=<value>\n", m.Name))
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// validateEnvSpecs enforces the per-spec schema rules: non-empty
// name, required + default mutex, no duplicate names.
func validateEnvSpecs(specs []EnvSpec) error {
	seen := map[string]bool{}
	for i, spec := range specs {
		if spec.Name == "" {
			return fmt.Errorf("app.env[%d]: `name:` is required", i)
		}
		if seen[spec.Name] {
			return fmt.Errorf("app.env[%d]: duplicate name %q", i, spec.Name)
		}
		seen[spec.Name] = true
		if spec.Required && spec.Default != "" {
			return fmt.Errorf("app.env[%d].%s: `required: true` and `default:` are mutually exclusive — defaults imply optional", i, spec.Name)
		}
	}
	return nil
}

// collectEnvRefs walks every templated string in c and returns the
// set of `${env.X}` names referenced. Covers the fields that
// actually carry templates:
//
//   - Sources: URL, Body, Method, Path, Headers values, Command
//     args, Env values, InitialMessages. Root/From/Where/etc. are
//     dot-paths or expression-language strings, not templates.
//   - Screens: Title.
//   - Actions: Run argv, Confirm, Notice.
//   - Components: Title, Items, Lines, Rows (string cells).
//
// Static Data (any type) isn't walked — if a user puts an env token
// in a static payload they're probably doing it deliberately.
func collectEnvRefs(c *Config) map[string]struct{} {
	refs := map[string]struct{}{}
	visit := func(s string) { addEnvRefs(s, refs) }

	for _, src := range c.Data.Sources {
		if src == nil {
			continue
		}
		visit(src.URL)
		visit(src.Body)
		visit(src.Method)
		visit(src.Path)
		for _, v := range src.Headers {
			visit(v)
		}
		for _, c := range src.Command {
			visit(c)
		}
		for _, v := range src.Env {
			visit(v)
		}
		for _, m := range src.InitialMessages {
			visit(m)
		}
	}

	// Screens: single-screen shorthand + multi-screen map.
	visit(c.TUI.Screen.Title)
	visitScreen(&c.TUI.Screen, visit)
	for _, s := range c.TUI.Screens {
		if s == nil {
			continue
		}
		visit(s.Title)
		visitScreen(s, visit)
	}

	// Actions: the argv / URL / headers / body that actually reach the
	// outside world.
	visitActions(c.Actions, visit)

	// Components: titles + static row/list content.
	for _, comp := range c.TUI.Components {
		if comp == nil {
			continue
		}
		visit(comp.Title)
		for _, item := range comp.Items {
			visit(item)
		}
		for _, line := range comp.Lines {
			visit(line)
		}
		for _, row := range comp.Rows {
			for _, cell := range row {
				if str, ok := cell.(string); ok {
					visit(str)
				}
			}
		}
	}

	return refs
}

// visitScreen walks screen-level templated fields — an action binding's
// Confirm / Notice and its Bind templates. Split out because both the
// shorthand `screen:` and the multi-screen `screens:` map need the same
// walk.
//
// The action's own Run / URL / Headers / Body are NOT walked here: they
// live in the top-level actions: map now, and visitActions covers them
// once each rather than once per binding that references them.
func visitScreen(s *Screen, visit func(string)) {
	for _, b := range s.Actions {
		visit(b.Confirm)
		visit(b.Notice)
		for _, tmpl := range b.Bind {
			visit(tmpl)
		}
	}
}

// visitActions walks the templated fields of every defined action. Runs
// after hoisting, so inline declarations are covered too.
func visitActions(actions map[string]*Action, visit func(string)) {
	for _, a := range actions {
		if a == nil {
			continue
		}
		for _, arg := range a.Run {
			visit(arg)
		}
		visit(a.URL)
		visit(a.Body)
		for _, v := range a.Headers {
			visit(v)
		}
		visit(a.Message)
		visit(a.ErrorMessage)
	}
}

// addEnvRefs finds every `${env.NAME}` in s and adds NAME to refs.
func addEnvRefs(s string, refs map[string]struct{}) {
	if s == "" || !strings.Contains(s, "${env.") {
		return
	}
	for _, m := range envTokenRe.FindAllStringSubmatch(s, -1) {
		refs[m[1]] = struct{}{}
	}
}

// stderr is the sink for warnings. Overridable in tests so we can
// capture the emitted lines without polluting the actual test
// output. io.Writer (not *os.File) so a bytes.Buffer fits.
var stderr io.Writer = os.Stderr
