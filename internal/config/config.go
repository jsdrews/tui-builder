// Package config defines the YAML schema for a tui-builder TUI: an app block,
// a top-level components map keyed by name, and a screen whose layout tree
// references components by name. Each layout node is a tagged union —
// exactly one of vstack / hstack / zstack / component must be set. Items
// inside vstack/hstack carry a sizing hint (flex or fixed) and inline the
// same node fields.
package config

// Config is the top-level document. Either Screen (single-screen) or
// Screens + Initial (multi-screen) must be set, not both.
//
// Pipelines (optional) sit between sources and consumers (components
// in TUI mode; stdout in wrangl/data mode). v1 pipelines are passthrough
// — they wrap a source or another pipeline and pass Fetch/Subscribe
// through unchanged. Operators (filter, project, sort, etc.) layer on
// top in later phases.
type Config struct {
	App         App                    `yaml:"app"`
	DataSources map[string]*DataSource `yaml:"data_sources,omitempty"`
	Pipelines   map[string]*Pipeline   `yaml:"pipelines,omitempty"`
	Components  map[string]*Component  `yaml:"components"`
	// Screen is the single-screen shorthand. Mutually exclusive with Screens.
	Screen Screen `yaml:"screen,omitempty"`
	// Screens is the multi-screen map keyed by name. Mutually exclusive
	// with Screen. Initial picks the root.
	Screens map[string]*Screen `yaml:"screens,omitempty"`
	// Initial names the root screen when Screens is used. Required iff
	// Screens is non-empty.
	Initial string `yaml:"initial,omitempty"`
}

// Pipeline composes / transforms one or more sources into a single
// named, addressable data stream. Pipelines are first-class: both the
// TUI (via Component.Pipeline binding) and the wrangl CLI (`wrangl
// config.yaml <pipeline-name>`) consume them through the same
// interface.
//
// Each pipeline is *exactly one* operator. The operator shape is a
// tagged union — set one of From / Filter / (future Project / Sort /
// Derive / Union / Compose / Join). The validator rejects configs
// with zero or multiple operators set.
//
// Operators in this file:
//   - From (string)        — passthrough. Wraps a source / pipeline,
//                            delegates Fetch / Subscribe unchanged.
//   - Filter (*FilterOp)   — drops items not matching a predicate.
//                            Streaming-safe (per-event check).
//   - Project (*ProjectOp) — replaces each item with a slimmer object
//                            built from declared output keys, each
//                            sourced from an input expression. Rename
//                            + flatten in one operator.
//   - Derive (*DeriveOp)   — copies each item and adds extra fields
//                            computed from expressions.
//
// Future operators (sort, union, compose, join) plug in here as
// additional union arms.
type Pipeline struct {
	// Parameters declares typed inputs the pipeline accepts. Each
	// operator's expressions (filter.where, project.keep values,
	// derive.compute values, sort.by) can reference bound values as
	// `params.<name>`.
	//
	// Wrangl: `--param key=value` on a pipeline target binds to the
	// pipeline when it declares parameters. (When the pipeline has
	// no parameters, --param falls through to the underlying source —
	// backwards compatible with the v1 wrangl behavior.)
	//
	// Pipeline parameters DON'T auto-forward to upstream source
	// parameters today — that's an explicit forwarding feature to
	// be designed once a real use case lands. Pipeline params are
	// for the pipeline's own operator expressions; upstream source
	// params are still bound by targeting the source directly.
	Parameters map[string]*Parameter `yaml:"parameters,omitempty"`

	// From names an upstream source or pipeline for a passthrough.
	// Mutually exclusive with operator blocks below.
	// Cycles in the pipeline reference graph are rejected at load time.
	From string `yaml:"from,omitempty"`

	// Filter drops items not matching the predicate. See FilterOp.
	Filter *FilterOp `yaml:"filter,omitempty"`
	// Project replaces each item with a slimmer object. See ProjectOp.
	Project *ProjectOp `yaml:"project,omitempty"`
	// Derive copies each item and adds computed fields. See DeriveOp.
	Derive *DeriveOp `yaml:"derive,omitempty"`
	// Sort reorders an iterable by a per-item key. See SortOp.
	Sort *SortOp `yaml:"sort,omitempty"`
	// Union composes N upstream iterables into one. See UnionOp. Same
	// semantics as the merge SOURCE but lives in the pipeline layer
	// so children can be other pipelines, not just leaf sources.
	Union *UnionOp `yaml:"union,omitempty"`
	// Compose bundles N heterogeneous upstreams into a single object
	// whose keys are caller-chosen output names. See ComposeOp.
	Compose *ComposeOp `yaml:"compose,omitempty"`
	// Join enriches each driver row with results from per-row lookup
	// fetches. The driver+lookup pattern (kubectl describe on row
	// highlight, expressed as a data-layer concept). See JoinOp.
	Join *JoinOp `yaml:"join,omitempty"`
	// Cache memoises the upstream's Fetch result for a TTL. See
	// CacheOp.
	Cache *CacheOp `yaml:"cache,omitempty"`
}

