# Data layer reference

The data layer — sources and pipelines — is the heart of tui-builder.
This doc is the canonical schema reference: every source kind, what it
takes, what it emits, what lifecycles it supports, and how parameters
bind across the TUI and wrangl entry points.

For architecture and design rationale, see [AGENTS.md](../AGENTS.md).
For TUI-side schema (components, layout, theming), see
[components.md](components.md). For runnable examples, browse
[`examples/`](../examples/).

---

## Top-level config shape

A tui-builder YAML config has three top-level blocks:

```yaml
app:
  title: My app                # config-wide metadata

data:                          # the data layer — read by wrangl and the TUI
  sources:                     # unified map — every entry carries a `type:`
    countries:       { type: http, url: https://restcountries.com/v3.1/all }
    sorted_countries: { type: sort, from: countries, by: "name.common" }

tui:                           # the presentation layer — ignored by wrangl
  components:
    table: { type: table, source: sorted_countries, columns: [...] }
  screen:                      # single-screen — mutually exclusive with `screens:`
    layout: { component: table }
  # screens / initial:         # multi-screen alternative
```

The split is intentional: `data:` is consumed by both wrangl and the
TUI; `tui:` is only consumed by the TUI binary. Splitting them in YAML
mirrors the package boundary enforced in code (see
`scripts/check-data-layer-boundary.sh`).

Inside `data.sources:`, every entry's `type:` picks its kind from one
of the leaf kinds (`http`, `exec`, `file`, `websocket`, `static`,
`merge`) or operator kinds (`passthrough`, `filter`, `project`,
`derive`, `sort`, `union`, `compose`, `join`, `cache`). Leaves fetch
externally; operators transform an upstream named via `from:` (or
fan out over `sources:` / `parts:` / `lookups:`). There is no
separate `data.pipelines:` block — leaves and operators share one
map.

**Throughout this doc**, snippets that focus on a single kind show
just the entry being discussed — in a real config those entries live
under `data.sources.<name>:`.

---

## The model

Every data source implements one contract:

```go
type Source interface {
    Fetch(ctx context.Context) (any, error)
    Refresh() time.Duration
}

type StreamingSource interface {
    Source
    Subscribe(ctx context.Context) (<-chan Event, error)
}
```

`Fetch` returns the useful value (with `root:` slicing already
applied). `Refresh` reports the polling cadence. Streaming sources
additionally implement `Subscribe`, pushing one `Event` per arriving
frame until cancelled.

Both the TUI binding layer and the wrangl CLI consume sources through
these two interfaces and nothing else. Source kinds (http, exec, file,
websocket, merge) are concrete implementations; the data layer above
the interface doesn't care which one it's reading from.

### Pipelines

Operator entries in `data.sources:` (filter / project / derive /
sort / union / compose / join / cache / passthrough) satisfy the
same `ds.Source` interface as leaf sources. A passthrough is the
simplest operator — `from: <source-or-pipeline>` and a stable
addressable name on top, with no transformation:

```yaml
all_countries:
  type: passthrough
  from: countries        # leaf-source name OR another operator name
```

Components and wrangl target either a leaf or an operator by name;
the binding resolution doesn't distinguish — both are `ds.Source`.

---

## Lifecycle

Lifecycle has two orthogonal axes; `wrangl --list` and `--describe`
report both inferred from the config:

**Cadence** — how the source emits values over time:

| Cadence | When | Inference rule |
|---|---|---|
| `streamed` | event-driven, pushes frames as they arrive | `websocket`, or `exec` / `http` with `follow: true` |
| `polled` | clock-driven, runs Fetch on a refresh tick | `refresh:` set |
| `one-shot` | single fetch | neither of the above |

**Binding** — whether the source needs caller input before it can run:

| Binding | When |
|---|---|
| self-contained | no parameters, or all required params have defaults / env fallbacks |
| `needs params` | at least one required parameter with no default |

Cadence and binding combine: a streaming source can need params
(`pod_logs` follow with namespace + name); a polled source can need
params (`pods` refreshed every 5s, parameterized by namespace). The
inferred label surfaces both:

```
polled (refresh: 30s)
polled (refresh: 5s) · needs params
streamed (follow) · needs params
one-shot · needs params
```

You never write a `lifecycle:` field. The config encodes facts
(parameters, refresh, follow); the label is what falls out.

---

## Parameters

Every source can declare typed input slots via a `parameters:` block.
Callers (wrangl `--param`, TUI push-site `bind:`, programmatic
calls) supply values; the source references them as `${params.<name>}`
in URL / body / headers / argv / etc.

### Schema

```yaml
parameters:
  <name>:
    type: string | int | bool | duration   # informational today; default string
    required: true | false                  # mutually exclusive with default
    default: "..."                          # implies optional
    description: "one-line summary"         # shown in --describe
```

### Resolution order

When a caller binds parameters, the source resolves each declared
param like this:

1. **Explicit caller value** — `--param key=value`, push-site
   `bind:`, programmatic call
2. **Declared `default:`**
3. **Error** if `required: true` and still unbound

Extra params (typos) are rejected — silent typos can't produce a
misleading result.

### Templating

