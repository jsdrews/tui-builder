# tui-builder

Build terminal UIs by writing YAML. Wire HTTP / subprocess / file data
sources into them. Merge sources across clusters, accounts, or
environments. Stream long-running output into a logview. Drill into
multi-screen flows with stable cursor state across refreshes.

Built on top of [tuilib](https://github.com/jsdrews/tuilib): the
component library does the rendering and theming; tui-builder turns
declarative config into a live composition.

**Two binaries, one config.** Data wrangling is a first-class concern,
not a TUI implementation detail:

- **`wrangl`** — runs the data layer only and dumps JSON / NDJSON to
  stdout. Pipe it into `jq`, `duckdb`, `miller`, a notebook, or another
  pipeline. Zero terminal-UI dependencies.
- **`tui-builder`** — uses the same data layer, then renders it
  through tuilib components. The TUI is one sink, not the product.

The `data.sources:` block holds every named, addressable producer of
data — both leaf sources (http / exec / file / websocket / static /
merge) and operator pipelines (`filter`, `project`, `derive`, `sort`,
`union`, `compose`, `join`, `cache`, `passthrough`). Every entry
carries a `type:` discriminator that picks its kind. Operator
expressions use an embedded expression language (`expr-lang`). Any
entry can declare its own typed `parameters:` block, bindable via
wrangl `--param`.

See [`docs/data-layer.md`](docs/data-layer.md) for the full reference.

```
┌ All pods (3 clusters merged) ────────────────────────────────────┐
│ Cluster      │ Namespace │ Name                    │ Status      │
├──────────────┼───────────┼─────────────────────────┼─────────────┤
│ pods_prod    │ default   │ nginx-56c45fd5ff-7p9mw  │ Running     │
│ pods_prod    │ default   │ nginx-56c45fd5ff-dv862  │ Running     │
│ pods_staging │ default   │ api-7d8b6c5b4-x2k7p     │ Running     │
│ pods_dev     │ default   │ broken-544795c8b5-5hxfg │ CrashLoop…  │
│ pods_dev     │ default   │ scratch-7c98...         │ Running     │
└──────────────────────────────────────────────────────────────────┘
   ? help
```

## What you get

| Capability | What it looks like |
|---|---|
| **Declarative TUIs** | One YAML file per app: `data.sources:` + `tui.components:` + `tui.screen:` / `tui.screens:`. No Go to write for the common case. |
| **Components** | `list`, `table`, `inspector`, `tree`, `logview` — every tuilib component except those that don't fit a config model |
| **Data sources** | `http`, `exec`, `file`, `websocket`, `static`, `merge` — plus operator kinds (`filter`, `project`, `derive`, `sort`, `union`, `compose`, `join`, `cache`, `passthrough`) layered on top |
| **Multi-screen** | Push/pop with breadcrumbs; `${selection.*}` substitutes parent row into child config (URL, title, fields) |
| **Streaming** | Long-running `exec` + `websocket` push events into a logview as they arrive |
| **Live data** | Per-source `refresh: <duration>` polling with in-place updates — cursor / filter / sort survive every refresh |
| **Color** | Theme-wide palettes (Nord, Dracula, …) + per-component `colors:` overrides + per-value `color_rules:` ("Running" → green, "ERROR" → red) |
| **Templating** | `${selection.col}` (parent row), `${env.VAR}` (env var) substitute anywhere a string lives — titles, URLs, headers, action argv |
| **Actions** | Named, addressable units of work (`exec` subprocess or `http` request) with typed `inputs:`. Bound to keys per screen; optional `confirm:` modal; interactive (TTY handoff) or captured (streams into the output console). Inspectable headlessly via `wrangl --list-actions` / `--describe-action` |
| **Output console** | One sink for every action result, subprocess line, and fetch error. `o` opens it; an unread badge tints red on failure. No blocking error modals anywhere |
| **Errors** | Failed subprocesses surface in an alert modal with stderr captured; failed merge children surface in the statusbar without blanking the table |

## Quickstart

```sh
git clone git@github.com:jsdrews/tui-builder.git
cd tui-builder
task build            # → bin/tui-builder, bin/example-launcher, bin/wrangl

# Browse every example in a launcher TUI:
task examples

# Or run one directly:
task example NAME=table
task example NAME=http_countries        # live REST API
task example NAME=merge_sources         # local merge of file + 2 exec sources

# Or skip the TUI entirely and pipe data:
go run ./cmd/wrangl --list examples/http_countries.yaml
go run ./cmd/wrangl examples/http_countries.yaml all_countries | jq '.[0].name.common'
```

`task --list` shows the full menu.

## Two ways to consume a config

Once you've written a YAML config with `data.sources:` + (optionally)
`tui.components:` + `tui.screen:`, you can either render it as a TUI
or just dump the data:

```sh
# Render the TUI:
bin/tui-builder examples/http_countries.yaml

# Or dump every defined entry:
bin/wrangl --list examples/http_countries.yaml
# NAME            KIND                     LIFECYCLE              UPSTREAM
# all_countries   pipeline / passthrough   polled (refresh: 5m)   countries
# countries       source / http            polled (refresh: 5m)

# Or pipe one pipeline's output downstream:
bin/wrangl examples/http_countries.yaml all_countries | jq '.[0]'
bin/wrangl --limit 50 examples/stream_l1.yaml l1 | jq '.data.s'

# Or run a parameterized pipeline (operator expressions get
# `params.X` access alongside item fields):
bin/wrangl examples/filter_demo.yaml long_usernames --param min=12

# Or describe a pipeline's schema (operator kind, lifecycle, params):
bin/wrangl examples/filter_demo.yaml users_with_posts --describe
```

`wrangl` is the same data layer the TUI uses — the architecture
guarantees there's no second pipeline implementation drifting out of
sync. A CI check enforces that `cmd/wrangl` and the data-layer packages
never import any TUI code.

## A first config

```yaml
# hello.yaml
app:
  title: Cities
  theme: nord

tui:
  components:
    cities:
      type: table
      filterable: true
      columns:
        - {title: City,   width: 16, sortable: true}
        - {title: Region, width: 14, sortable: true}
        - {title: Pop,    width: 8,  sortable: true, sort: si, align: right}
      rows:
        - [London,    Europe,   "9M"]
        - [Tokyo,     Asia,     "37M"]
        - [Reykjavík, Europe,   "130K"]

  screen:
    layout:
      component: cities
```

```sh
bin/tui-builder hello.yaml
```

That's it — `tab` cycles focus, `/` filters, `[`/`]`/`s` step the sort
column, `q` quits, and `o` opens the output console.

The mouse works too: click a pane to focus it, click a row to move its
cursor, scroll with the wheel, and double-click a row for `enter` — the
same `on_key: {key: enter}` push or `enter` action the keyboard fires.
Mouse reporting takes over the terminal's own click-drag text selection —
hold `shift` (or `alt` in iTerm2) while dragging to select text for a copy.

## Tour by feature

### Data sources

Bind a component to a source by name. The same dot-path resolver works
across all source types; the same `color_rules` syntax works against
any pluckable value.

```yaml
data:
  sources:
    countries:
      type: http
      url: https://restcountries.com/v3.1/all?fields=name,region,population
      refresh: 5m

tui:
  components:
    countries:
      type: table
      source: countries
      columns:
        - {title: Name,       value: name.common}
        - {title: Region,     value: region}
        - {title: Population, value: population, sort: si, align: right}
```

Source kinds (leaf — fetch externally):

| Type | When | Notes |
|---|---|---|
| `http` | REST APIs, JSON endpoints, kube proxies | `url`, `method`, `headers`, `body`, `format: json\|text` |
| `exec` | Anything that prints JSON: `kubectl get -o json`, `gh api`, `terraform output -json`, custom scripts | Per-source `env:` adds to inherited env |
| `file` | Fixtures, generated dumps, lab notebook output | `refresh: <duration>` re-reads; omit for once |
| `websocket` | Live event streams: chat, custom buses | Headers pass through on the upgrade request |
| `static` | Inline data, fixtures, lookup tables | `data:` carries the value directly — list, object, scalar |
| `merge` | Compose N children, union the rows, tag each item by source — cross-cluster / cross-account / cross-anything | `tag_field:` writes child name into each item; `on_error: skip` returns survivors + a partial-error notice |

Operator kinds (transform an upstream entry):

| Type | What it does |
|---|---|
| `passthrough` | Stable addressable alias over an upstream (`from:`); no transformation |
| `filter` | Drop items whose `where:` predicate doesn't match |
| `project` | Rebuild each item from declared output keys (`keep:`) |
| `derive` | Copy each item and add computed fields (`compute:`) |
| `sort` | Reorder by key expression (`by:`, `order: asc\|desc`) |
| `union` | Flatten N homogeneous upstreams into one list (same shape as `merge` but children can be operators too) |
| `compose` | Bundle N heterogeneous upstreams into one object (`parts: {key: upstream-name}`) |
| `join` | Per-row enrichment: driver iterable + per-row lookup fetches |
| `cache` | TTL-memoise an upstream's Fetch |

See [`docs/components.md`](docs/components.md) for the full schema.

### Paging a remote API

An `http` or `exec` source over more rows than fit in memory has two
options, and they trade against each other:

```yaml
paginate:                  # walk every page up front, hand back one list
  strategy: link
  next_path: next

window:                    # fetch only the rows on screen; the SERVER
  offset_param: offset     # answers the filter and the sort
  limit_param: limit
  total_path: numFound
  search_param: q
  filters: {Author: author}
```

With `window:` the bound table holds one page of a much larger set and
paints the rest as `·` until you scroll there. Typing `author:tolkien`
searches every row the server has, not the hundred that happen to be
resident — which is the part `paginate:` can't do at any page count.
Binding a table to a windowed source is what switches it into that mode;
there's no second flag.

`window:` works on `exec` too — there the request templates into the
argv instead of a query string, via `${window.offset}`,
`${window.limit}`, `${window.search}`, and `${window.filters.<name>}`:

```yaml
command: [myreport, --offset=${window.offset}, --limit=${window.limit},
          --author=${window.filters.author}]
```

An element whose window tokens all resolve empty is dropped, so with no
`author:` term typed the `--author=` flag disappears entirely rather than
being passed empty.

See [`examples/http_window.yaml`](examples/http_window.yaml),
[`examples/exec_window.yaml`](examples/exec_window.yaml), and
[Windowed sources](docs/data-layer.md#windowed-sources).

### Streaming

Long-running subprocess (`exec` + `follow: true`) or WebSocket
connection. Events arrive over time; the bound component receives them
without resetting cursor / filter / scroll.

**Into a logview** — lines append as-is:

```yaml
data:
  sources:
    logs:
      type: exec
      follow: true
      command: [kubectl, logs, -f, "-n", default, my-pod]

tui:
  components:
    log:
      type: logview
      source: logs
      color_rules:
        - {when: "~\\b(ERROR|FATAL)\\b", color: red}
        - {when: "~\\bWARN\\b",          color: yellow}
```

**Into a table — two modes**:

*Ring buffer* (live-tape pattern) — each frame appends, oldest scroll
off past `max_rows`:

```yaml
components:
  trades_table:
    type: table
    source: trades
    max_rows: 100
    columns:
      - {title: Price,  value: data.price_str,  sort: number, align: right}
      - {title: Amount, value: data.amount_str, sort: number, align: right}
```

*Keyed upsert* (L1 / status-grid pattern) — one row per `row_key`,
updates in place when the key recurs, append when the key is new:

```yaml
components:
  l1_book:
    type: table
    source: l1
    row_key: data.s              # symbol = row identity
    columns:
      - {title: Symbol,   value: data.s}
      - {title: Best bid, value: data.b, sort: number, align: right}
      - {title: Best ask, value: data.a, sort: number, align: right}
```

The cursor stays on whichever row you'd selected while ticks flow —
even at hundreds of updates per second.

### Multi-screen drilldown

A screen can push another when the user hits enter on a focused list /
table. The selected row's values flow into the child's config via
`${selection.col}` (table) or `${selection}` (list).

```yaml
data:
  sources:
    pod_detail:
      type: http
      url: http://localhost:8001/api/v1/namespaces/${selection.Namespace}/pods/${selection.Name}

tui:
  screens:
    pods:
      layout: {component: pods_table}
      on_key:
        - {source: pods_table, push: detail, key: enter}
    detail:
      title: ${selection.Name}
      layout: {component: pod_inspector}
  initial: pods
```

`esc` pops; breadcrumbs accumulate at the top of the screen.

### Actions

Actions are the write side, declared in a top-level `actions:` block and
bound to keys by a screen. The split is deliberate: an action says *what
to run* and what inputs it needs; a binding says *which key*, *which
pane's row feeds it*, and *whether to confirm*.

```yaml
actions:
  pod_delete:
    description: Delete a pod
    inputs:
      namespace: {type: string, required: true}
      name:      {type: string, required: true}
    run: [kubectl, delete, -n, "${inputs.namespace}", pod, "${inputs.name}"]
    message: "deleted ${inputs.name}"

tui:
  screens:
    pods:
      actions:
        - key: D
          action: pod_delete
          label: delete
          from: pods_table
          confirm: "Delete ${selection.Name}? Cannot be undone."
          bind:
            namespace: ${selection.Namespace}
            name:      ${selection.Name}
```

An action never mentions `${selection.*}` — that keeps it reusable from
any screen. `from:` is required exactly when a template reads a
selection and rejected otherwise, so an action needing no row simply
omits it and its key fires from anywhere on the screen.

Actions are also addressable from the CLI, which makes them testable
without a TTY:

```sh
wrangl --list-actions examples/kube.yaml
# NAME         KIND   INPUTS              DESCRIPTION
# pod_delete   exec   name*, namespace*   Delete a pod

wrangl --describe-action examples/kube.yaml pod_delete \
  --param namespace=default --param name=nginx-abc
# ...
# DRY RUN
#   kubectl delete -n default pod nginx-abc
```

There is no `wrangl --run`: every safety gate an action has is TUI state,
and a headless caller would bypass all of it.

**HTTP actions.** `type: http` is for APIs with no CLI in the loop —
`method` / `url` / `headers` / `body`, plus a result contract for APIs
that don't use status codes honestly:

```yaml
actions:
  sync_app:
    type: http
    method: POST
    url: ${env.ARGOCD_URL}/api/v1/applications/${inputs.app}/sync
    headers: {Authorization: "Bearer ${env.ARGOCD_TOKEN}"}
    inputs: {app: {type: string, required: true}}
    success: code < 400 or code == 409   # already syncing isn't a failure
    error_message: ${body.message}       # the API's reason, not "→ 403"
```

**Inputs the caller doesn't bind get collected in a form** generated
from the input declarations — there is no separate `prompts:` schema, so
the form can't drift out of step with the argv. The data type picks the
widget: `type: bool` is a toggle, anything with `options:` is a select,
everything else is a text input.

### Where results go

Every action reports to the **app-wide output console** (`o`), and only
there. Its stdout/stderr stream in line by line as they arrive; the
summary line paints the statusbar; a persistent unread badge in the
statusbar's right slot goes red if anything failed and stays until you
read it.

There are no error modals — not for actions, not for failed fetches. The
statusbar's centre slot wipes on the next keypress, which is why modals
existed; the console plus a badge that *doesn't* wipe is the better fix,
and nothing blocks.

Actions also never refresh your views. A view owns its own refresh cycle:
one that should converge after a mutation declares `refresh:`, and `r`
refetches on demand. Coupling a mutation to a repaint would make every
action responsible for knowing which panes it invalidated.

### Color rules

Per-column for tables, per-field for inspectors, top-level for
lists/logviews. Same matcher syntax everywhere:

```yaml
color_rules:
  - {when: Running,             color: green}        # exact string
  - {when: "~CrashLoopBackOff", color: red}          # regex
  - {when: ">5",                color: red}          # numeric > 5
  - {when: ">=2M",              color: bright_red}   # SI-suffix numeric
  - {when: "",                  color: gray}         # wildcard / default
```

Colors accept named (`red`, `bright_green`, `gray`), 0-255 indices
(`"160"`), hex (`"#ff8800"`), and theme tokens (`theme:accent`).

## Examples

Run any via `task example NAME=<filename-without-yaml>`, or browse all
via `task examples`.

| File | What it shows |
|---|---|
| `examples/list.yaml` | Filterable list, navigation keys |
| `examples/table.yaml` | Filterable + sortable table, filter syntax (`key:value`, `~regex`) |
| `examples/table_columns.yaml` | Column sizing (fixed / auto / flex / max_width) + alignment |
| `examples/table_styled.yaml` | Colored cells + clickable hyperlinks, `initial_sort` |
| `examples/inspector.yaml` | Two-column label/value record viewer, nested groups |
| `examples/tree.yaml` | Hierarchical view, expand/collapse, search |
| `examples/logview.yaml` | Streaming-log pane, `/`-search, filter mode |
| `examples/layout.yaml` | Nested vstack / hstack with mixed flex weights |
| `examples/themes.yaml` | Theme picker reference |
| `examples/chrome.yaml` | `app.glyphs` + `app.borders` — glyph vocabulary and border shapes |
| `examples/marking.yaml` | Multi-select with `markable:` + `mark_key:` |
| `examples/colors.yaml` | Per-component `colors:` overrides |
| `examples/multi.yaml` | Multi-screen drilldown with breadcrumbs |
| `examples/http_countries.yaml` | Live REST API table (restcountries.com) |
| `examples/http_github.yaml` | GitHub API drilldown: users → repos → repo detail |
| `examples/http_github_auth.yaml` | Authenticated GitHub (`${env.GITHUB_TOKEN}`) |
| `examples/http_refresh.yaml` | CoinGecko prices with 10s polling |
| `examples/http_paginated.yaml` | `paginate:` — walk every page up front into one list |
| `examples/http_window.yaml` | `window:` over http — page and filter a remote API from the table; the server answers `author:tolkien` across all 2M Open Library books while the table holds 100 rows |
| `examples/exec_window.yaml` | `window:` over exec — the same loop templated into an argv; `git log --skip/--max-count/--author/--grep` answers the table's paging and filtering |
| `examples/exec_local.yaml` | `exec` source: `git log` as a table |
| `examples/file_fixture.yaml` | `file` source with live re-read |
| `examples/merge_sources.yaml` | `merge` source unioning file + 2 exec children |
| `examples/stream_exec.yaml` | `exec` follow mode → logview |
| `examples/stream_websocket.yaml` | `websocket` source → logview |
| `examples/stream_trades_table.yaml` | `websocket` source → live table (`max_rows: 100` ring buffer of bitstamp BTC/USD trades) |
| `examples/stream_l1.yaml` | L1 ticker JOINED from two Binance.us streams (`bookTicker` for fast bid/ask + `@ticker` for last price + 24h stats), merged by symbol via `row_key: data.s`. Deep-merge composes both sources' fields onto each row |
| `examples/prompts_boot.yaml` | Boot-time form collects params (`app.prompts`) before the main screen renders; values become env vars, feed into the source URL via `${env.USER}`. Includes a `mask: true` field for a token |
| `examples/action_prompts.yaml` | Action `inputs:` the binding doesn't fill are collected in a generated form; `${inputs.<key>}` substitutes into run argv + confirm message at fire time |
| `examples/action_http_argocd.yaml` | `type: http` actions against the Argo CD API — bearer auth, `success:` for a 409-is-fine API, `error_message: ${body.message}` |
| `examples/kube.yaml` | Single-cluster kube: namespaces → pods → pod detail + logs |
| `examples/kube_multi.yaml` | Multi-cluster kube: 3 clusters merged into one table |

The two kube examples are wired to Taskfile tasks — see [Kubernetes
demos](#kubernetes-demos) below.

## Kubernetes demos

Two layers — single cluster (one TUI binding) and multi-cluster (merge
across 3 kind clusters).

### Single cluster

```sh
task kube:up                  # creates `tui-builder` kind cluster + workloads
task kube:proxy               # in another terminal: kubectl proxy --port=8001
task kube:demo                # opens examples/kube.yaml
task kube:down                # cleans up
```

`examples/kube.yaml` drills namespaces → pods → pod detail + tailing
logs. The pods table demonstrates kubectl-style STATUS computation via
a `value:` fallback chain (`waiting.reason → terminated.reason →
status.phase`).

### Multi-cluster (3 kind clusters merged)

Per-terminal — each proxy is foreground:

```sh
task kube:multi:up                 # creates tb-prod / tb-staging / tb-dev
task kube:multi:proxy:prod         # terminal 1
task kube:multi:proxy:staging      # terminal 2
task kube:multi:proxy:dev          # terminal 3

task kube:multi:check              # verify all 3 are reachable
task kube:multi:demo               # opens examples/kube_multi.yaml

task kube:multi:down               # tears down all 3 clusters
```

`task kube:multi:proxy:status` shows port binding state (regardless of
how a proxy got started). `kube:multi:check` additionally pings each
endpoint and counts the pods.

## Project layout

[golang-standards/project-layout](https://github.com/golang-standards/project-layout):

```
cmd/
  tui-builder/        # CLI: tui-builder <config.yaml>      (TUI sink)
  wrangl/             # CLI: wrangl [flags] <config.yaml>    (data sink: JSON / NDJSON)
  example-launcher/   # TUI for browsing every example
internal/
  config/             # YAML schema: cfg.Source bag-of-fields + Type-discriminator dispatch
  datasource/         # ds.Source interface + http / exec / file / websocket / static / merge builders
  pipeline/           # operator builders: filter / project / derive / sort / union / compose / join / cache / passthrough
  expr/               # embedded expression language adapter (expr-lang/expr); used by every operator
  output/             # JSON / NDJSON stdout sink used by wrangl
  build/              # cfg → live tuilib components + binding layer       [TUI side]
  screen/             # screen.Screen impl: focus, push/pop, modals, lifecycle [TUI side]
examples/             # one YAML per feature, plus the kube demos
scripts/
  check-data-layer-boundary.sh   # enforces no-TUI-imports in the data layer
docs/
  components.md       # full schema cheat sheet + per-component reference
AGENTS.md             # rules for AI agents working in this repo
Taskfile.yml          # go-task entry points (`task --list`)
```

The horizontal split — `config / datasource / pipeline / output / cmd/wrangl`
above; `build / screen / cmd/tui-builder` below — is enforced by a CI
check (`scripts/check-data-layer-boundary.sh`). The data layer must
stay buildable, testable, and runnable without dragging in Bubble Tea
or tuilib. If you ever need a TUI helper from the data layer, that's a
sign the boundary is wrong, not that you need an exception.

## Where to go next

- **[`docs/components.md`](docs/components.md)** — complete schema
  reference with every component, every data source field, every color
  knob, the full example index, and the substitution syntax.
- **[`AGENTS.md`](AGENTS.md)** — agent guidance: architecture brief,
  rules to follow, anti-patterns. Read this before generating tui-builder
  code with an LLM.
- **Examples** — `task examples` launches a picker; each file is
  heavily commented and meant to be copy-and-adapt material.

## Development

```sh
task build           # binaries → ./bin
task test            # go vet + go test
task tidy            # go mod tidy
task clean           # rm bin/
```

Tests live under `internal/datasource/source_test.go` (data-source
contract) and `internal/screen/datasource_test.go` +
`internal/screen/multi_e2e_test.go` (end-to-end through the app shell
with stub servers).

## Built on

- [tuilib](https://github.com/jsdrews/tuilib) — the component library
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — the runtime
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) — styling
- [coder/websocket](https://github.com/coder/websocket) — WebSocket client
- [yaml.v3](https://github.com/go-yaml/yaml) — config parsing
- [go-task](https://taskfile.dev/) — task runner