// FilterOp drops items from its upstream that don't pass the
// expression in Where. The expression is evaluated against each item
// as an env (top-level field access: `status.phase == 'Running'`
// rather than `item.status.phase == 'Running'`).
//
// Streaming: events whose payload doesn't pass are silently dropped.
// One-shot / polled: the returned iterable is the subset that passes.
// Non-iterable upstream (single object, string, etc.): the predicate
// runs against the value itself.
type FilterOp struct {
	// From names the upstream source or pipeline whose items get
	// filtered. Required.
	From string `yaml:"from"`
	// Where is the predicate expression. Required, non-empty. See
	// internal/expr for the language reference; common forms:
	//
	//   status.phase == 'Running'
	//   metadata.namespace == 'default'
	//   hasPrefix(metadata.name, 'kube-')
	//   status.containerStatuses.0.restartCount > 5
	//   lower(metadata.namespace) contains 'prod'
	Where string `yaml:"where"`
}

// ProjectOp replaces each upstream item with a new object whose keys
// come from Keep. Each value in Keep is an expression evaluated
// against the input item — bare dot-path expressions (`metadata.name`)
// are the common case but anything the expression language can
// produce is valid (`lower(metadata.namespace)`, `len(items)`).
//
// The result is a NEW object — fields not listed in Keep are dropped.
// Useful for slimming down deep API responses to just what a downstream
// component / wrangl consumer needs, and for renaming awkward source
// paths to cleaner names.
//
// Non-object items pass through as nil (you can't project fields out
// of a scalar).
type ProjectOp struct {
	// From names the upstream source or pipeline. Required.
	From string `yaml:"from"`
	// Keep maps output keys to input expressions. Required, non-empty.
	//
	//   project:
	//     from: pods
	//     keep:
	//       name:      metadata.name
	//       namespace: metadata.namespace
	//       phase:     status.phase
	//
	// Output items have the shape {name: ..., namespace: ..., phase: ...}.
	Keep map[string]string `yaml:"keep"`
}

// UnionOp composes N upstream iterables into a single flat union.
// Same semantics as the merge SOURCE — including per-child tags
// nested under MetaKey — but lives as a pipeline operator so children
// can be ANY ds.Source (a leaf source OR another pipeline).
//
// Two configuration shapes — pick one:
//
//   - Sources + TagField: shorthand where each child gets one
//     synthetic tag whose value is the child source name. Legacy.
//   - Children: each entry names a child source + its own tags map.
//     Long form; required when you want richer per-child metadata
//     (cluster name + URL + region, etc.).
//
// The validator rejects both shapes being set, and `tag_field:`
// paired with `children:` (each child carries its own tags map in
// that form, so a global tag field name is meaningless).
type UnionOp struct {
	// Shorthand: list child sources/pipelines by name.
	Sources []string `yaml:"sources,omitempty"`
	// TagField is the key written under MetaKey for every row from
	// every child in the shorthand path. Value is the child source name.
	TagField string `yaml:"tag_field,omitempty"`
	// Long form: each entry explicitly names a child + its own tags.
	Children []MergeChild `yaml:"children,omitempty"`
	// OnError chooses what happens when a child fails:
	//   "" / "fail" (default) — any child error aborts the union
	//   "skip"                — drop the failed child, return the rest
	OnError string `yaml:"on_error,omitempty"`
	// MetaKey controls where injected tags land — defaults to "_meta"
	// (segregated from upstream child data). "" opts out and writes
	// flat at the top level (legacy v1 shape).
	MetaKey *string `yaml:"meta_key,omitempty"`
}

// CacheOp memoises the upstream's Fetch result for a configured TTL.
// Reads within the TTL return the cached snapshot without hitting
// the upstream; the first read after the TTL elapses re-fetches.
//
// Useful for:
//   - Expensive upstreams shared by multiple operators (`union of
//     [filter from pods, sort from pods]` where pods would otherwise
//     fetch twice per top-level Fetch).
//   - Slow remote APIs that downstream consumers hit repeatedly.
//
// Errors are NOT cached — a failing upstream will be retried on the
// next Fetch rather than returning a stale error for the rest of the
// TTL.
//
// Streaming: Subscribe passes through unchanged. Caching event
// streams isn't meaningful (events are incremental, not snapshots);
// callers needing dedup on streaming events should design that
// upstream.
type CacheOp struct {
	// From names the upstream source or pipeline to memoise.
	From string `yaml:"from"`
	// TTL is the cache lifetime as a duration string ("30s", "5m").
	// Required.
	TTL string `yaml:"ttl"`
}