Inside a source, reference parameters with `${params.<name>}`. The
following fields all substitute at construction time:

| Field | Substitution applies |
|---|---|
| `url`, `body`, `method` | ✓ |
| `headers` (values) | ✓ |
| `path` (file source) | ✓ |
| `command` (each argv element) | ✓ |
| `env` (values) | ✓ |
| `initial_messages` (each entry) | ✓ |

The parser leaves `${params.X}` literal when no matching parameter is
declared, so typos surface at fetch time (URL `404` or visible literal)
rather than silent empty-string substitution.

### Other tokens

The same templates can also use:

| Token | Resolves to |
|---|---|
| `${env.NAME}` | `os.Getenv("NAME")` — empty when unset, unless declared under `app.env` (see below) |
| `${selection}` | parent screen's focused list selection (TUI only) |
| `${selection.N}` | parent's table row, 1-based cell index (TUI only) |
| `${selection.COLNAME}` | parent's table row, cell by column-title prefix (TUI only) |
| `${prompt.KEY}` | action prompt value (actions only) |

### Declaring env-var dependencies (`app.env`)

An unset env var referenced in a URL / Command / Header silently
substitutes to the empty string. That's a footgun — you get a cryptic
`HTTP 401` or a double-slash URL instead of "you forgot to export
`AWX_TOKEN`." Declare the dependency under `app.env` and Load-time
checks catch it early:

```yaml
app:
  title: AWX
  env:
    - name:        AWX_HOST
      required:    true
      description: "Tower base URL (e.g. https://awx.example.com)"
    - name:        AWX_TOKEN
      required:    true
      description: "OAuth2 bearer token, from Users → Tokens"
    - name:        DEBUG
      default:     "0"
      description: "Set to 1 for verbose fetch logging"
```

Behavior:

- **`required: true` + unset (no default)** → hard error at `Load`.
  Every missing required var lands in one message so you fix the
  whole batch in one edit.
- **`default:` set + unset** → `os.Setenv` applies the default. Same
  shape as `app.prompts` defaults.
- **`${env.X}` referenced but not declared under `app.env` AND
  unset** → stderr warning. Not an error because empty-string
  substitution is a legitimate pattern for some fields (optional
  headers, feature-flag vars). Declare it if you want to elevate to
  a hard error.
- **`app.prompts.key` counts as declared** — those vars are filled
  in by the boot-time TUI form modal, so no warning even though
  they're not under `app.env` at Load time.
- **`required:` and `default:` are mutually exclusive** — defaults
  imply optional.

Applies to both `wrangl` and `tui-builder`. wrangl exits with a
non-zero status on missing required, which CI can catch.

`${selection.*}` and `${prompt.*}` only resolve in TUI contexts —
they don't make sense to wrangl. The recommended pattern post-params:
keep `${selection.*}` only inside push-site `bind:` blocks; let the
source's URL reference `${params.*}` exclusively. That makes the
source equally consumable from the TUI and from wrangl.

### Binding at push sites (TUI)

When the TUI pushes from screen A to screen B and B's sources need
parameters, declare them inline at the push site:

```yaml
pods:
  on_enter:
    - source: pods_table
      push: detail
      bind:
        namespace: ${selection.Namespace}
        name:      ${selection.Name}
```

The bind values are resolved against the focused row at push time
and fed into the destination screen's parameterized sources. Missing
required params surface immediately as an alert modal, not at fetch.

### Binding from wrangl

Use `--param` (repeatable). Pipelines route to their underlying
source automatically:

```sh
wrangl examples/kube.yaml pods --param namespace=default
wrangl examples/kube.yaml pod_detail --param namespace=default --param name=nginx-xyz
```

`wrangl <config> <target> --describe` prints the parameter schema for
a single target so you know what to bind.

---

## Source kinds

### `http`

GET / POST / etc. a URL; parse the response.

**Use for:** REST APIs, JSON endpoints, kube proxies, GraphQL (via
POST body), anything HTTP-shaped.

| Field | Required | Notes |
|---|---|---|
| `type: http` | ✓ | |
| `url` | ✓ | template; supports `${params.*}`, `${env.*}`, `${selection.*}` |
| `method` | ✗ | default `GET` |
| `headers` | ✗ | `map[string]string`; values templated |
| `body` | ✗ | string; templated. Method auto-promotes to POST if `GET` is current default |
| `format` | ✗ | `json` (default) parses body as JSON; `text` keeps raw string |
| `root` | ✗ | dot-path into JSON response, picks the iterable root |
| `refresh` | ✗ | duration string (e.g. `30s`); enables polling (ignored with `follow: true`) |
| `timeout` | ✗ | duration string; default `10s` (ignored with `follow: true` — streams are long-running by design) |
| `follow` | ✗ | `true` switches to streaming: holds the request open, reads body line-by-line, emits one Event per line. Use for kube `?follow=true` log endpoints, SSE streams, NDJSON change-feeds |
| `parameters` | ✗ | the params block above |

**Emits:**
- One-shot / polled: typed JSON value (`format: json`) or raw text
  string (`format: text`). With `root:` set, returns just the
  iterable.
- Streaming (`follow: true`): one `Event{Line: ...}` per response-body
  line until the connection closes or ctx cancels.

