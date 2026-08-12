# Agent guidance for tui-builder

This file is the entry point for AI agents (Claude Code, Cursor, etc.)
working in this repository. It captures the architecture in
~10 minutes of reading and codifies the rules that keep the codebase
lean — tui-builder's core value is that it has very little code, and
most "I need feature X" requests should be answered with YAML, not Go.

Read [`README.md`](README.md) first for the user-facing surface; this
doc assumes you know what the project does. For the full schema
reference, see [`docs/components.md`](docs/components.md).

## The mental model

tui-builder has two halves, split by what they do with the same config:

```
                          ┌──────────────────────────────────── data layer ────────────────────────────────────┐
   YAML config  ──►  internal/config  ──►  internal/datasource  ──►  internal/pipeline  ──┬──►  internal/output  ──►  stdout (cmd/wrangl)
                    (structs + valid.)    (Source impls)            (operator catalog)    │       (JSON / NDJSON / raw)
                                                                          │               │
                                                              internal/expr             ▼
                                                          (expression language)         │
                                                                                        │
                                                                                        ▼
                                                                              ┌───── TUI layer ──────────────────────────────┐
                                                                              internal/build  ──►  internal/screen  ──►  tuilib pkg/app
                                                                              (cfg → live comps)    (screen.Screen)
```

**Data layer** (the heart of the product):

- **`internal/config`** owns the schema. The headline type is
  `cfg.Source` — one bag-of-fields struct with a `Type` string
  discriminator. Both leaf kinds (http / exec / file / websocket /
  static / merge) and operator kinds (passthrough / filter / project
  / derive / sort / union / compose / join / cache) populate the
  same struct; each kind reads only the fields it cares about.
  `DataBlock.Sources map[string]*Source` is the unified data map.
  yaml.v3 drives Unmarshal directly into `Source`; `Source.Validate`
  switches on `Type` to delegate to per-kind validators
  (`validateHTTP`, `validateFilter`, etc.).
  `Config.Validate` walks the source graph for cycle detection +
  join-lookup constraints.
- **`internal/datasource`** owns the `ds.Source` runtime interface
  and the concrete leaf builders (http, exec, file, websocket,
  static, merge). Each `newXxx(s *cfg.Source) (ds.Source, error)`
  reads only the fields its kind uses. Self-contained; no awareness
  of components, screens, or rendering.
- **`internal/pipeline`** is the named, addressable composition layer
  over sources. Operator catalog (all shipped): passthrough (`from:`),
  `filter`, `project`, `derive`, `sort`, `union`, `compose`, `join`,
  `cache`. Every operator satisfies the same `ds.Source` interface,
  so neither the TUI binding nor `wrangl` special-cases them.
  Operators also have their own typed `parameters:` blocks (visible
  in operator expressions as `params.X`); wrangl `--param` routes to
  operator params when declared, otherwise falls through to the
  underlying leaf's params.
- **`internal/expr`** is the embedded expression language adapter —
  wraps `expr-lang/expr` behind a `Compile` / `Eval` / `EvalBool`
  (+ `EvalWithParams` / `EvalBoolWithParams`) surface. Operators use
  it for `where:`, `keep:`, `compute:`, `by:`, and join `on:`. A
  small set of built-ins (`now`, `parseTime`, `lower`, `upper`)
  layers on top of the library's natives.
- **`internal/output`** is the JSON / NDJSON stdout sink used by
  `wrangl`. One-shot sources emit one JSON value; streams emit
  NDJSON. `--raw` mode skips JSON encoding for plain-text values.

**TUI layer** (one of two consumers of the data layer):

- **`internal/build`** maps `cfg.Component` → live tuilib components
  (`pkg/list.Model`, `pkg/table.Model`, …) by populating the right
  `Options` struct and calling `New(opts)`. Also owns layout-tree
  construction, data binding (`ApplyData`), template substitution
  (`${selection.*}` / `${env.*}`), and color-rule evaluation.
- **`internal/screen`** is one config-driven `screen.Screen` impl.
  Holds focus state, the pipeline registry, modal state (confirm /
  alert), action dispatch, and the streaming-event pump. Multi-screen
  is implemented as the same `Model` type pushed/popped from tuilib's
  `screen.Stack`.