// JoinOp enriches each row from a driver iterable with results
// fetched per-row from one or more lookups. The classic pattern is
// "list pods → for each pod, fetch its detail / logs" — the kubectl
// describe-on-highlight flow expressed as a data-layer concept.
//
// Driver yields N rows; for each row, every lookup is invoked with
// params computed from that row, and the result is attached to the
// row. Emit controls whether lookup results sit alongside the row
// (`separate`, default) or get merged into the row's fields
// (`merged`).
//
// v1 constraints:
//   - Snapshot only — Subscribe returns ErrNotStreaming. Joining
//     over a driver stream needs windowing semantics (cache the
//     latest snapshot; fetch lookups lazily on access) not yet
//     designed.
//   - Each `lookup.from` must be a source with `parameters:` declared,
//     not a pipeline. Lookups need to be re-invoked per row with new
//     params; sources support that via BindParams. Pipeline-as-lookup
//     is a follow-up.
//   - No caching yet — every driver row triggers a fresh per-lookup
//     fetch. Add an LRU keyed on params when N gets large enough to
//     hurt.
type JoinOp struct {
	// Driver names the iterable whose rows seed the join. Can be any
	// source or pipeline that returns []any.
	Driver JoinDriver `yaml:"driver"`
	// Lookups is the map of per-row fetches. Each entry's key is the
	// output bucket name (`detail`, `logs`); each value names the
	// lookup source + how to derive its params from the driver row.
	Lookups map[string]JoinLookup `yaml:"lookups"`
	// Emit controls the output shape per row:
	//   "" / "separate" (default) → {row: <driver row>, <lookup_name>: <result>, ...}
	//   "merged"                  → {<driver row fields>, <lookup result fields>}
	//                                Merged requires lookup results to be
	//                                map-shaped; non-map results error.
	Emit string `yaml:"emit,omitempty"`
	// OnError controls what happens when a single per-row lookup fails:
	//   "" / "fail" (default) — surface as a fetch error
	//   "skip"                — drop the row from the output and continue
	OnError string `yaml:"on_error,omitempty"`
}

// JoinDriver names the iterable whose rows seed the join.
type JoinDriver struct {
	// From references a source or pipeline that returns an iterable.
	From string `yaml:"from"`
}

// JoinLookup names a per-row fetch and how to derive its params from
// the driver row.
type JoinLookup struct {
	// From references a data source (NOT a pipeline) whose
	// `parameters:` block enumerates what it needs to run.
	From string `yaml:"from"`
	// On maps the lookup source's parameter names → expressions
	// evaluated against the driver row. Expressions use the same
	// language as filter / derive / sort / project. Common forms:
	//
	//   on:
	//     namespace: metadata.namespace
	//     name:      metadata.name
	//     port:      "1000 + spec.containerPort"
	On map[string]string `yaml:"on"`
}

// ComposeOp bundles N heterogeneous upstreams into a single object
// whose top-level keys are caller-chosen output names and whose
// values are the corresponding upstream results.
//
// Unlike `union` (which flattens N HOMOGENEOUS iterables into one
// big list), compose preserves the separation of each upstream so a
// single addressable target can carry unrelated data shapes — pods +
// deployments + services for a "fleet" pipeline, for instance.
//
// Children are fetched concurrently; ordering of the parallel fan-out
// doesn't affect the output (the result is a map keyed by output name).
type ComposeOp struct {
	// Parts maps output key → upstream source / pipeline name.
	// Required, non-empty.
	//
	//   compose:
	//     parts:
	//       pods:        pods_all
	//       deployments: deployments_all
	//       services:    services_all
	//
	// Output is {pods: <pods_all result>, deployments: …, services: …}.
	Parts map[string]string `yaml:"parts"`
	// OnError chooses what happens when a child fails:
	//   "" / "fail" (default) — any child error aborts compose
	//   "skip"                — drop the failed child from the output;
	//                            only error if every child fails
	OnError string `yaml:"on_error,omitempty"`
}

// SortOp reorders the upstream iterable by a per-item key expression.
// The key is computed once per item; sort.SliceStable orders items
// so ties keep their relative input order.
//
// Snapshot-only: streaming subscribes return ErrNotStreaming so
// consumers fall back to polling. Sorting a true event stream needs
// windowing (which we'll add when a use-case demands it); for now
// the operator is honest about its constraint.
//
// Comparison handles bool, number (int / int64 / float64), string,
// and time.Time. Mixed-type keys (rare in practice) fall back to
// string-representation comparison.
type SortOp struct {
	// From names the upstream source or pipeline. Required.
	From string `yaml:"from"`
	// By is the expression that produces the sort key per item.
	// Required, non-empty. Common forms:
	//
	//   metadata.name
	//   status.containerStatuses.0.restartCount
	//   lower(metadata.name)
	//   parseTime(status.startTime)
	By string `yaml:"by"`
	// Order is "asc" (default) or "desc". Anything else is rejected
	// at validate time so typos surface immediately.
	Order string `yaml:"order,omitempty"`
}