**Cadence:** streamed when `follow: true`; polled when `refresh:` set;
otherwise one-shot. Binding: `needs params` when any required
parameter has no default.

### `exec`

Run a subprocess; consume its stdout.

**Use for:** wrapping any CLI that emits JSON (`kubectl get -o json`,
`gh api`, `terraform output -json`, custom scripts), or any process
that streams text (`kubectl logs -f`, `tail -f`, `journalctl -f`).

| Field | Required | Notes |
|---|---|---|
| `type: exec` | ✓ | |
| `command` | ✓ | argv; first element is looked up in `$PATH`. Each element templated |
| `env` | ✗ | additions/overrides on top of inherited env; values templated |
| `follow` | ✗ | `true` switches to streaming — subprocess runs and stdout is read line-by-line as it arrives |
| `format` | ✗ | `json` (default) parses captured stdout; `text` keeps as string |
| `root` | ✗ | dot-path into JSON output |
| `refresh` | ✗ | re-run interval (ignored when `follow: true`) |
| `timeout` | ✗ | hard cap per run |
| `parameters` | ✗ | per-param substitution into argv / env |

**Emits:** typed JSON value (capture mode) or text frames (`follow: true`,
one event per stdout line).

**Lifecycle:** streamed when `follow: true`; polled when `refresh:`
set; on-demand when required params without defaults; otherwise
one-shot.

### `static`

Inline data declared right in the YAML. No I/O, no network.

**Use for:** fixtures, lookup tables (region codes → names, status
labels), pipeline tests without external dependencies, demos that
work offline.

| Field | Required | Notes |
|---|---|---|
| `type: static` | ✓ | |
| `data` | ✓ | inline YAML payload — list, object, scalar, anything |
| `root` | ✗ | dot-path slicing into the data (mirrors http / file `root:`); useful when the inline value is a wrapper like `{items: [...]}` |

```yaml
# Flat list
regions:
  type: static
  data: [us-east-1, us-west-2, eu-west-1]

# List of objects
people:
  type: static
  data:
    - {name: Ada Lovelace,    role: Engineer}
    - {name: Grace Hopper,    role: Director}

# Wrapper object + root slicing
pods:
  type: static
  root: items
  data:
    kind: PodList
    items:
      - {metadata: {name: pod-a}, status: {phase: Running}}
      - {metadata: {name: pod-b}, status: {phase: Pending}}
```

**Emits:** the `data:` payload as-is (after `root:` slicing).

**Lifecycle:** one-shot. No streaming, no refresh.

Static sources compose with every pipeline operator — filter, sort,
join, etc. — exactly like any other source, which makes them a
natural scaffold for developing or testing a pipeline before pointing
it at a real upstream.

### `file`

Read a file off disk.

**Use for:** local fixtures, generated dumps, lab-notebook output.

| Field | Required | Notes |
|---|---|---|
| `type: file` | ✓ | |
| `path` | ✓ | filesystem path; templated |
| `format` | ✗ | `json` (default) or `text` |
| `root` | ✗ | dot-path into JSON content |
| `refresh` | ✗ | re-read interval; without it, file is read once at construction |
| `parameters` | ✗ | substitute into `path` |

**Emits:** typed JSON value or raw text string.

**Lifecycle:** polled when `refresh:` set; on-demand when required
params without defaults; otherwise one-shot.

### `websocket`

Subscribe to a WebSocket; receive frames as events.

**Use for:** live event streams (market data, chat, custom buses).

| Field | Required | Notes |
|---|---|---|
| `type: websocket` | ✓ | |
| `url` | ✓ | `ws://` or `wss://`; templated |
| `headers` | ✗ | sent on the upgrade request |
| `initial_messages` | ✗ | text frames sent immediately after connect (for protocols requiring a subscribe handshake); each entry templated |
| `format` | ✗ | `json` (default) — each frame parsed as JSON; `text` keeps as string |
| `timeout` | ✗ | initial dial cap; ignored once connected |
| `parameters` | ✗ | substitute into URL / headers / initial messages |

**Emits:** one event per arriving frame; channel closes when the
server hangs up.

**Lifecycle:** always streamed.

### `merge`

Fan out to N child sources; union their results; optionally tag each
item with which child it came from.

**Use for:** cross-cluster / cross-account / cross-environment unions
where the resulting view should look like one table.

| Field | Required | Notes |
|---|---|---|
| `type: merge` | ✓ | |
| `sources` | ✓ | list of child source names |
| `tag_field` | ✗ | name of a field injected into every map-shaped item, with the value being the source name (so a downstream column can identify which child the item came from) |
| `on_error` | ✗ | `fail` (default) — any child error aborts; `skip` — drop failed children, return the rest |
| `refresh` | ✗ | merge cadence |
| `parameters` | ✗ | merge doesn't substitute these itself, but children may |

**Emits:** flat list union of children's iterables. With `tag_field`,
each map item gets the child source name injected at that key.

**Lifecycle:** streamed when all children are streamable; polled
when `refresh:` set; falls back to polling when streaming isn't
possible (e.g. mixed children).

