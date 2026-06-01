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

tui-builder is a YAML → tuilib bridge. There are three layers:

```
   YAML config  ──►  internal/config  ──►  internal/build  ──►  internal/screen ──►  tuilib pkg/app
                    (structs + valid.)    (cfg → live comps)    (screen.Screen)
```

- **`internal/config`** owns the schema. One Go struct per YAML shape.
  Yaml tags drive Unmarshal; `Validate()` walks the tree.
- **`internal/build`** maps `cfg.Component` → live tuilib components
  (`pkg/list.Model`, `pkg/table.Model`, …) by populating the right
  `Options` struct and calling `New(opts)`. Also owns layout-tree
  construction, data binding (`ApplyData`), template substitution
  (`${selection.*}` / `${env.*}`), and color-rule evaluation.
- **`internal/screen`** is one config-driven `screen.Screen` impl.
  Holds focus state, the data-source registry, modal state (confirm /
  alert), action dispatch, and the streaming-event pump. Multi-screen
  is implemented as the same `Model` type pushed/popped from tuilib's
  `screen.Stack`.

The CLI binary (`cmd/tui-builder`) is ~80 lines: load YAML, build the
root screen, hand to `app.New`, run. The launcher (`cmd/example-launcher`)
is a list-of-yamls + a `screen.Push` per selection.

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

## How a YAML field becomes pixels

Trace the trip for `value: name.common` on a table column bound to an
HTTP source:

1. **Parse.** `config.Load(path)` reads the file, unmarshals into the
   `Config` struct in `internal/config/config.go`.
2. **Validate.** `Config.Validate()` checks per-type field shape,
   cross-references (source-name resolution, merge cycle detection),
   and component / data-source bindings.
3. **Build sources.** `datasource.Build(defs)` constructs every source.
   Topologically-correct order via recursive build — leaves (http,
   exec, file, websocket) first, merges last with their resolved
   children attached.
4. **Build components.** `build.Build(layout, components, theme)`
   walks the layout tree, constructs each component leaf via the
   matching `buildList` / `buildTable` / etc. function.
5. **Bind.** `screen.Model.build_()` wires components-by-source-name
   into a registry (`m.sources[name]`).
6. **OnEnter.** Screen kicks off `startFetch(name)` per source.
7. **Fetch.** `http.Fetch` GETs the URL, parses JSON, applies the
   source's own `root:` slicing, returns the resulting value.
8. **Apply.** `build.ApplyData(component, data, theme)` dispatches by
   `Component.Kind`. For tables: walk `items := ds.Iter(data)`, then
   for each row + column, call `applyColorRules(ds.FirstString(item,
   col.Value), col.ColorRules, theme)` and hand the assembled `[]Row`
   to `table.SetRows`.
9. **Render.** tuilib's `pkg/table` does the actual rendering on next
   View().

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
existing multi-screen + `on_enter`. Don't add a `drilldown:` field.

If the user request *can't* be expressed in YAML, the smallest extension
is the right answer:

- New leaf-level capability per kind → new field on `cfg.Component`
- New behavior across kinds → new field on `cfg.DataSource`
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

### 3. The `Source` interface is the universal contract. Don't extend it lightly.

Adding a new source type means writing one Go file (~50 LOC) that
satisfies `Source` (and optionally `StreamingSource`). It does NOT
mean changing the screen, the bindings, or any other source. That's
the whole point of having one interface.

If you find yourself wanting to extend the `Source` interface itself —
stop. Most "I need richer source semantics" are actually:

- "I need a new field on the source's config" — add to `cfg.DataSource`
- "I need to compose sources" — that's `merge`
- "I need transformations" — wrap the source with `exec` (any CLI
  that prints JSON is a source)

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
4. **Screen fanout.** Add cases in `screen.setFocused`,
   `componentCapturing`, `componentHelp`, `updateComponent`. These are
   the four places that mention every component Kind.
5. **Data binding (if applicable).** If the new component takes data,
   add a case in `build.ApplyData` and a matching apply function.
6. **Example.** `examples/<kind>.yaml` demonstrating the smallest
   compelling usage. Heavily commented.
7. **Docs.** Add to `docs/components.md` (schema), README example
   table, and `internal/config/config.go` field comments.

Look at the inspector or logview commits as the template — they each
followed this checklist.

## Adding a new data source type

A worked example: suppose you're adding an `sse` (Server-Sent Events)
source. Steps:

1. **Schema.** Add per-type fields to `cfg.DataSource` (probably
   `URL`, headers — possibly shareable with `http`). Update the type
   whitelist in `DataSource.validate`. Decide if it's `Source` or
   `StreamingSource`.
2. **Implementation.** New `internal/datasource/sse.go`. Implement
   `Source` (always) and `StreamingSource` (probably, for SSE).
   Apply `root:` inside `Fetch` per rule 3.
3. **Registry.** Add the case in `newLeaf` in `source.go`.
4. **Example.** `examples/stream_sse.yaml` or wherever it fits.
5. **Docs.** README + `docs/components.md` data-source section.

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