// DeriveOp copies each upstream item unchanged and adds new fields
// computed from expressions. Useful for synthesising derived values
// (age from a start timestamp, a label from a status combination)
// without losing access to the original fields.
//
// Compute keys that collide with existing item fields override them —
// derive wins so a user can re-shape an awkward field in-place.
//
// Non-object items pass through unchanged (nowhere to add fields).
type DeriveOp struct {
	// From names the upstream source or pipeline. Required.
	From string `yaml:"from"`
	// Compute maps output keys to expressions. Required, non-empty.
	//
	//   derive:
	//     from: pods
	//     compute:
	//       age_seconds: "now() - parseTime(status.startTime)"
	//       is_kube:     "hasPrefix(metadata.namespace, 'kube-')"
	Compute map[string]string `yaml:"compute"`
}

// DataSource fetches data that one or more components bind to. Supported
// `type:` values: http, exec, file, merge. ${selection.*} and ${env.*}
// tokens substitute at push time across most string fields.
//
// Per-type field reference:
//
//	http   url, method, headers, body, format, root, refresh, timeout
//	exec   command, env, format, root, refresh, timeout
//	file   path, format, root, refresh
//	merge  sources, tag_field, on_error, refresh
type DataSource struct {
	// Type selects the fetch mechanism.
	Type string `yaml:"type"`

	// Parameters declares typed inputs the source needs to run. Any
	// caller (wrangl --param, TUI push site, programmatic) must supply
	// values for the required params; the source references them via
	// ${params.<name>} in URL / Body / Headers / Command / Env / Path /
	// InitialMessages templates.
	//
	// Lifecycle inference: a source with at least one required param
	// that has no default is on-demand (won't run without explicit
	// caller binding). Polled / streamed sources can also have params
	// but all required ones must be bindable at startup (defaults,
	// env, etc.) for the source to schedule itself.
	Parameters map[string]*Parameter `yaml:"parameters,omitempty"`

	// Shared by http / exec / file / merge.
	// Root is a dot-path into the response selecting the iterable root
	// for list/table bindings. Empty = response itself.
	Root string `yaml:"root,omitempty"`
	// Refresh is the polling interval (e.g. "30s", "1m"). Empty = fetch
	// once on screen activate. Driven by tea.Tick.
	Refresh string `yaml:"refresh,omitempty"`
	// Timeout caps per-fetch latency. Default 10s for http/exec, n/a
	// for file (synchronous read) and merge (defers to children).
	Timeout string `yaml:"timeout,omitempty"`
	// Format selects how the response body is parsed:
	//   "" / "json"   parse as JSON, hand the typed value to bindings (default)
	//   "text"        keep the body as a raw string — required for logview
	//                 bindings against plain-text endpoints (e.g. kube pod logs)
	Format string `yaml:"format,omitempty"`

	// http fields.
	URL     string            `yaml:"url,omitempty"`
	Method  string            `yaml:"method,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty"`

	// websocket fields.
	// InitialMessages are text frames sent immediately after the
	// connection upgrades — useful for protocols (bitstamp, Kraken,
	// Coinbase, many custom buses) that require a subscribe handshake
	// before the server starts emitting. ${selection.*} / ${env.*}
	// substitute per entry. Sent in order, fire-and-forget; failures
	// don't terminate the stream but do appear as one error event.
	InitialMessages []string `yaml:"initial_messages,omitempty"`

	// exec fields.
	// Command is the argv ([cmd, arg, arg, ...]). The first element is
	// looked up in $PATH; subsequent elements are passed as-is.
	// ${selection.*} and ${env.*} substitute per element.
	Command []string `yaml:"command,omitempty"`
	// Env adds (or overrides) environment variables on top of the
	// process's own environment. ${env.*} can reference outer env;
	// ${selection.*} substitutes in values.
	Env map[string]string `yaml:"env,omitempty"`
	// Follow turns a source into a streaming source.
	//
	// On `exec`: the subprocess is started (not waited on), its stdout
	// is read line-by-line, and each line is delivered as an Event.
	// Use for `kubectl logs -f`, `tail -f`, `journalctl -f`, etc.
	//
	// On `http`: the request stays open and the response body is read
	// line-by-line. Use for kube `?follow=true` log endpoints, SSE
	// streams, NDJSON change-feeds, anything chunked.
	//
	// Refresh is ignored when Follow is true (the open stream is the
	// continuous data path). format: text is the typical companion —
	// each line goes through as-is. format: json parses each line
	// at the consumer (logview / wrangl --raw bypasses parsing).
	Follow bool `yaml:"follow,omitempty"`

	// file fields.
	// Path is the file to read. ${selection.*} / ${env.*} substitute.
	Path string `yaml:"path,omitempty"`

	// merge fields. Two shapes — pick one:
	//
	// Shorthand: Sources + TagField. Lists child source names; the
	// composer injects a single field per row whose value is the source
	// name. Right for cross-cluster / cross-account fan-outs where the
	// only thing you need is "which source did this row come from."
	//
	// Long form: Children. Each entry names a source AND a map of
	// arbitrary tags to inject into every row from that child. Use when
	// rows need more than a single literal source-name — e.g. a drilldown
	// detail source needs the cluster's proxy URL or region code that
	// isn't in the pod JSON itself.
	Sources  []string     `yaml:"sources,omitempty"`
	TagField string       `yaml:"tag_field,omitempty"`
	Children []MergeChild `yaml:"children,omitempty"`
	// MetaKey is the JSON key that merge-injected tags get nested under,
	// segregating framework metadata from the original child data so
	// they don't collide and so consumers can tell at a glance "this
	// came from the merge composer, not from the upstream source."
	//
	// Default: `_meta`. Set to "" (explicit empty) to opt out and
	// write tags flat at the top level (the legacy v1 shape).
	//
	// Concrete example with default:
	//   {"metadata": {...}, "status": {...}, "_meta": {"cluster": "prod"}}
	// With MetaKey set to "":
	//   {"metadata": {...}, "status": {...}, "cluster": "prod"}
	//
	// Column / inspector field paths reach the metadata via dot-path
	// (`value: _meta.cluster`) when nested; via the bare key
	// (`value: cluster`) when flat.
	MetaKey *string `yaml:"meta_key,omitempty"`
	// OnError chooses what happens when a child source errors during a
	// merge fetch:
	//   "" / "fail" (default) — any child error aborts the merge
	//   "skip"                — drop the failed child, return the rest
	//                            (only errors if EVERY child fails)
	OnError string `yaml:"on_error,omitempty"`
}

