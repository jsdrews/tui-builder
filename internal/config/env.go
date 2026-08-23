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
	walkTemplates(c, func(s *string) { addEnvRefs(*s, refs) })
	return refs
}

// SubstituteEnv replaces every `${env.NAME}` token in the config with
// os.Getenv(NAME), in place. Unset vars become the empty string —
// checkEnv has already warned about (or rejected) the ones that matter.
//
// This lives in the data layer on purpose. The equivalent TUI-side
// substitution is in internal/build, which cmd/wrangl must not import
// (see scripts/check-data-layer-boundary.sh) — so before this existed,
// `wrangl` silently emitted configs with literal `${env.X}` in their
// URLs and argv while `tui-builder` resolved them, which is a
// difference no user could be expected to guess at.
//
// It is NOT called from Load, and must not be: cmd/tui-builder collects
// `app.prompts:` and os.Setenv's the answers *after* loading, so baking
// env in at load time would freeze prompt-backed vars to empty. Each
// entry point calls this once its environment is final.
//
// Safe to call more than once — the second call returns immediately.
// That guard is load-bearing, not tidiness: substitution is single-pass
// within a call, so an inserted value is never rescanned, but a *second*
// walk would treat it as a fresh template. An env var whose value
// happens to contain "${env.SOMETHING}" would then expand on the second
// pass, turning the contents of one variable into a reference to
// another. A substituted value is data, not a template.
func (c *Config) SubstituteEnv() {
	if c == nil || c.envSubstituted {
		return
	}
	c.envSubstituted = true
	walkTemplates(c, func(s *string) {
		if *s == "" || !strings.Contains(*s, "${env.") {
			return
		}
		*s = envTokenRe.ReplaceAllStringFunc(*s, func(tok string) string {
			return os.Getenv(envTokenRe.FindStringSubmatch(tok)[1])
		})
	})
}

// walkTemplates visits every string in the config that may carry a
// template token, handing each one out by pointer so a caller can read
// it or rewrite it in place.
//
// Both the load-time reference scan and SubstituteEnv go through here.
// That is the point: when they were two separate walks, the scanner
// warned about references the substituter never resolved, and coverage
// drifted apart field by field. One walk means a field is either in
// both or in neither.
//
// Deliberately not covered: fields that name data rather than carry
// display text — dot-paths (a column's `value:`, a source's `root:`, an
// inspector field's `path:`) and expression strings (`where:`,
// `compute:`, `by:`) — and `static` `data:`, which is arbitrary YAML
// rather than a template. An inspector field's `value:` IS covered: it
// is a literal, with `path:` being that field's dot-path.
func walkTemplates(c *Config, fn func(*string)) {
	visitMap := func(m map[string]string) {
		for k, v := range m {
			s := v
			fn(&s)
			m[k] = s
		}
	}
	visitSlice := func(xs []string) {
		for i := range xs {
			fn(&xs[i])
		}
	}

	for _, src := range c.Data.Sources {
		if src == nil {
			continue
		}
		fn(&src.URL)
		fn(&src.Body)
		fn(&src.Method)
		fn(&src.Path)
		visitMap(src.Headers)
		visitSlice(src.Command)
		visitMap(src.Env)
		visitSlice(src.InitialMessages)
	}

	// Screens: single-screen shorthand + multi-screen map.
	visitScreen(&c.TUI.Screen, fn)
	for _, s := range c.TUI.Screens {
		if s != nil {
			visitScreen(s, fn)
		}
	}

	for _, comp := range c.TUI.Components {
		if comp == nil {
			continue
		}
		fn(&comp.Title)
		fn(&comp.FilterPlaceholder)
		fn(&comp.InitialFilter)
		fn(&comp.InitialQuery)
		fn(&comp.RootLabel)
		visitSlice(comp.Items)
		visitSlice(comp.Lines)
		for i := range comp.Columns {
			fn(&comp.Columns[i].Title)
		}
		for _, row := range comp.Rows {
			for j, cell := range row {
				switch x := cell.(type) {
				case string:
					s := x
					fn(&s)
					row[j] = s
				case map[string]any:
					// Styled cells: {value: X, color: red} / {label: X, url: …}.
					for k, vv := range x {
						if str, ok := vv.(string); ok {
							s := str
							fn(&s)
							x[k] = s
						}
					}
				}
			}
		}
		visitTreeNode(comp.Root, fn)
		visitInspectorFields(comp.Fields, fn)
	}
}

// visitScreen walks a screen's own templated fields: its title, its
// actions' Run / Confirm / Notice, and any on_key bind templates.
func visitScreen(s *Screen, fn func(*string)) {
	fn(&s.Title)
	for i := range s.Actions {
		fn(&s.Actions[i].Confirm)
		fn(&s.Actions[i].Notice)
		for j := range s.Actions[i].Run {
			fn(&s.Actions[i].Run[j])
		}
	}
	for i := range s.OnKey {
		for k, v := range s.OnKey[i].Bind {
			str := v
			fn(&str)
			s.OnKey[i].Bind[k] = str
		}
	}
}

func visitTreeNode(n *TreeNode, fn func(*string)) {
	if n == nil {
		return
	}
	fn(&n.Label)
	for _, ch := range n.Children {
		visitTreeNode(ch, fn)
	}
}

func visitInspectorFields(fields []InspectorField, fn func(*string)) {
	for i := range fields {
		fn(&fields[i].Label)
		fn(&fields[i].Value)
		visitInspectorFields(fields[i].Children, fn)
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
