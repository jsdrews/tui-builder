// Command wrangl is the data-layer-only entry point for tui-builder.
//
// Usage:
//
//	wrangl <config.yaml> [pipeline-or-source-name] [flags]
//
// pflag (via cobra) accepts flags in any position — `wrangl
// examples/foo.yaml --list` and `wrangl --list examples/foo.yaml`
// both work.
//
// Examples:
//
//	wrangl --list examples/http_countries.yaml
//	wrangl examples/http_countries.yaml all_countries
//	wrangl --limit 5 examples/stream_l1.yaml l1
//	wrangl --pretty examples/http_countries.yaml all_countries
//
// With a name, wrangl runs that pipeline (or source) and emits its
// result to stdout: JSON for a one-shot polled source, NDJSON (one
// JSON value per line) for a stream. Designed to pipe into jq,
// duckdb, miller, or any line-oriented downstream.
//
// With no name, wrangl prints the same listing as --list: every
// defined source and pipeline, its kind, and its lifecycle. Useful
// when you've inherited a config and want to know what's available.
//
// wrangl deliberately does NOT import internal/screen, internal/build,
// or any tuilib package. The data layer is architecturally
// independent of the TUI — running wrangl never compiles, links, or
// drags in any terminal-UI code. A CI check enforces this boundary.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/datasource"
	"github.com/jsdrews/tui-builder/internal/output"
	"github.com/jsdrews/tui-builder/internal/pipeline"
	"github.com/jsdrews/tui-builder/internal/version"
)

func main() {
	datasource.UserAgent = "wrangl/" + version.String()
	datasource.Notify = func(msg string) { fmt.Fprintln(os.Stderr, "notice:", msg) }
	// Cobra returns the error from RunE; it has already been printed
	// via SilenceErrors=false behavior. We just need the right exit code.
	if err := newRoot().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	var (
		listOnly    bool
		describe    bool
		pretty      bool
		raw         bool
		all         bool
		listActions bool
		describeAct bool
		limit       int
		maxDuration time.Duration
		paramArgs   []string
	)
	cmd := &cobra.Command{
		Use:   "wrangl <config.yaml> [pipeline-or-source-name]",
		Short: "Run the data layer of a tui-builder config; dump JSON / NDJSON to stdout.",
		Long: `wrangl runs only the data layer of a tui-builder config — sources and
pipelines — and dumps the result to stdout. No TUI is loaded.

With no target name, wrangl prints the inventory of every defined
source and pipeline. With a name, it dumps that target: pretty JSON
for one-shot sources, NDJSON (one frame per line) for streaming
sources. Streams are designed to pipe straight into jq, duckdb,
miller, or any other line-oriented downstream.

The data layer is architecturally independent of the TUI — running
wrangl never compiles, links, or drags in any terminal-UI code.`,
		Example: `  # List every defined source and pipeline
  wrangl --list examples/http_countries.yaml

  # Dump a pipeline as pretty JSON
  wrangl --pretty examples/http_countries.yaml all_countries

  # Pipe into jq
  wrangl examples/http_countries.yaml all_countries | jq '.[0].name.common'

  # Cap a stream at N events or a duration
  wrangl --limit 5 examples/stream_l1.yaml l1
  wrangl --for 10s examples/stream_websocket.yaml trades

  # Inspect actions — what this config can do, and what one would run
  wrangl --list-actions examples/kube.yaml
  wrangl --describe-action examples/kube.yaml delete_pod --param name=nginx --param namespace=default

  # Bind parameters on a parameterized source (repeatable)
  wrangl examples/params_demo.yaml posts_by_user --param user_id=1
  wrangl examples/params_demo.yaml posts_by_user --param user_id=3 --param limit=10`,
		Version:       version.String(),
		Args:          cobra.MaximumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			params, err := parseParams(paramArgs)
			if err != nil {
				return err
			}
			if listActions {
				return runListActions(args[0])
			}
			if describeAct {
				if len(args) < 2 {
					return fmt.Errorf("--describe-action requires an action name")
				}
				return runDescribeAction(args[0], args[1], params)
			}
			if describe {
				if len(args) < 2 {
					return fmt.Errorf("--describe requires a target name")
				}
				return runDescribe(args[0], args[1])
			}
			return runDump(args, listOnly, pretty, raw, all, limit, maxDuration, params)
		},
	}
	// pflag long flags. The names match the previous std-flag surface so
	// `task wrangl -- --list foo.yaml` and existing muscle-memory still
	// work after the migration.
	cmd.Flags().BoolVar(&listOnly, "list", false, "list every defined source / pipeline and exit")
	cmd.Flags().BoolVar(&describe, "describe", false, "print the schema (kind, lifecycle, parameters) for the given target and exit")
	cmd.Flags().BoolVar(&listActions, "list-actions", false, "list every defined action (including hoisted inline ones) and exit")
	cmd.Flags().BoolVar(&describeAct, "describe-action", false, "print an action's schema and a dry run of its resolved argv / request, then exit. Bind inputs with --param")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "indent one-shot JSON output (streams remain NDJSON)")
	cmd.Flags().BoolVar(&raw, "raw", false, "emit string / log-line values as plain text instead of JSON (useful for `format: text` and streaming logs)")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include hidden sources and pipelines (those whose name starts with `_`) in --list")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap stream output at N events, then exit (0 = no cap)")
	cmd.Flags().DurationVar(&maxDuration, "for", 0, "cap stream consumption at this duration, e.g. 5s, 1m (0 = no cap)")
	cmd.Flags().StringSliceVar(&paramArgs, "param", nil, "bind a source parameter (repeatable): --param key=value")
	return cmd
}