// App configures the surrounding tuilib app shell.
type App struct {
	// Title prefixes the breadcrumb (the screen title appears after it).
	Title string `yaml:"title,omitempty"`
	// Version renders on the right side of the statusbar.
	Version string `yaml:"version,omitempty"`
	// Theme names a built-in theme.Theme.Name to use as the initial palette.
	// Unknown names fall through to the first theme.
	Theme string `yaml:"theme,omitempty"`
	// HelpVerbose restores the legacy footer that tight-packs bindings
	// inline. Default (false) is minimal mode — the footer shows "? help"
	// and `?` opens the expanded panel.
	HelpVerbose bool `yaml:"help_verbose,omitempty"`
	// Prompts collected at boot, before any screen renders. Each
	// prompt's Key becomes an env var (set via os.Setenv) whose value
	// is whatever the user typed / picked, so the existing
	// ${env.<KEY>} substitution covers BOTH OS env vars and these
	// boot-time params. Cancel from the form aborts the program.
	//
	// Use for: which symbols to watch (URL param), which cluster to
	// hit (URL host), which flags to pass to a CLI source (exec
	// argv). Anything that's "configure at startup, then constant."
	//
	// Pre-populating: if the env var named by a prompt's Key is
	// already set, the prompt's input is pre-filled with that value —
	// so `SYMBOLS=btcusdt,ethusdt tui-builder ...` lets you skip the
	// modal entirely.
	Prompts []Prompt `yaml:"prompts,omitempty"`
}

// Screen describes one screen — its breadcrumb title, its layout tree,
// and any on_enter bindings that push other screens.
type Screen struct {
	// Title shows in the breadcrumb. May contain ${selection} tokens
	// when this screen is reachable via an on_enter push.
	Title string `yaml:"title,omitempty"`
	// Layout is the root of the layout tree. Required.
	Layout Node `yaml:"layout"`
	// OnEnter declares which components, when enter is pressed on them
	// (and they're focused), push another screen. Multi-screen only.
	OnEnter []OnEnterBinding `yaml:"on_enter,omitempty"`
	// Actions hand a key off to a subprocess (kubectl exec, $EDITOR, open,
	// etc.) with the focused row's selection substituted into the argv.
	Actions []Action `yaml:"actions,omitempty"`
}

// Action binds a key to a subprocess executed via tuilib's pkg/runner.
// While the subprocess runs the TUI is suspended and the terminal is
// handed over to the subprocess (so kubectl exec, ssh, $EDITOR, etc.
// work as expected). Run argv elements support ${selection.*} (resolved
// against the focused list/table row at fire time) and ${env.*} (always).
type Action struct {
	// Key is the dispatch key. Common choices: "x", "d", "o", "e". Don't
	// collide with reserved keys (q, t, ?, tab, esc, enter, /, j, k, r).
	Key string `yaml:"key"`
	// Label appears in the help strip / panel.
	Label string `yaml:"label,omitempty"`
	// Source is the list or table component whose focused row's selection
	// is substituted into Run.
	Source string `yaml:"source"`
	// Run is the argv. Must be non-empty.
	Run []string `yaml:"run"`
	// Notice, when non-empty, is printed once after the TUI suspends and
	// before the subprocess starts — useful for slow handoffs ("connecting…").
	Notice string `yaml:"notice,omitempty"`
	// Confirm, when non-empty, shows a yes/no modal with this message
	// before dispatching the subprocess. ${selection.*}/${env.*} resolve
	// in the message the same way they do in Run. Yes runs the action,
	// No (or esc) dismisses the modal.
	Confirm string `yaml:"confirm,omitempty"`
	// Interactive controls how the subprocess is launched.
	//   true  (default): hand the TTY off via pkg/runner — needed for
	//                     vim, kubectl exec, ssh, htop, anything that
	//                     wants raw input or full-screen redraws.
	//                     The alt-screen suspends + resumes around the
	//                     subprocess (visible as a brief flicker).
	//   false: cmd.Run() in a goroutine, capture stdout+stderr, never
	//          suspend the alt-screen — right for non-interactive
	//          actions (`kubectl scale`, `kubectl delete`, `open URL`,
	//          one-shot scripts). Subprocess output appears in an alert
	//          on error, statusbar on success.
	Interactive *bool `yaml:"interactive,omitempty"`
	// Prompts collects user input before the action dispatches. Each
	// prompt becomes a form field; on submit, values are exposed as
	// ${prompt.<key>} tokens substituted into Run argv, Confirm
	// message, and Notice. Order: prompts → confirm (with substituted
	// preview) → dispatch. Cancel from the form aborts the action.
	Prompts []Prompt `yaml:"prompts,omitempty"`
}