**Note:** the children are independent sources. Each child can have
its own parameters; merge doesn't currently forward parent params to
children (a future enhancement).

---

## Pipelines

Each pipeline is exactly one operator on an upstream source or
pipeline. The operator block is a tagged union — set one of
`from:` (passthrough), `filter:`, … — and the validator rejects
configs with zero or multiple operators set.

```yaml
<name>:
  from: <upstream-name>           # passthrough
<name>:
  filter:                          # filter operator
    from: <upstream-name>
    where: <expression>
```

Pipelines are first-class data-layer nodes:

- A stable public name decoupled from the underlying source kind (so
  you can swap an `http` source for an `exec` source without touching
  any component or wrangl invocation).
- A graph point where transforms (`filter`, `project`, `derive`,
  `sort`), composers (`union`, `compose`), joins, and caches plug
  in without changing the binding contract.
- Their own typed `parameters:` block, bindable via wrangl `--param`
  and visible in operator expressions as `params.X`.

Components and wrangl can target either sources or pipelines by name.
The `Registry.Get(name)` lookup checks pipelines first.

**Hidden / private convention**: names prefixed with `_` are treated
as "hidden" by `wrangl --list` — they don't show up in the default
listing but stay fully callable from wrangl, from component
bindings, and as upstreams in other pipelines. Use this for raw
intermediate pipelines you don't want consumers binding to directly:

```yaml
_raw:        { from: kubectl_clusters_raw }           # private
_kind_only:  { filter: { from: _raw, where: "hasPrefix(name, 'kind-')" } }
clusters:    { project: { from: _kind_only, keep: { name: name, server: cluster.server } } }
```

`wrangl --list <config>` shows only `clusters`. `wrangl --list --all`
(or `-a`) reveals everything.

**Operator catalog at a glance**:

| Operator | Arity | Shape | Streams |
|---|---|---|---|
| `from:` (passthrough) | 1 → 1 | identity wrapper | yes (delegates) |
| `filter:` | 1 → 1 | drop items not matching predicate | yes (per-event check) |
| `project:` | 1 → 1 | rebuild each item from declared keys | yes |
| `derive:` | 1 → 1 | add computed fields per item | yes |
| `sort:` | 1 → 1 | reorder by key expression | no (snapshot only) |
| `union:` | N → 1 | flatten homogeneous children, tag rows | yes (when all children stream) |
| `compose:` | N → 1 | bundle heterogeneous children into named buckets | no |
| `join:` | driver + lookups → 1 | per-row lookup fetch + enrichment | no |
| `cache:` | 1 → 1 | TTL-memoise upstream Fetch | streams pass-through |

### Inline `pipe:` chains

When a chain's intermediate steps aren't useful on their own, declare
them inline via `pipe:` instead of giving each one a name:

```yaml
short_summary:
  from: users
  pipe:
    - filter:  { where: "len(username) >= 8" }
    - project:
        keep:
          username: username
          name:     name
          domain:   "lower(website)"
    - sort: { by: username }
```

This desugars at load into a chain of anonymous pipelines linked by
`from:` — `_short_summary_step1` (filter), `_short_summary_step2`
(project), and the user-facing `short_summary` becomes the final
`sort`. The intermediates use the hidden-name convention so they
stay out of `wrangl --list` by default (`--list --all` reveals them).

Rules:

- `from:` on the parent pipeline is required — it's the input to the
  first stage.
- `from:` on a stage operator is forbidden — stage input is implicit
  (previous stage's output, or the parent's `from:` for stage 1).
- Each stage is exactly one operator. Stages are constrained to
  single-input operators (`filter`, `project`, `derive`, `sort`,
  `cache`). Multi-input operators (`union`, `compose`, `join`) take
  extra upstreams that don't fit the implicit-previous-step model —
  declare those as named pipelines and feed them via `from:`.
- `pipe:` is mutually exclusive with the single-operator fields on
  the same pipeline.
- A single-stage `pipe:` is sugar for the operator itself — no
  intermediate pipelines are created.

When to break the chain into named pipelines instead:

- An intermediate step is independently useful (other pipelines or
  components consume it directly).
- You want to attach `parameters:` to one of the intermediate steps
  (parameters live on a named pipeline, not on a stage).

#### `pipe:` on a source

The same sugar works directly on a leaf source. A source declared as

```yaml
biz_only:
  type: http
  url: https://example.test/users
  pipe:
    - filter: { where: "hasSuffix(website, '.biz')" }
```

desugars at load into a `_biz_only_raw` leaf source (hidden) and a
pipeline `biz_only` with `from: _biz_only_raw, pipe: [...]`. The
user-facing name is still `biz_only` — components and wrangl bind to
the transformed output, and the raw response stays addressable as
`_biz_only_raw` for debugging.

Use when the source you consume downstream is always the transformed
shape and the raw response isn't independently useful. Rules:

- `type:` must be set to one of the leaf kinds (http / exec / file /
  websocket / static). Operator kinds (filter / sort / etc.) are
  declared as their own entries with `from:` pointing at this one.
- `type: merge` is rejected — merge is multi-input and doesn't fit
  the implicit-previous-step model. Declare a pipeline with `union:`
  or `compose:` if you need transforms on merged data.