// parseParams splits each `key=value` form into a map. Empty key,
// missing `=`, or duplicate keys are errors so silent typos can't
// produce misleading dumps.
func parseParams(args []string) (map[string]string, error) {
	out := make(map[string]string, len(args))
	for _, a := range args {
		i := strings.IndexByte(a, '=')
		if i <= 0 {
			return nil, fmt.Errorf("--param %q: expected key=value (got no `=` or empty key)", a)
		}
		k := a[:i]
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("--param %q: %q given more than once", a, k)
		}
		out[k] = a[i+1:]
	}
	return out, nil
}

func runDump(args []string, listOnly, pretty, raw, all bool, limit int, maxDuration time.Duration, params map[string]string) error {
	configPath := args[0]
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}
	// Resolve ${env.*} before anything reads a URL or an argv. The TUI
	// does this after its prompts run; wrangl has no prompts, so the
	// environment is already final here. Without it, a config that works
	// under `tui-builder` would fetch a literal "${env.HOST}/api" here —
	// the same config behaving differently depending on which binary
	// opened it.
	//
	// app.env `from:` values are read first, but only when something is
	// fetched: a listing shouldn't run a password manager.
	if !listOnly && len(args) >= 2 {
		if err := c.ResolveEnv(); err != nil {
			return err
		}
	}
	c.SubstituteEnv()

	// Route --param: if the target is a PIPELINE with its own
	// `parameters:` block, bind to the pipeline (its operator
	// expressions see them as `params.X`). Otherwise fall through to
	// the original source-binding behavior — params get substituted
	// into URL/Body/Headers/etc. before Build.
	//
	// This means pipelines with params shadow upstream sources for
	// --param purposes — backwards-compatible with single-target
	// source configs while letting parameterized pipelines do their
	// thing. Wire-level forwarding (pipeline params → upstream source
	// params) is a future feature, not in this slice.
	pipelineParams := map[string]map[string]string{}
	if !listOnly && len(args) >= 2 {
		target := args[1]
		if _, ok := c.Data.Presets[target]; ok {
			return fmt.Errorf("%s is a preset; run a source that extends it", target)
		}
		// If the target is an OPERATOR entry that declares its own
		// `parameters:`, bind there (its expressions see params.X).
		// Otherwise walk through to the underlying leaf and bind the
		// caller's params onto its template fields before Build.
		s, ok := c.Data.Sources[target]
		if ok && !s.IsLeaf() && len(s.Parameters) > 0 {
			pipelineParams[target] = params
		} else if err := applyParams(c, target, params); err != nil {
			return err
		}
	} else if len(params) > 0 {
		return fmt.Errorf("--param is only valid when dumping a specific target (got --list / no target)")
	}

	reg, err := pipeline.Build(nil, c.Data.Sources, pipelineParams)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}

	// Bare `wrangl config.yaml` and `--list` both print the listing.
	// Helpful when you don't remember what's defined; matches kubectl's
	// pattern of "no args = something useful, not an error."
	if listOnly || len(args) == 1 {
		printList(os.Stdout, c, reg, all)
		return nil
	}

	name := args[1]
	src := reg.Get(name)
	if src == nil {
		return fmt.Errorf("no source or pipeline named %q (run `wrangl --list %s` to see what's defined)", name, configPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return output.Run(ctx, src, os.Stdout, output.Options{
		Pretty:      pretty,
		Raw:         raw,
		Limit:       limit,
		MaxDuration: maxDuration,
	})
}

// runDescribe prints the schema for a single entry: kind, inferred
// lifecycle, the templated request shape (URL / Path / Command for
// leaves), and the full parameter table. Intended for "I want to
// know what knobs this target takes" introspection — the wrangl
// counterpart to a TUI form preview.
func runDescribe(configPath, target string) error {
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}
	// Deliberately NOT SubstituteEnv'd: --describe reports the templated
	// shape of a request, so `${env.AWX_HOST}/api/v2/jobs/` is the useful
	// answer, and resolving it would print whatever secret an env var
	// holds into the terminal.
	if p, ok := c.Data.Presets[target]; ok {
		describeLeaf(os.Stdout, target, p, extendedBy(c, target))
		return nil
	}
	s, ok := c.Data.Sources[target]
	if !ok {
		return fmt.Errorf("no entry named %q (run `wrangl --list %s` to see what's defined)", target, configPath)
	}
	if s.IsLeaf() {
		describeLeaf(os.Stdout, target, s, nil)
	} else {
		describeOperator(os.Stdout, target, s, c)
	}
	return nil
}