// MergeChild names a child source plus the tags merge should inject
// into every row that originated from that child. Tags are key/value
// strings written at the top level of each map-shaped item — same
// substrate as the legacy TagField but with arbitrary keys and values
// instead of one literal source-name.
//
// Order in the parent `children:` list determines child fetch order
// (mirrors the legacy `sources:` order), so deterministic UIs that
// depend on row order get the same shape under both forms.
type MergeChild struct {
	// Source is the name of a data source defined elsewhere in
	// the config. Required.
	Source string `yaml:"source"`
	// Tags are injected into every map-shaped row produced by this
	// child. Existing keys on the row survive — tagging is purely
	// additive. Non-map rows (scalars, arrays) pass through
	// untouched, same as TagField.
	Tags map[string]string `yaml:"tags,omitempty"`
}

// Parameter declares one typed input slot on a data source. Callers
// bind values; the source references them with ${params.<name>}.
//
// Today's POC schema is minimal — `type` is informational (`string`
// covers all current uses); `required` + `default` are mutually
// exclusive (a default makes a param effectively optional). Validation
// rules, complex types, and computed defaults are deferred until a
// concrete need surfaces.
type Parameter struct {
	// Type is informational today: string (default), int, bool, duration.
	// Future use: form widget selection at TUI binding sites, basic
	// validation in wrangl (--param port=abc against type:int rejects).
	Type string `yaml:"type,omitempty"`
	// Required means callers MUST bind a value before the source can
	// run. Mutually exclusive with Default.
	Required bool `yaml:"required,omitempty"`
	// Default is the value used when no caller supplies one. Setting
	// Default implies the param is optional.
	Default string `yaml:"default,omitempty"`
	// Description shows up in --list / launcher prompts / future
	// --help output. One-line summary.
	Description string `yaml:"description,omitempty"`
}

// Prompt is one field in an action's input form. Types map 1:1 to
// tuilib pkg/form field kinds:
//
//	text    (default) — single-line text input
//	select  — pick one of `options`
//	confirm — yes/no toggle (value is "true" / "false")
type Prompt struct {
	Key         string   `yaml:"key"`
	Label       string   `yaml:"label,omitempty"`
	Type        string   `yaml:"type,omitempty"`        // text | select | confirm
	Placeholder string   `yaml:"placeholder,omitempty"` // text only
	Initial     string   `yaml:"initial,omitempty"`     // text default value
	Options     []string `yaml:"options,omitempty"`     // select choices
	InitialIdx  int      `yaml:"initial_index,omitempty"` // select default
	InitialBool bool     `yaml:"initial_bool,omitempty"`  // confirm default
}

// InteractiveDefault reports whether an action with no explicit
// Interactive field should run via pkg/runner. The default is true so
// the most common case (drop into a shell, edit a file) Just Works.
func (a Action) InteractiveDefault() bool {
	if a.Interactive == nil {
		return true
	}
	return *a.Interactive
}

// OnEnterBinding wires "enter on Source pushes Push." The source must be
// a list or table component referenced in this screen's layout; Push
// names a screen in Config.Screens. The source component's current
// selection becomes the ${selection} token in the pushed screen.
//
// Bind maps destination-screen parameter names to templates evaluated
// against the focused row's Selection. Use this when the destination
// screen has data sources that declare `parameters:` — the values
// resolve at push time and feed into each parameterized source's
// BindParams call. Without Bind, parameterized sources on the
// destination won't have their required params filled and will error
// at fetch (or push, depending on how strict we make it).
//
//	bind:
//	  namespace: ${selection.Namespace}
//	  name:      ${selection.Name}
//
// Values support the same ${selection.*} / ${env.*} / ${prompt.*}
// substitutions as everywhere else.
type OnEnterBinding struct {
	Source string            `yaml:"source"`
	Push   string            `yaml:"push"`
	Bind   map[string]string `yaml:"bind,omitempty"`
}