The TUI CLI (`cmd/tui-builder`) and the data CLI (`cmd/wrangl`) are
both ~100 lines each: load YAML, build sources + pipelines, hand off
to the screen or the output package respectively. The launcher
(`cmd/example-launcher`) is a list-of-yamls + a `screen.Push` per
selection.

### Hard rule: the data layer never imports the TUI

The five packages above the dotted line —
`internal/datasource`, `internal/pipeline`, `internal/output`,
`internal/config`, `cmd/wrangl` — MUST NOT import
`internal/screen`, `internal/build`, or any `tuilib` package. This
is enforced in CI by `scripts/check-data-layer-boundary.sh`
(walks `go list -deps` for each data-layer package).

Why: data wrangling is the product, the TUI is one sink. The split
keeps `wrangl` cheap to build and link, keeps the data layer
testable without a TTY, and forces honest separation — if you ever
need a TUI helper from the data layer, the helper belongs in the
data layer, not the screen package. If you genuinely can't avoid
the dependency, surface it explicitly — change the architecture
deliberately, don't quietly grow the import graph.

## The universal contract: `datasource.Source`

Every data source — http, exec, file, websocket, merge — implements:

```go
// internal/datasource/source.go
type Source interface {
    Fetch(ctx context.Context) (any, error)
    Refresh() time.Duration
}

type StreamingSource interface {
    Source
    Subscribe(ctx context.Context) (<-chan Event, error)
}
```

**`Fetch` returns the useful value.** `root:` slicing happens *inside*
each source's `Fetch`, not in the caller. This is load-bearing: it's
how `merge` composes children correctly. Don't move root handling out
of the source.

**Streaming sources** implement `Subscribe` *additionally*. The screen
detects the interface at `OnEnter`, opens the subscription, and pumps
events into the bound logview. Non-streaming sources fall back to the
poll-via-`tea.Tick` path.

## Pipelines: named, addressable data over sources

Pipelines live alongside leaf sources in the unified `data.sources:`
map — every entry is a `*cfg.Source` whose `Type` field picks its
kind. Leaf kinds (http, exec, file, websocket, static, merge) fetch
externally; operator kinds (passthrough, filter, project, derive,
sort, union, compose, join, cache) transform an upstream named via
`from:` (or fan out over `sources:` / `parts:` / `lookups:`).

```yaml
data:
  sources:
    countries: { type: http, url: …, refresh: 5m }

    all_countries:           # passthrough — stable addressable name
      type: passthrough
      from: countries

    large_countries:         # filter — operators chain through from:
      type: filter
      from: countries
      where: "population > 100000000"

    with_age:                # derive — adds computed fields
      type: derive
      from: countries
      compute:
        is_huge: "population > 100000000"
```

Components bind to any entry by name via `source:` — leaf or
operator, the schema doesn't distinguish:

```yaml
tui:
  components:
    countries:
      type: table
      source: all_countries
```

The full operator catalog layers on top of leaves without changing
the binding contract — the component and `wrangl` both keep asking
for a name; the entry does whatever shaping it does internally.

When asked to add a new operator type, the shape is parallel to
adding a new source kind ("Adding a new pipeline operator" below).

## How a YAML field becomes pixels

Trace the trip for `value: name.common` on a table column bound to an
HTTP source:

1. **Parse.** `config.Load(path)` reads the file. yaml.v3 unmarshals
   into the `Config` struct. Every entry under `data.sources:`
   decodes into a `*cfg.Source` with its `Type` field set.
2. **Desugar.** `ExpandSourcesPipe` rewrites any `pipe:` chain into
   standalone entries linked by `from:`.
3. **Validate.** `Config.Validate()` calls `Source.Validate(path)`
   on each entry (switches on `Type` to per-kind helpers), then
   walks the graph for cycle detection + join-lookup constraints +
   component bindings.
4. **Build.** `pipeline.Build(prebuilt, sources, params)` walks the
   graph topologically. Leaves dispatch to `ds.BuildLeaf` which
   switches on `Type` and calls `newHTTP` / `newExec` / etc. with
   the source. Operators dispatch to `newFilter` / `newSort` / etc.
   with their upstream pre-resolved.
5. **Build components.** `build.Build(layout, components, theme)`
   walks the layout tree, constructs each component leaf via the
   matching `buildList` / `buildTable` / etc. function.