// describeLeaf prints a leaf source, or a preset when extendedBy is
// non-nil (the sources extending it). A field inherited from a preset
// gets a "(from <preset>)" suffix.
func describeLeaf(w *os.File, name string, s *cfg.Source, extendedBy []string) {
	preset := extendedBy != nil
	from := func(field string) string {
		if o := s.Inherited[field]; o != "" && o != name {
			return " (from " + o + ")"
		}
		return ""
	}
	fmt.Fprintf(w, "%s\n", name)
	if preset {
		fmt.Fprintf(w, "  Kind:       preset / %s%s\n", s.Type, from("type"))
	} else {
		fmt.Fprintf(w, "  Kind:       source / %s%s\n", s.Type, from("type"))
	}
	if len(s.ExtendsChain) > 0 {
		fmt.Fprintf(w, "  Extends:    %s\n", strings.Join(s.ExtendsChain, " → "))
	}
	if !preset {
		fmt.Fprintf(w, "  Lifecycle:  %s\n", inferLifecycle(s))
	}
	if s.Refresh != "" {
		fmt.Fprintf(w, "  Refresh:    %s%s\n", s.Refresh, from("refresh"))
	}
	if s.URL != "" {
		fmt.Fprintf(w, "  URL:        %s%s\n", s.URL, from("url"))
	}
	if s.Path != "" {
		fmt.Fprintf(w, "  Path:       %s%s\n", s.Path, from("path"))
	}
	if len(s.Command) > 0 {
		fmt.Fprintf(w, "  Command:    %v%s\n", s.Command, from("command"))
	}
	if len(s.Headers) > 0 {
		names := make([]string, 0, len(s.Headers))
		for k := range s.Headers {
			names = append(names, k)
		}
		sort.Strings(names)
		for i, k := range names {
			label := "  Headers:    "
			if i > 0 {
				label = "              "
			}
			v := s.Headers[k]
			if cfg.IsSecretHeader(k) {
				v = maskLiteral(v)
			}
			fmt.Fprintf(w, "%s%s: %s%s\n", label, k, v, from("headers"))
		}
	}
	body := ""
	switch {
	case s.GraphQL != nil:
		body = "graphql"
	case s.JSON != nil:
		body = "json"
	case len(s.Form) > 0:
		body = "form"
	case s.Body != "":
		body = "raw"
	}
	if body != "" {
		if s.Method != "" {
			fmt.Fprintf(w, "  Method:     %s%s\n", s.Method, from("method"))
		}
		fmt.Fprintf(w, "  Body:       %s\n", body)
	}
	if a := s.Auth; a.Scheme() != "" {
		var how string
		switch a.Scheme() {
		case "bearer":
			how = maskLiteral(a.Bearer)
		case "basic":
			how = "user " + maskLiteral(a.Basic.User)
		case "api_key":
			how = fmt.Sprintf("%s %s = %s", a.APIKey.In, a.APIKey.Name, maskLiteral(a.APIKey.Value))
		case "oauth2":
			how = fmt.Sprintf("client %s via %s", maskLiteral(a.OAuth2.ClientID), a.OAuth2.TokenURL)
		}
		fmt.Fprintf(w, "  Auth:       %s %s%s\n", a.Scheme(), how, from("auth"))
	}
	if r := s.Retry; r != nil {
		max, backoff := r.Max, r.Backoff
		if max == 0 {
			max = cfg.DefaultRetryMax
		}
		if backoff == "" {
			backoff = cfg.DefaultRetryBackoff.String()
		}
		fmt.Fprintf(w, "  Retry:      %d × %s%s\n", max, backoff, from("retry"))
	}
	if p := s.Paginate; p != nil {
		max := p.MaxPages
		if max == 0 {
			max = 20
		}
		fmt.Fprintf(w, "  Paginate:   %s (max %d pages)%s\n", p.Strategy, max, from("paginate"))
	}
	if win := s.Window; win != nil {
		fmt.Fprintf(w, "  Window:     %s%s\n", windowKind(win), from("window"))
	}
	if preset {
		by := strings.Join(extendedBy, ", ")
		if by == "" {
			by = "(nothing)"
		}
		fmt.Fprintf(w, "  Extended by: %s\n", by)
	}
	if len(s.Parameters) == 0 {
		fmt.Fprintln(w, "  Parameters: (none)")
		return
	}
	fmt.Fprintln(w, "  Parameters:")
	writeParamTable(w, s.Parameters, "    ")
}