// Node is a tagged-union layout node. Exactly one of VStack / HStack /
// ZStack / Component must be non-empty. Component is the name of a
// component defined in Config.Components.
type Node struct {
	VStack    []Item  `yaml:"vstack,omitempty"`
	HStack    []Item  `yaml:"hstack,omitempty"`
	ZStack    *ZStack `yaml:"zstack,omitempty"`
	Component string  `yaml:"component,omitempty"`
}

// Item is a child of a vstack or hstack. It carries a sizing hint (flex or
// fixed) plus an inlined Node. Exactly one of Flex / Fixed should be set;
// when both are zero, Flex=1 is assumed.
type Item struct {
	Flex  int `yaml:"flex,omitempty"`
	Fixed int `yaml:"fixed,omitempty"`
	Node  `yaml:",inline"`
}

// ZStack overlays Overlay on top of Base. Both fill the parent rect.
type ZStack struct {
	Base    Node `yaml:"base"`
	Overlay Node `yaml:"overlay"`
}

// Component is a leaf node — one of the supported tuilib components: list,
// table, logview, tree, inspector. Most fields are kind-specific; the
// validator rejects mismatched combinations.
type Component struct {
	// Type selects the component kind: list | table | logview | tree |
	// inspector.
	Type string `yaml:"type"`
	// Title sits on the component's pane border.
	Title string `yaml:"title,omitempty"`
	// Filterable enables the embedded '/' filter on list/table/inspector.
	// (For logview/tree, use Searchable.)
	Filterable bool `yaml:"filterable,omitempty"`
	// FilterPlaceholder is the empty-state hint inside the filter input.
	FilterPlaceholder string `yaml:"filter_placeholder,omitempty"`
	// InitialFilter pre-populates the filter value (and applies it).
	InitialFilter string `yaml:"initial_filter,omitempty"`
	// InitialCursor places the cursor at a specific row index on startup.
	InitialCursor int `yaml:"initial_cursor,omitempty"`

	// Source names a data source this component is bound to. When set,
	// the static Items/Rows/Fields are ignored and the component is
	// populated by the source's response after each fetch. Use Item
	// (list), per-column Value (table), or per-field Path (inspector) to
	// map from response shape to component shape.
	//
	// Pipeline is the alias: binding to a pipeline name works exactly
	// like binding to a source name. Pipelines implement the same
	// fetch contract; the component layer doesn't care which one it's
	// reading from. Either Source OR Pipeline can be set; setting both
	// is a config error.
	Source   string `yaml:"source,omitempty"`
	Pipeline string `yaml:"pipeline,omitempty"`
	// Item is the dot-path used by a list bound to a data source to pluck
	// the display string for each element of the iterable root.
	Item string `yaml:"item,omitempty"`

	// Colors overrides individual theme tokens on this component. Unset
	// fields fall through to the theme. Color values accept named colors
	// (red, green, gray, bright_red, ...), 0-255 palette indices ("160"),
	// or hex strings ("#ff8800").
	Colors *Colors `yaml:"colors,omitempty"`

	// ColorRules drive data-aware coloring for components with a single
	// value stream — list items and logview lines. (For table, rules
	// live per Column; for inspector, per InspectorField.) Rules
	// evaluate in order, first match wraps the rendered text with
	// ansi.CellColor.
	ColorRules []ColorRule `yaml:"color_rules,omitempty"`

	// list fields
	Items []string `yaml:"items,omitempty"`

	// table fields
	Columns     []Column `yaml:"columns,omitempty"`
	Rows        [][]any  `yaml:"rows,omitempty"`
	InitialSort *Sort    `yaml:"initial_sort,omitempty"`
	// MaxRows caps the table when bound to a streaming source — each
	// arriving JSON frame is prepended as a new row, oldest rows
	// dropped past this size. 0 (default) implies 100 for streaming
	// tables (unbounded growth would eat memory); ignored for
	// non-streaming bindings and for keyed-upsert mode (see RowKey).
	// Set explicitly to override or to -1 for truly unbounded.
	MaxRows int `yaml:"max_rows,omitempty"`
	// RowKey turns a streaming-bound table into a keyed-upsert view —
	// the L1 order-book / status-table / "one row per X" pattern. The
	// dot-path picks a key out of each event; when an event arrives
	// whose key matches an existing row, that row is updated in
	// place (cursor stays put). When the key is new, the row appends.
	// Without RowKey, streaming events prepend to a ring buffer
	// (live-tape pattern). MaxRows is ignored in keyed mode — row
	// count is naturally bounded by the number of distinct keys.
	RowKey Path `yaml:"row_key,omitempty"`

	// logview fields
	Lines      []string `yaml:"lines,omitempty"`
	Searchable bool     `yaml:"searchable,omitempty"`
	MaxLines   int      `yaml:"max_lines,omitempty"`
	FilterMode bool     `yaml:"filter_mode,omitempty"`
	// InitialQuery pre-populates the search query on logview / tree.
	InitialQuery string `yaml:"initial_query,omitempty"`

	// tree fields
	Root *TreeNode `yaml:"root,omitempty"`

	// inspector fields
	Fields []InspectorField `yaml:"fields,omitempty"`

	// shared hierarchical fields (tree, inspector). InitialDepth pre-
	// expands every node whose depth is < InitialDepth: 0 = root only,
	// 1 = root expanded, 2 = root + first level, …
	InitialDepth int `yaml:"initial_depth,omitempty"`
}