6. **Bind.** `screen.Model.build_()` wires components-by-source-name
   into a registry (`m.sources[name]`).
7. **Screen enter.** The tuilib `OnEnter` hook fires `startFetch(name)`
   per source (cursor-driven sources skip — they wait for their driver's
   first RowFocusedMsg).
8. **Fetch.** `http.Fetch` GETs the URL, parses JSON, applies the
   source's own `root:` slicing, returns the resulting value.
9. **Apply.** `build.ApplyData(component, data, theme)` dispatches by
   `Component.Kind`. For tables: walk `items := ds.Iter(data)`, then
   for each row + column, call `applyColorRules(ds.FirstString(item,
   col.Value), col.ColorRules, theme)` and hand the assembled `[]Row`
   to `table.SetRows`.
10. **Render.** tuilib's `pkg/table` does the actual rendering on
    next View().

When something is "not appearing," walk this chain from both ends.
Usually it's a path mismatch (step 8 plucks `""`), a binding mismatch
(component's `source:` doesn't match a defined data source), or a
templating mismatch (the substituted URL didn't resolve).

## Rules

These are the conventions that keep the codebase lean. Following them
matters more than the specific implementations.

### 1. YAML is the API. Don't add Go for what YAML can express.

If a user request can be answered with a YAML pattern using existing
schema, document the pattern. Don't add a new schema field.

Example: "I want to drill from a list to a detail view" — that's
existing multi-screen + `on_key`. Don't add a `drilldown:` field.

If the user request *can't* be expressed in YAML, the smallest extension
is the right answer:

- New per-component-kind capability → new field on `cfg.Component`
- New leaf-source or operator field → new field on `cfg.Source`
- New layout primitive → new tag in `cfg.Node`'s tagged union

Each addition costs a schema field, a validator clause, a build-layer
mapping, and a docs entry — keep that overhead in mind when accepting
scope.

### 2. tui-builder code is a thin wrapper. Trust tuilib for behavior.

If something can be done by setting a field on tuilib's `Options`,
that's the implementation. Don't reimplement filtering, sorting,
scrolling, theming, focus, layout, or anything else tuilib already
does.

The pattern, every time:

```go
func buildXyz(c *cfg.Component, th theme.Theme) xyz.Model {
    opts := th.Xyz()                  // start from theme builder
    opts.Title = c.Title              // map config to options
    opts.SomeField = c.SomeField
    if c.Colors != nil {              // apply per-component overrides
        // …pure pass-through to opts fields…
    }
    m := xyz.New(opts)
    if c.InitialSomething != "" {     // post-construction state
        m.SetSomething(c.InitialSomething)
    }
    return m
}
```

Mirror this shape for any new component or new field. State-preserving
rebuild on `SetTheme` lives in `Component.Rebuild`; mirror the same
field flow there using tuilib's accessor methods to preserve cursor /
value / sort / filter state across the rebuild.

### 3a. The data-layer / TUI-layer boundary is enforced in CI. Don't break it.

`scripts/check-data-layer-boundary.sh` fails the build if anything in
`internal/config`, `internal/datasource`, `internal/pipeline`,
`internal/expr`, `internal/output`, or `cmd/wrangl` transitively
imports `internal/screen`, `internal/build`, or any `tuilib` package.

If you're tempted to import a TUI helper from the data layer:

- The right move is almost always to move the helper into the data
  layer (or into a small shared `internal/*` package the data layer
  can own).
- The wrong move is to disable the boundary check or add an
  exception. Don't.
- If the import genuinely belongs in both halves (truly shared, with
  no TUI ties), that's a sign you've found a new data-layer package
  that should exist. Create it cleanly; don't reach across.

### 3. The `Source` interface is the universal contract. Don't extend it lightly.

Adding a new source type means writing one Go file (~50 LOC) that
satisfies `Source` (and optionally `StreamingSource`). It does NOT
mean changing the screen, the bindings, or any other source. That's
the whole point of having one interface.

If you find yourself wanting to extend the `Source` interface itself —
stop. Most "I need richer source semantics" are actually:

- "I need a new field on the source's config" — add to `cfg.Source`
- "I need to compose sources" — that's `merge` (leaf) or `union` (operator)
- "I need transformations" — that's an operator pipeline (filter /
  project / derive / sort / cache), or wrap with `exec` if the
  transform belongs in a CLI