- Stage rules are identical to pipeline-level `pipe:` above.
- `parameters:` on the source still bind to the raw fetch — `wrangl
  --param key=val biz_only` routes through to `_biz_only_raw` since
  the synthesized pipeline declares no parameters.

Runnable example: `examples/inline_pipe_demo.yaml`.

### Pipeline parameters

A pipeline can declare its own `parameters:` block — same shape as
source parameters (type, required, default, description). Bound
values are available in every operator expression as `params.<name>`,
alongside the item being processed.

```yaml
long_usernames:
  parameters:
    min: { type: int, default: "8", description: "minimum username length" }
  filter:
    from: users
    where: "len(username) >= int(params.min)"
```

| Where bindings come from | Behavior |
|---|---|
| **wrangl `--param key=value`** when target is a pipeline with parameters declared | binds to the PIPELINE (its operator expressions see `params.X`) |
| **wrangl `--param`** when target is a source, or a pipeline with no parameters | falls through to source binding (existing behavior; backwards compatible) |
| **Defaults** | applied for params the caller didn't supply |
| **Required params, unbound** | Build fails fast with a clear error |

**Inspect** what a pipeline takes with `wrangl <config> <pipeline> --describe`
— pipeline parameters appear under a "Pipeline parameters:" section.

**Resolution rules** (shared with source params, via `cfg.ResolveParams`):
- Explicit caller value → declared default → error if required.
- Extra params (typos) are rejected.