// Sort declares an initial table sort. Column may be a column title
// (case-insensitive prefix match) or a 1-based column number; the column
// must be Sortable.
type Sort struct {
	Column string `yaml:"column"`
	Desc   bool   `yaml:"desc,omitempty"`
}

// Column declares one table column. Width sizing modes mirror table.Column:
// Width>0 fixed, Width==0 content-auto, Flex>0 expands.
type Column struct {
	Title    string `yaml:"title"`
	Width    int    `yaml:"width,omitempty"`
	Flex     int    `yaml:"flex,omitempty"`
	MaxWidth int    `yaml:"max_width,omitempty"`
	Align    string `yaml:"align,omitempty"` // left | right | center
	Sortable bool   `yaml:"sortable,omitempty"`
	// Sort picks the comparator when Sortable is true.
	//   "" / "string"  case-insensitive lex on the ANSI-stripped cell (default)
	//   "number"       strconv.ParseFloat after stripping commas
	//   "si"           number + K/M/B/G/T suffix (1K=1e3, 1M=1e6, …)
	Sort string `yaml:"sort,omitempty"`
	// Value is the dot-path (or fallback chain of dot-paths) used by a
	// data-source-bound table to pluck the cell from each iterable-root
	// element. A scalar string ("name.common") is the common case; a
	// list of strings is tried in order and the first non-empty result
	// wins — useful for kube-style computed fields where the
	// authoritative value lives under different keys depending on
	// state (e.g. container waiting reason → terminated reason →
	// pod phase).
	Value Path `yaml:"value,omitempty"`
	// ColorRules apply data-driven coloring per cell in this column.
	// Rules are evaluated in order; the first match wraps the cell value
	// with ansi.CellColor (preserves the selected-row background). When
	// no rule matches the cell is rendered plain. See ColorRule for the
	// `when:` syntax.
	ColorRules []ColorRule `yaml:"color_rules,omitempty"`
}

// ColorRule pairs a `when:` matcher with a `color:`. Recognised `when:`
// syntax: exact case-insensitive string, "~regex", numeric comparison
// ("> 5", "<= 10", "== 0", "!= 0", with optional K/M/B/G/T suffix on
// the right-hand side), or empty (always — useful as a terminal default
// rule).
type ColorRule struct {
	When  string `yaml:"when,omitempty"`
	Color string `yaml:"color"`
}

// TreeNode is a node in a tree component's root. Children may be empty
// for leaves. Cycle detection is not performed — define a DAG only.
type TreeNode struct {
	Label    string      `yaml:"label"`
	Children []*TreeNode `yaml:"children,omitempty"`
}

// InspectorField is one entry in an inspector component — a label / value
// pair with optional nested children that render as an expandable subtree.
// Pass an empty Value when the field is just a header for its Children.
// When the inspector is data-source-bound, Path overrides Value: the
// resolved dot-path into the source response becomes the displayed value.
type InspectorField struct {
	Label string `yaml:"label"`
	Value string `yaml:"value,omitempty"`
	Path  string `yaml:"path,omitempty"`
	// ColorRules wrap this field's rendered value with ansi.CellColor
	// when a rule matches. Same syntax as Column.ColorRules.
	ColorRules []ColorRule      `yaml:"color_rules,omitempty"`
	Children   []InspectorField `yaml:"children,omitempty"`
}

// Colors is the per-component palette override. Each field maps to one
// tuilib component Options field; only fields the component understands
// are consulted (e.g. Header/SelectedBG apply to tables, Label/Value
// apply to inspectors). Unset fields fall through to the theme.
type Colors struct {
	// All panes:
	BorderActive   string `yaml:"border_active,omitempty"`
	BorderInactive string `yaml:"border_inactive,omitempty"`
	Spinner        string `yaml:"spinner,omitempty"`

	// list:
	Selected string `yaml:"selected,omitempty"`

	// table:
	SelectedFG      string `yaml:"selected_fg,omitempty"`
	SelectedBG      string `yaml:"selected_bg,omitempty"`
	Header          string `yaml:"header,omitempty"`
	Cell            string `yaml:"cell,omitempty"`
	ColumnSeparator string `yaml:"column_separator,omitempty"`
	HeaderRule      string `yaml:"header_rule,omitempty"`

	// inspector:
	Label string `yaml:"label,omitempty"`
	Value string `yaml:"value,omitempty"`

	// inspector / logview / tree:
	Match         string `yaml:"match,omitempty"`
	CurrentLineBG string `yaml:"current_line_bg,omitempty"`
}