The one legitimate extension we've added is `StreamingSource` for the
fundamentally different lifecycle of push-based events. Don't add more
unless you have a comparable structural reason.

### 4. Selection substitution happens at push time, not fetch time.

`${selection.*}` tokens substitute when a screen is pushed — they
become baked-in static strings in the pushed screen's config. The
substitution machinery lives in `internal/build/template.go`. Action
argv is the exception: it substitutes at dispatch time (when the user
hits the action key), so the focused row's *current* selection wins.

When adding new fields that can carry templates, decide consciously
between push-time and dispatch-time. Most new fields want push-time.

### 5. Always strip ANSI when capturing selection.

`color_rules` wrap values with ANSI escapes for display. When that
value flows into `${selection.*}` it must be the logical value, not
the wrapped string. `screen.selectionFrom` calls `xansi.Strip` on
every captured cell / item. Preserve this on any new selection path.

### 6. Modals are state in `Model`, rendered via `ZStack` in `Layout()`.

The pattern for a new modal (in addition to the existing `confirm` /
`alert`):

- Field on `Model`: `xModal *xyz.Model`
- Add a branch in `Layout()` returning
  `ZStack(body, Center(W, H, Sized(m.xModal)))`
- Intercept its result message (e.g. `xyz.DismissedMsg`) at the top
  of `Update`
- Forward all other messages to the modal while it's up
- Add it to `IsCapturingKeys` so the app shell suppresses globals
  while it's open

### 7. Keep examples runnable without external setup where possible.

Examples are the primary documentation. An example that requires the
user to spin up a cluster / get an API token / install something is
costly. Default to examples that work out of the box (file sources,
synthesized exec output, public unauthenticated APIs like
restcountries.com or coingecko). Reserve external-setup examples (kube,
authenticated APIs) for things that can't be demoed otherwise.

### 8. Comment WHY, not WHAT.

Code in this repo has a high "comment density that explains the
non-obvious." Maintain that bar. Examples worth keeping:

- Why we strip ANSI before capturing selection (color leaks into URLs)
- Why each source applies its own root in Fetch (merge composition)
- Why `interactive: false` exists for actions (no alt-screen flicker
  for one-shot commands)
- Why merge returns (data, partial-error) on `on_error: skip` (silent
  failures hide unreachable clusters)

Don't write comments that restate the code. Do write comments that
explain why the obvious-looking alternative was wrong.

## Adding a new component type

A worked example: suppose you're adding a `tabs` component (multiple
sub-screens behind one tab strip). Steps in order:

1. **Schema.** Add to `cfg.Component`: kind-specific fields (`Tabs
   []TabEntry` or similar). Update the type whitelist in
   `Component.validate`.
2. **Build helper.** New `buildTabs` in `internal/build/component.go`
   following the pattern in rule 2. Add `KTabs` to the Kind enum,
   `Tabs *tab.Model` to the Component struct, the case in
   `NewComponent`, the case in `Rebuild`.
3. **Layout wrapper.** Add the case in `componentNode` in
   `internal/build/layout.go`.
4. **Screen fanout.** Add cases in `screen.focusableOf`,
   `componentCapturing`, `componentHelp`, `updateComponent`. These are
   the four places that mention every component Kind. (`focusableOf`
   feeds both `setFocused` and the click-to-focus matching, so the
   new kind gets keyboard focus and mouse focus from the one case.)
5. **Data binding (if applicable).** If the new component takes data,
   add a case in `build.ApplyData` and a matching apply function.
6. **Example.** `examples/<kind>.yaml` demonstrating the smallest
   compelling usage. Heavily commented.
7. **Docs.** Add to `docs/components.md` (schema), README example
   table, and `internal/config/config.go` field comments.

Look at the inspector or logview commits as the template — they each
followed this checklist.

## Adding a new pipeline operator

The pipeline operator catalog is mature (passthrough, filter,
project, derive, sort, union, compose, join, cache). New operators
plug in by following the same shape:

1. **Schema.** Add the operator's fields directly to `cfg.Source`
   (or reuse existing ones — `From` is shared by every single-input
   operator). Add the new `Type` value to `operatorTypes` in
   `source.go`. Add a `case "newkind":` to `Source.Validate` calling
   a new `validateNewKind` method. Add a branch to
   `Source.Upstreams` so cycle detection + Build resolution walk
   the right edges (single-input: one element via `From`;
   multi-input: union over the relevant fields).
2. **Implementation.** Add an operator file in `internal/pipeline/`
   (e.g. `filter.go`, `compose.go`). The `Pipeline` wrapper
   delegates Fetch / Subscribe to its `upstream ds.Source` with
   optional `transformSnapshot` / `transformEvent` hooks; snapshot-
   only operators (sort, compose, cache, join) set
   `disableStreaming: true` so Subscribe returns ErrNotStreaming.
   Operators that need custom execution semantics (compose, join)
   define a private `<op>Source` implementing `ds.Source` and wrap
   it as the Pipeline's upstream. Each `newXxx` takes `*cfg.Source`
   and reads only the fields its kind populates.
3. **Wire it into Build.** Add a `case "newkind":` to the operator
   switch in `pipeline.Build`; if your operator needs raw
   `*cfg.Source` defs (like join, for per-row cloning), they're
   already threaded in via the `sources` arg.
4. **Expressions.** Use `internal/expr.EvalWithParams` /
   `EvalBoolWithParams` so the operator's bound `parameters:`
   surface as `params.X` alongside item fields. Compile once at
   newXxx; pass the compiled program + params closure into the
   per-item evaluation.
5. **Tests.** Unit tests using the in-package `fakeSource` /
   `fakeStreamer` patterns from `pipeline_test.go`. Don't pull in
   real http/exec/file for operator tests — they're transformations,
   so feed them fake data. Use `cfg.NewEntry(&cfg.Source{Type:
   "newkind", From: "src", ...})` for fixtures.
6. **Docs.** New operator section in `docs/data-layer.md`, plus an
   example pipeline in `examples/filter_demo.yaml` (the canonical
   "operator showroom").
7. **wrangl.** Nothing to do — `wrangl <name>` already works
   uniformly across every operator, because they all satisfy
   `ds.Source`. `--list` shows the operator kind via `s.Type`
   directly.

Look at `internal/pipeline/filter.go` and `internal/pipeline/cache.go`
as templates — they cover the transform-hook and custom-source
patterns respectively.

The architectural promise to keep: operators NEVER reach into the
TUI. They're pure data layer. If an operator depends on a screen
construct, the design is wrong.

## Adding a new data source type

A worked example: suppose you're adding an `sse` (Server-Sent Events)
source. Steps:

1. **Schema.** Add per-type fields to `cfg.Source` (probably `URL`,
   `Headers` — already there for http/websocket). Add the new
   `Type` value to `leafTypes` in `source.go`. Add a `case "sse":`
   to `Source.Validate` calling a new `validateSSE` method. Decide
   if it's `ds.Source` or `ds.StreamingSource`.
2. **Implementation.** New `internal/datasource/sse.go` with a
   `newSSE(s *cfg.Source) (ds.Source, error)` constructor. Implement
   `Source` (always) and `StreamingSource` (probably, for SSE).
   Apply `root:` inside `Fetch` per rule 3.
3. **Dispatch.** Add the case in `ds.BuildLeaf` in `source.go`.
4. **Example.** `examples/stream_sse.yaml` or wherever it fits.
5. **Docs.** README + `docs/data-layer.md` source-kind section.

That's it — no screen / binding / build changes. The `Source`
interface is doing its job when adding a source touches just one new
file plus a one-line case.

## Testing

Patterns in `internal/datasource/source_test.go` and
`internal/screen/`:

- **Unit tests for sources** use direct `Build()` + `Fetch()` calls,
  no app shell. Use `httptest.Server` for http; `sh -c 'echo …'` for
  exec; in-memory or `t.TempDir()` for file. See `TestMergeFanout`,
  `TestMergeWithRootedChildren`.
- **End-to-end through the app shell** — spin up stub servers, build
  a `cfg.Config`, construct the screen, wrap in `app.New`, send
  `tea.WindowSizeMsg`, then drain the cmd queue with a bounded
  iteration counter, then assert on `m.View()`. See
  `multi_e2e_test.go`. The cmd-queue drain pattern handles
  `tea.BatchMsg` by unpacking it inline — copy that loop verbatim.