func describeOperator(w *os.File, name string, s *cfg.Source, c *cfg.Config) {
	fmt.Fprintf(w, "%s\n", name)
	fmt.Fprintf(w, "  Kind:       pipeline / %s\n", s.Type)
	srcName := resolveTargetLeaf(c, name)
	if srcName != "" {
		if leaf, ok := c.Data.Sources[srcName]; ok {
			fmt.Fprintf(w, "  Upstream:   %s (source / %s)\n", srcName, leaf.Type)
		}
	}
	if len(s.Parameters) > 0 {
		fmt.Fprintln(w, "  Pipeline parameters:")
		writeParamTable(w, s.Parameters, "    ")
	}
	fmt.Fprintf(w, "  Lifecycle:  %s\n", lifecycleOfRef(c, name))
	if srcName == "" {
		return
	}
	leaf, ok := c.Data.Sources[srcName]
	if !ok {
		return
	}
	if len(leaf.Parameters) == 0 {
		fmt.Fprintln(w, "  Parameters: (none — passes through from upstream)")
		return
	}
	fmt.Fprintln(w, "  Parameters (from upstream):")
	writeParamTable(w, leaf.Parameters, "    ")
}

// writeParamTable renders the parameter map as an aligned table. We
// sort by name so the same config produces the same output on every
// run (map iteration order would otherwise drift between platforms).
func writeParamTable(w *os.File, params map[string]*cfg.Parameter, indent string) {
	names := make([]string, 0, len(params))
	for n := range params {
		names = append(names, n)
	}
	sort.Strings(names)

	nw, tw, mw := 0, 0, 0
	for _, n := range names {
		if len(n) > nw {
			nw = len(n)
		}
		t := paramType(params[n])
		if len(t) > tw {
			tw = len(t)
		}
		m := paramMode(params[n])
		if len(m) > mw {
			mw = len(m)
		}
	}
	for _, n := range names {
		p := params[n]
		fmt.Fprintf(w, "%s%-*s   %-*s   %-*s   %s\n",
			indent, nw, n, tw, paramType(p), mw, paramMode(p), p.Description)
	}
}

func paramType(p *cfg.Parameter) string {
	if p.Type == "" {
		return "string"
	}
	return p.Type
}