**Scope**: pipeline params are visible only inside the pipeline's own
operator expressions. They do NOT auto-forward to upstream source
parameters. If an upstream source needs its own params, target the
source directly with `wrangl --param` (or future explicit forwarding
once it's designed).

**In expressions**: `params.X` is its own namespace, separate from the
item's top-level fields. So `params.threshold` is the pipeline param;
`status.phase` is the item field. They don't collide unless your data
literally has a top-level `params` key.

### `from:` — passthrough

```yaml
pods:
  from: pods_raw
```

`Fetch` / `Subscribe` delegate unchanged. Useful as a stable public
name even when no transform is needed yet.

### `filter:` — drop items not matching a predicate

```yaml
running_pods:
  filter:
    from: pods
    where: "status.phase == 'Running'"

errors_only:
  filter:
    from: app_logs                          # streaming text source
    where: "item contains 'ERROR'"          # `item` = the raw line
```

**Inputs / outputs**:

| Upstream shape | Output |
|---|---|
| `[]any` snapshot | the subset of items where `where:` is truthy |
| single object / scalar snapshot | the value if `where:` is truthy, else `nil` |
| streaming JSON-frame events | events whose parsed payload passes; others drop silently |
| streaming text-line events | events whose raw line passes (predicate uses `item` for the line); others drop |

**Expression environment**:

- For JSON payloads, every top-level field is available unprefixed:
  `status.phase`, `metadata.namespace`, `spec.containers.0.image`.
- For non-JSON text events, the raw line is bound to `item`:
  `item contains 'ERROR'`, `hasPrefix(item, '[WARN]')`.
- Built-in functions: `lower`, `upper` (our wrap), plus expr-lang
  natives — `len`, `hasPrefix`, `hasSuffix`, `indexOf`, `now`,
  `parseTime`, `string contains substring` (infix), `string matches
  regex` (infix), `string in array` (membership), `&&` / `||` / `!`,
  comparisons (`==`, `!=`, `<`, `>`, `<=`, `>=`), arithmetic.
- Missing fields evaluate to `nil` (we compile with
  `AllowUndefinedVariables`), so `metadata.namespace == 'default'`
  on an item without `metadata` returns false rather than erroring.

**Composability**: a filter's upstream can be another pipeline,
including another filter — operators chain through `from:`.

**Errors**: compile errors surface at Build time (config load
fails with a clear "where: …" message). Runtime errors on a
per-item evaluation surface as a stream error event or a fetch
error for snapshots.

See [`examples/filter_demo.yaml`](../examples/filter_demo.yaml) for a
runnable demo against jsonplaceholder.

### `project:` — slim items down to declared output keys

```yaml
user_summary:
  project:
    from: users
    keep:
      id:       id
      username: username
      domain:   "lower(website)"
      city:     "address.city"
```

Each `keep:` value is an expression evaluated against the input item.
Bare dot-paths (`address.city`) are the simple case; anything the
expression language can produce is valid (`lower(website)`,
`len(items)`, `status.phase == 'Running' ? 'ok' : 'bad'`).

| Upstream shape | Output |
|---|---|
| `[]any` of maps | `[]any` of slimmer maps; non-map items drop |
| single map | one projected object |
| scalar / nil | `nil` |
| streaming JSON-frame events | events re-encoded as the slimmer JSON |
| streaming text-line events | drop (no fields to project from a raw string) |

Rename + flatten in one operator — Project's output key is the LEFT
side of each `keep:` entry, the source expression is the RIGHT. So
`name: metadata.name` lifts a deep field up to a top-level `name`.

### `derive:` — copy items and add computed fields

```yaml
users_with_labels:
  derive:
    from: users
    compute:
      is_biz:       "hasSuffix(website, '.biz')"
      username_len: "len(username)"
      is_kube:      "hasPrefix(metadata.namespace, 'kube-')"
```

Every original field survives; each `compute:` entry adds a new field.
Collisions are intentional — a compute key that matches an existing
field overrides it, letting you reshape an awkward source value in
place without writing a separate project.

| Upstream shape | Output |
|---|---|
| `[]any` of maps | `[]any` where each map gets the computed fields added |
| `[]any` with non-maps | non-maps pass through unchanged |
| single map | one extended object |
| scalar / nil | unchanged |
| streaming JSON-frame events | events re-encoded with the extra fields |
| streaming text-line events | pass through unchanged |

Mutations are copy-on-write — derive never alters the upstream items.

### `sort:` — reorder items by a key expression

```yaml
pods_by_restarts:
  sort:
    from: pods
    by: "status.containerStatuses.0.restartCount"
    order: desc

alphabetical:
  sort:
    from: users
    by: "lower(name)"
```

| Field | Required | Notes |
|---|---|---|
| `from:` | ✓ | upstream source / pipeline |
| `by:` | ✓ | expression evaluated per item to produce the sort key |
| `order:` | ✗ | `asc` (default) or `desc` |

| Upstream shape | Output |
|---|---|
| `[]any` | reordered slice; comparison is stable on ties |
| non-iterable (single map / scalar / nil) | passed through unchanged |
| streaming | `Subscribe` returns `ErrNotStreaming` — consumers fall back to polling via `Fetch` |

**Comparison rules**:

- Numbers (`int`, `int64`, `float64`) compare numerically.
- Strings compare lexicographically.
- `bool`: `false < true`.
- `time.Time`: earlier before later.
- Mixed-type keys (rare) fall back to string-representation
  comparison so a heterogeneous list still produces a deterministic
  order.
- `nil` keys (missing field) sort BEFORE other values in ascending
  order — matches `kubectl`/`jq` convention; missing-key items
  cluster at the top.

**Streaming caveat**: sort is snapshot-only. Sorting a true event
stream needs windowing semantics (sort the last N events, sort
within a tumbling window, etc.) which haven't been built yet. If you
hand sort a `websocket` or `exec --follow` upstream, the pipeline
returns `ErrNotStreaming` on `Subscribe` rather than silently
degrading to "sort just what's arrived so far."

### `union:` — compose N upstreams into one flat iterable

Same composition semantics as the [`merge` source](#merge) — including
per-child tag injection nested under `meta_key:` — but lives in the
pipeline layer so children can be **any** `ds.Source`: leaf sources OR
other pipelines.

```yaml
# Shorthand: same as `merge sources: + tag_field:`. Each child gets
# one synthetic tag whose value is the child source name.
all_pods:
  union:
    sources: [pods_prod, pods_staging, pods_dev]
    tag_field: cluster
    on_error: skip

# Long form: per-child arbitrary tags. Use when rows need more
# metadata than the source name.
all_pods:
  union:
    children:
      - source: pods_prod
        tags: { cluster: prod,    cluster_url: "http://localhost:8001" }
      - source: pods_staging
        tags: { cluster: staging, cluster_url: "http://localhost:8002" }
    on_error: skip
```

| Field | Required | Notes |
|---|---|---|
| `sources:` | one of `sources:` / `children:` | shorthand: list of child names |
| `tag_field:` | ✗ | shorthand only; the tag key under `meta_key` |
| `children:` | one of `sources:` / `children:` | long form: list of `{source, tags}` entries |
| `on_error:` | ✗ | `fail` (default) — any child error aborts; `skip` — drop the failed child, return the rest |
| `meta_key:` | ✗ | tag namespace, default `_meta`; explicit `""` opts out and writes tags flat at the top level |

**The pipeline-as-child win**:

```yaml
# Filter each cluster's pods first, THEN union. The merge SOURCE
# can't do this — its children must be sources.
prod_running:
  filter: { from: pods_prod,    where: "status.phase == 'Running'" }
staging_running:
  filter: { from: pods_staging, where: "status.phase == 'Running'" }

all_running:
  union:
    sources: [prod_running, staging_running]
    tag_field: cluster
```

Streaming follows merge's rule: union is streaming when every child
streams; if any child can only Fetch, the whole union falls back to
polling.

### `compose:` — bundle N heterogeneous upstreams into one object

Where `union` *flattens* N homogeneous iterables into one big list,
`compose` *preserves* the separation — each child becomes a top-level
key in an output object. Useful for screens or wrangl consumers that
want multiple unrelated data shapes from one addressable target.

```yaml
fleet:
  compose:
    parts:
      pods:        pods_all
      deployments: deployments_all
      services:    services_all
    on_error: skip
  # Output: {pods: [...], deployments: [...], services: [...]}
```

| Field | Required | Notes |
|---|---|---|
| `parts:` | ✓ | map of output_key → upstream source / pipeline name |
| `on_error:` | ✗ | `fail` (default) — any child error aborts; `skip` — drop the failed bucket, return the rest (only errors if every child fails) |

**Lifecycle**:

- Snapshot only — `Subscribe` returns `ErrNotStreaming`. "Compose of
  streams" is ambiguous (each child emits its own events; merging
  them into one composed event would need fan-in semantics we
  haven't designed). For streaming consumers, subscribe to
  individual children directly.
- Children are fetched in parallel; the slowest child caps total
  latency.
- Children can be any `ds.Source` — leaf sources OR other pipelines
  (filter, project, union, even nested compose).

**vs. `union`**:

|  | `union` | `compose` |
|---|---|---|
| Output shape | flat `[]any` | `map[string]any` |
| Child shapes | must be homogeneous (rows from each child become rows in the union) | heterogeneous — each bucket keeps its own shape |
| Per-child tags | yes (`tag_field:` / `tags:`) | no (the output key already identifies origin) |
| Streaming | yes when all children stream | no |

### `join:` — enrich each driver row with per-row lookup fetches

Takes a **driver** iterable and one or more **lookups**; for each
driver row, every lookup is invoked with params computed from that
row, and the row gets enriched with the result. The kubectl-describe-
on-tree-highlight pattern, expressed at the data layer.

```yaml
data:
  sources:
    pods:
      type: http
      url: http://localhost:8001/api/v1/namespaces/default/pods
      root: items

    pod_detail:
      type: http
      parameters:
        namespace: { type: string, required: true }
        name:      { type: string, required: true }
      url: http://localhost:8001/api/v1/namespaces/${params.namespace}/pods/${params.name}

  pipelines:
    pods_with_detail:
      join:
        driver:
          from: pods                  # fetches once, yields N rows
        lookups:
          detail:
            from: pod_detail          # invoked PER row with row-derived params
            on:
              namespace: metadata.namespace   # expression eval'd against the driver row
              name:      metadata.name
        emit: separate                 # or `merged`
        on_error: fail                 # or `skip`
```

| Field | Required | Notes |
|---|---|---|
| `driver.from:` | ✓ | any source or pipeline returning an iterable |
| `lookups:` | ✓ | map of output-bucket-name → lookup spec (one or more) |
| `lookups.<name>.from:` | ✓ | must be a **source** with `parameters:` declared (pipelines as lookups not yet supported) |
| `lookups.<name>.on:` | ✓ | map of lookup param → expression evaluated against the driver row |
| `emit:` | ✗ | `separate` (default) → `{row, <bucket>: ...}` per row; `merged` → lookup fields flattened into row |
| `on_error:` | ✗ | `fail` (default) → abort whole join on any lookup error; `skip` → drop the row from output |

**Output shapes**:

`emit: separate` (default):
```json
[
  {"row": <driver row>, "detail": <lookup result>, "logs": <other lookup result>},
  ...
]
```

`emit: merged`:
```json
[
  {<driver row fields>, <lookup result fields>},
  ...
]
```
Merged requires both the row and every lookup result to be
map-shaped. Key collisions are namespaced (`<lookup_name>_<key>`)
to avoid silent overwrites.

**Execution shape**:

- Driver fetched once; for each row, every lookup runs in parallel.
- Per-row lookup execution: clone the lookup's `*cfg.Source`,
  bind row-derived params via `BindParams`, build a fresh
  `ds.Source` from the bound cfg, fetch.
- No caching (yet). For N driver rows × M lookups, expect N×M
  fetches per Fetch call. An LRU keyed on the params tuple is a
  natural follow-up when N grows.

**Lifecycle**: snapshot-only. `Subscribe` returns `ErrNotStreaming`.
Joining over a driver stream needs windowing semantics (cache the
latest snapshot, fetch lookups lazily) not yet designed.

**Validator catches**:

- Lookup `from:` referencing a pipeline → rejected (v1 lookups must
  be sources).
- Lookup `from:` source without declared `parameters:` → rejected.
- `on:` key not declared as a parameter on the lookup source →
  rejected (catches typos at load time, not at first fetch).

See [`examples/filter_demo.yaml`](../examples/filter_demo.yaml) →
`users_with_posts` for a runnable demo against jsonplaceholder.

### `cache:` — memoise an upstream's Fetch for a TTL

```yaml
cached_users:
  cache:
    from: users
    ttl: 30s
```

Reads within the TTL return the cached snapshot without hitting
the upstream. The first read after the TTL elapses re-fetches.
Errors are NOT cached — failing upstreams are retried on the next
call rather than returning a stale error for the rest of the TTL.

| Field | Required | Notes |
|---|---|---|
| `from:` | ✓ | upstream source or pipeline |
| `ttl:` | ✓ | duration string (`30s`, `5m`, `1h`). Validator rejects malformed values at load time |

**Motivating use case** — shared upstreams. When a downstream
operator graph (typically union / compose) has multiple paths into
the same source, caching the source ensures it's fetched once per
TTL window instead of once per consumer:

```yaml
# Expensive upstream cached for 30s.
cached_pods:
  cache: { from: pods, ttl: 30s }

# Three operators that all read from pods. Without cache, each
# call to `dashboard` would trigger three separate fetches. With
# cache, just one (per TTL window).
running:
  filter: { from: cached_pods, where: "status.phase == 'Running'" }
by_age:
  sort:   { from: cached_pods, by: "status.startTime", order: desc }
summary:
  project:
    from: cached_pods
    keep: { name: metadata.name, phase: status.phase }

dashboard:
  compose:
    parts:
      running: running
      by_age:  by_age
      summary: summary
```

**Streaming**: pass-through. The cache layer only memoises Fetch
snapshots; Subscribe goes straight to the upstream (events are
inherently incremental, not snapshots).

**Concurrency**: the cache uses a mutex but releases it during the
upstream call, so concurrent waiters may briefly race to refresh
together. The common case (sequential consumers within a Fetch
tick) is unaffected. If you need strict single-flight, that's a
follow-up.

### `merge` (source) is still supported

The `merge` kind pre-dates the union operator and remains
fully functional. Under the unified schema the practical difference
has shrunk — both fan out N upstreams — but `union` reads more
naturally for "pipeline operator" intent, while `merge` carries leaf
fields (`refresh`, `timeout`) that don't apply to a pure transform.
Prefer `type: union` for new configs; existing `type: merge` entries
keep working unchanged.

### Combining operators

Operators chain through `from:` — a filter's upstream can be a
project, a project's upstream can be a derive, etc. The graph is
arbitrary as long as it's acyclic (the validator rejects cycles at
load time).

```yaml
# raw HTTP source
users:
  from: users_raw

# derive labels onto each user
labeled:
  derive:
    from: users
    compute:
      is_biz: "hasSuffix(website, '.biz')"

# keep only the .biz users
biz_only:
  filter:
    from: labeled
    where: "is_biz"

# then project down to a flat shape for the consumer
biz_summary:
  project:
    from: biz_only
    keep:
      name:    name
      website: website
```

The full chain re-evaluates per fetch / per event. Build compiles
every expression once; runtime cost is one map lookup per derive
output + one expr.Run per filter check + one expr.Run per project
key.

**Built-in name collisions to watch for:** the expression language
(expr-lang) reserves some identifiers as built-ins (`count`, `sum`,
`len`, `keys`, `values`, `filter`, `map`, `all`, `any`, `one`,
`none`, `sort`, `sortBy`, `contains`, `startsWith`, `endsWith`).
Item fields named after these can't be referenced bare. Use
`project:` to rename the offending field before downstream operators
touch it, or pre-process the upstream payload to use a different
key.

---

## wrangl: the data-layer CLI

`wrangl <config.yaml>` runs the data layer with no TUI loaded. Use it
to inspect, dump, or pipe.

| Invocation | Behavior |
|---|---|
| `wrangl <config>` | prints inventory of every source + pipeline (alias for `--list`) |
| `wrangl --list <config>` | same |
| `wrangl --list --all <config>` (or `-a`) | include hidden items — by convention names starting with `_` (private intermediates) are omitted from the listing but stay fully callable |
| `wrangl <config> <target>` | dumps the named source/pipeline to stdout |
| `wrangl <config> <target> --describe` | prints schema (kind, lifecycle, URL, parameters) |
| `wrangl <config> <target> --param k=v` | binds a parameter; repeatable |
| `wrangl <config> <target> --pretty` | indent one-shot JSON output (streams stay NDJSON) |
| `wrangl <config> <target> --raw` | emit string / log-line values as plain text (skips JSON quoting/escaping for `format: text` and streaming text frames) |
| `wrangl <config> <target> --limit N` | cap stream output at N events |
| `wrangl <config> <target> --for D` | cap stream consumption at duration D |

### Output contract

| Source shape | Default stdout | With `--raw` |
|---|---|---|
| `format: json`, one-shot / polled | one JSON value, ending in `\n` (compact; pretty with `--pretty`) | unchanged — there's no raw text representation for parsed JSON |
| `format: text`, one-shot / polled | one JSON-quoted string (`"...\n..."`) | the raw response body, written as-is |
| streaming JSON frames | NDJSON — one JSON value per line | unchanged (the frames already are JSON) |
| streaming text frames (kube logs follow, etc.) | NDJSON of JSON-encoded strings (one quoted line per frame) | plain text, one log line per stdout line — `kubectl logs -f` style |

For streaming sources, errors mid-stream emit as
`{"error": "..."}` lines (even in `--raw` mode) so consumers can
grep them out without losing the line-protocol contract.

**Picking the right shape for log-like sources:** prefer streaming
over polled text. A polled `format: text` source dumps the whole tail
buffer on every refresh tick, which is wasteful and not really
"following." `follow: true` (http or exec) emits new lines as they
arrive — combine with `--raw` for human-readable output.

---

## Architecture: data layer ≠ TUI

The data layer is deliberately independent of the TUI:

- `internal/config`, `internal/datasource`, `internal/pipeline`,
  `internal/output`, `cmd/wrangl` MUST NOT import anything from
  `internal/screen`, `internal/build`, or any `tuilib` package.
- A CI check (`scripts/check-data-layer-boundary.sh`) walks
  `go list -deps` and fails the build on violations.

Why: data wrangling is the product, the TUI is one sink. Wrangl
needs to stay cheap to build/test/link without dragging in Bubble
Tea. If a TUI helper looks like it belongs in the data layer, move
it; don't reach across.

See [AGENTS.md](../AGENTS.md) §3a for the rule statement.