- **TTY-dependent runs** — anything that calls `tea.NewProgram(...).Run()`
  will fail in headless environments with "could not open a new TTY".
  Smoke-test by parsing-and-immediately-erroring; assert on the early
  output via `head -1`. See the per-example smoke loop in commit
  history.

When adding a feature, always pick the lowest-fidelity test that
catches regressions: unit-test the source / build helper / template
substitution before reaching for an end-to-end harness.

## Anti-patterns we've already encountered

These are real foot-guns we've hit. Don't repeat them.

- **Wrapping ANSI-escaped values into URLs.** Fixed via `xansi.Strip`
  at selection capture. Any new selection-flow code path must do the
  same.
- **Calling `SetLoading(true)` on every poll refresh.** Causes
  flicker. The first fetch is allowed; subsequent fetches swap data
  in place without touching loading state. The `entry.loaded` flag in
  `screen.Model` gates this.
- **Silent partial failures in `merge`.** Fixed by returning
  `(data, error)` simultaneously on `on_error: skip` so the user sees
  both halves. Any new composer-style source should follow this
  pattern.
- **Tying source root slicing to the caller.** Previously screen.go
  did `Get(msg.data, entry.root)` after fetch — broke merge because
  children's roots never applied. Fixed by moving root into `Fetch`.
  Don't move it back out.
- **Stdout tee to the terminal for action errors.** Old behavior wrote
  subprocess stderr to both the alt-screen AND a buffer. Caused
  garbled output behind the alert modal. Fixed by capturing stderr
  only to the buffer; alert shows it cleanly.
- **Backgrounding subprocesses via task on macOS.** `nohup` + `disown`
  + closed stdin isn't enough; task's shell cleanup reaps the child.
  Solution: foreground tasks per terminal. Don't try to revive the
  background pattern without a different mechanism (e.g., `setsid`
  via util-linux, or a tiny daemonize helper binary).

## Known limitations

- **Streams leak on screen pop.** When a screen with a streaming
  source is popped, the source's goroutine keeps running. v1 accepts
  this; sessions are short and OS reaps subprocesses on exit. A
  cleaner fix would require a "screen popped" hook in tuilib's stack
  (not present today).
- **List streaming.** Logview and table now support streaming. Lists
  don't yet — would need the same JSON-frame-to-row projection used
  by table streaming (just one column instead of N). Add when a
  use-case demands it.
- **Tree streaming + tree data binding.** Tree is currently
  static-only. Adding dynamic tree data is possible but the
  natural shape isn't obvious (do you re-key by path? Replace
  subtree wholesale?). Wait for a use case.
- **Inspector list/table streaming.** Same as above.

When extending into these areas, design the protocol first; write the
example second; implement third. Don't ship a half-shape.

## Don't

- **Don't run `git add`, `git commit`, or `git push`** without explicit
  user instruction. The user owns the staging set, commit boundaries,
  and push timing. Read-only git inspection (`git status`, `git diff`,
  `git log`) is fine anytime.
- **Don't refactor surrounding code while implementing a feature.**
  Bug fix → fix the bug. New feature → add the feature. Cleanup is
  its own task with its own approval.
- **Don't add error handling for situations that can't happen.**
  Internal calls are trusted; validation happens at config-load time.
  Don't sprinkle defensive checks on internal call sites — they
  obscure the real edges of the system.
- **Don't write documentation files unless asked.** Update existing
  docs when you change behavior; don't introduce new ones
  speculatively. `README.md`, `docs/components.md`, and this file are
  the canonical docs — keep them current, don't proliferate.

## When in doubt

- Read the closest example. Examples are heavily commented and meant
  as adapt-this material.
- Read [`docs/components.md`](docs/components.md) for the full schema.
- Read the file-level package doc comments in `internal/datasource/`,
  `internal/build/`, `internal/screen/` — each opens with a paragraph
  on what the package owns and why.
- Look at tuilib's [CLAUDE.md](https://github.com/jsdrews/tuilib/blob/main/CLAUDE.md)
  for the upstream's rules. tui-builder inherits them implicitly via
  the build helpers, but understanding the source-level conventions
  helps when our wrappers need to grow.