// paramMode renders the required / default / optional column.
//   - REQUIRED       → caller must bind
//   - default: "5"   → optional with declared default
//   - optional       → optional, no default (substitutes to empty)
func paramMode(p *cfg.Parameter) string {
	if p.Required {
		return "REQUIRED"
	}
	if p.Default != "" {
		return "default: " + strconv.Quote(p.Default)
	}
	return "optional"
}

// windowKind names how a window addresses rows: by cursor, by page
// number, or by row offset.
func windowKind(w *cfg.WindowConfig) string {
	switch {
	case w.Cursor != "":
		return "anchored (cursor " + w.Cursor + ")"
	case w.PageParam != "":
		start := 1
		if w.PageStart != nil {
			start = *w.PageStart
		}
		return fmt.Sprintf("page (%s, from %d)", w.PageParam, start)
	}
	return "offset (" + w.OffsetParam + ")"
}

// inferLifecycle returns a one-line classification combining two
// orthogonal facts:
//
//   - Cadence: streamed / polled / one-shot — how the source emits
//     values over time (event-driven, clock-driven, or single fetch).
//   - Readiness: self-contained / needs params — whether required
//     parameters need arguments from a caller before the source can run.
//
// A streaming source can also need params (kube `pod_logs` follow
// with required namespace/name); a polled source can also need params
// (`pods` list refreshed every 5s but parameterized by namespace).
// We surface both so --list / --describe tell the whole truth.
//
// Operator entries don't have an inherent cadence — they delegate to
// their upstream. Lifecycle for those is computed by lifecycleOfRef.
func inferLifecycle(s *cfg.Source) string {
	cadence := "one-shot"
	switch s.Type {
	case "websocket":
		cadence = "streamed"
	case "exec", "http":
		if s.Follow {
			cadence = "streamed (follow)"
		} else if s.Refresh != "" {
			cadence = "polled (refresh: " + s.Refresh + ")"
		}
	case "file", "merge":
		if s.Refresh != "" {
			cadence = "polled (refresh: " + s.Refresh + ")"
		}
	}
	if hasUnbindableRequired(s.Parameters) {
		cadence += " · needs params"
	}
	return cadence
}

func hasUnbindableRequired(params map[string]*cfg.Parameter) bool {
	for _, p := range params {
		if p != nil && p.Required && p.Default == "" {
			return true
		}
	}
	return false
}

// applyParams binds caller-supplied parameter values onto the leaf
// source that backs target. Pipelines walk to their underlying leaf
// via `from:` chains. Templated string fields (URL, Body, Headers,
// Command, …) are resolved in place on a cloned source stored back
// into c.Data.Sources so the data-layer builders see fully-resolved
// templates.
func applyParams(c *cfg.Config, target string, params map[string]string) error {
	if len(params) == 0 {
		// No caller binding requested. Leave the leaf alone — its
		// templated fields stay as-is so the per-row join lookup
		// can clone+bind them later, or so the fetch surfaces a
		// clear unresolved-template error.
		return nil
	}
	leafName := resolveTargetLeaf(c, target)
	if leafName == "" {
		return fmt.Errorf("target %q is not a defined entry", target)
	}
	leaf := c.Data.Sources[leafName]
	cloned := leaf.Clone()
	if err := cloned.BindParams(params); err != nil {
		return fmt.Errorf("%s: %w", leafName, err)
	}
	c.Data.Sources[leafName] = cloned
	return nil
}

// resolveTargetLeaf walks from target through `from:` references
// until it lands on a leaf entry; returns the leaf's name. Empty
// string when no leaf is reachable. Cycles are rejected at config
// load time so the recursion always terminates.
func resolveTargetLeaf(c *cfg.Config, target string) string {
	visited := map[string]bool{}
	for cur := target; cur != ""; {
		if visited[cur] {
			return ""
		}
		visited[cur] = true
		s, ok := c.Data.Sources[cur]
		if !ok {
			return ""
		}
		if s.IsLeaf() {
			return cur
		}
		// Operator — follow its primary upstream.
		ups := s.Upstreams()
		if len(ups) == 0 {
			return ""
		}
		cur = ups[0]
	}
	return ""
}

// printList emits a flat, scannable inventory of every defined source
// and pipeline. Columns:
//
//	NAME             KIND            LIFECYCLE        UPSTREAM (pipelines only)
//
// By convention, names prefixed with `_` are treated as "hidden" —
// they're still fully callable (`wrangl <config> _internal` works),
// but they don't clutter the listing. Pass `showAll` (--all / -a) to
// reveal them.
//
// We deliberately leave emit-type detection for a later phase — it
// requires actually fetching from each source to inspect the value,
// which would have side effects on a `--list` call.
func printList(w *os.File, c *cfg.Config, reg *pipeline.Registry, showAll bool) {
	type row struct{ name, kind, lifecycle, upstream string }
	var rows []row

	add := func(r row) {
		if !showAll && strings.HasPrefix(r.name, "_") {
			return
		}
		rows = append(rows, r)
	}

	for name, s := range c.Data.Sources {
		if s.IsLeaf() {
			add(row{
				name:      name,
				kind:      "source / " + s.Type,
				lifecycle: inferLifecycle(s),
			})
			continue
		}
		// Operator — pick a representative upstream name for the
		// column and follow it to find a lifecycle.
		ups := s.Upstreams()
		upstream := ""
		lifecycle := "unknown"
		if len(ups) > 0 {
			upstream = ups[0]
			lifecycle = lifecycleOfRef(c, upstream)
		}
		add(row{
			name:      name,
			kind:      "pipeline / " + s.Type,
			lifecycle: lifecycle,
			upstream:  upstream,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	// Column widths.
	nw, kw, lw := len("NAME"), len("KIND"), len("LIFECYCLE")
	for _, r := range rows {
		if len(r.name) > nw {
			nw = len(r.name)
		}
		if len(r.kind) > kw {
			kw = len(r.kind)
		}
		if len(r.lifecycle) > lw {
			lw = len(r.lifecycle)
		}
	}
	fmt.Fprintf(w, "%-*s   %-*s   %-*s   %s\n", nw, "NAME", kw, "KIND", lw, "LIFECYCLE", "UPSTREAM")
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s   %-*s   %-*s   %s\n", nw, r.name, kw, r.kind, lw, r.lifecycle, r.upstream)
	}
	printPresets(w, c)
	_ = reg // reserved — future --list will use the Registry for richer info (emitted types after Phase 2 operators land).
}

// lifecycleOfRef walks operator → upstream edges until it lands on
// a leaf entry, then classifies that leaf's cadence. Multi-input
// operators (union / compose) return all their children; we use the
// first (lifecycle is "polled if any child is polled"; an arbitrary
// representative works for the high-level label).
func lifecycleOfRef(c *cfg.Config, ref string) string {
	s, ok := c.Data.Sources[ref]
	if !ok {
		return "unknown"
	}
	if s.IsLeaf() {
		return inferLifecycle(s)
	}
	ups := s.Upstreams()
	if len(ups) == 0 {
		return "unknown"
	}
	return lifecycleOfRef(c, ups[0])
}

// printPresets lists data.presets after the sources, in their own
// section: a preset produces nothing, so it isn't a row you can run.
func printPresets(w *os.File, c *cfg.Config) {
	if len(c.Data.Presets) == 0 {
		return
	}
	names := make([]string, 0, len(c.Data.Presets))
	for name := range c.Data.Presets {
		names = append(names, name)
	}
	sort.Strings(names)
	nw, kw := len("PRESET"), len("KIND")
	for _, name := range names {
		nw = max(nw, len(name))
		kw = max(kw, len("preset / ")+len(c.Data.Presets[name].Type))
	}
	fmt.Fprintf(w, "\n%-*s   %-*s   %s\n", nw, "PRESET", kw, "KIND", "EXTENDED BY")
	for _, name := range names {
		fmt.Fprintf(w, "%-*s   %-*s   %s\n", nw, name, kw, "preset / "+c.Data.Presets[name].Type, strings.Join(extendedBy(c, name), ", "))
	}
}

// extendedBy lists the sources that inherit from preset, directly or
// through another preset.
func extendedBy(c *cfg.Config, preset string) []string {
	out := []string{}
	for name, s := range c.Data.Sources {
		if slices.Contains(s.ExtendsChain, preset) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// maskLiteral shows a templated secret as written (`${env.TOKEN}`) and a
// literal one as •••, so --describe never prints a credential.
func maskLiteral(v string) string {
	if strings.Contains(v, "${") {
		return v
	}
	return "•••"
}
