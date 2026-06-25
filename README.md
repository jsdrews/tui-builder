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

A `pipelines:` block in the YAML declares named, addressable pipelines
over your sources. Today they're passthroughs (a stable name you can
bind components to and ask `wrangl` to dump). Operators (`filter`,
`project`, `union`, `join`) layer on top in later phases.

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
| **Declarative TUIs** | One YAML file per app: `components:` + `screen:`/`screens:` + (optional) `data_sources:`. No Go to write for the common case. |
| **Components** | `list`, `table`, `inspector`, `tree`, `logview` — every tuilib component except those that don't fit a config model |
| **Data sources** | `http`, `exec`, `file`, `websocket`, and `merge` (compose any of the above) |
| **Multi-screen** | Push/pop with breadcrumbs; `${selection.*}` substitutes parent row into child config (URL, title, fields) |
| **Streaming** | Long-running `exec` + `websocket` push events into a logview as they arrive |
| **Live data** | Per-source `refresh: <duration>` polling with in-place updates — cursor / filter / sort survive every refresh |
| **Color** | Theme-wide palettes (Nord, Dracula, …) + per-component `colors:` overrides + per-value `color_rules:` ("Running" → green, "ERROR" → red) |
| **Templating** | `${selection.col}` (parent row), `${env.VAR}` (env var) substitute anywhere a string lives — titles, URLs, headers, action argv |
| **Actions** | Bind a key to a subprocess (`kubectl exec`, `$EDITOR`, `open`); optional `confirm:` modal; interactive (TTY handoff) or non-interactive (no flicker) |
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

Once you've written a YAML config with `data_sources:` + `pipelines:`,
you can either render it as a TUI or just dump the data:

```sh
# Render the TUI:
bin/tui-builder examples/http_countries.yaml

# Or dump every defined source / pipeline:
bin/wrangl --list examples/http_countries.yaml
# NAME            KIND                     LIFECYCLE              UPSTREAM
# all_countries   pipeline / passthrough   polled (refresh: 5m)   countries
# countries       source / http            polled (refresh: 5m)

# Or pipe one pipeline's output downstream:
bin/wrangl examples/http_countries.yaml all_countries | jq '.[0]'
bin/wrangl --limit 50 examples/stream_l1.yaml l1 | jq '.data.s'
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
column, `q` quits, `t` cycles themes.

## Tour by feature

### Data sources

Bind a component to a source by name. The same dot-path resolver works
across all source types; the same `color_rules` syntax works against
any pluckable value.

```yaml
data_sources:
  countries:
    type: http
    url: https://restcountries.com/v3.1/all?fields=name,region,population
    refresh: 5m

components:
  countries:
    type: table
    source: countries
    columns:
      - {title: Name,       value: name.common}
      - {title: Region,     value: region}
      - {title: Population, value: population, sort: si, align: right}
```

Source kinds:

| Type | When | Notes |
|---|---|---|
| `http` | REST APIs, JSON endpoints, kube proxies | `url`, `method`, `headers`, `body`, `format: json\|text` |
| `exec` | Anything that prints JSON: `kubectl get -o json`, `gh api`, `terraform output -json`, custom scripts | Per-source `env:` adds to inherited env |
| `file` | Fixtures, generated dumps, lab notebook output | `refresh: <duration>` re-reads; omit for once |
| `websocket` | Live event streams: chat, custom buses | Headers pass through on the upgrade request |
| `merge` | Compose N children, union the rows, tag each item by source — cross-cluster / cross-account / cross-anything | `tag_field:` writes child name into each item; `on_error: skip` returns survivors + a partial-error notice |

See [`docs/components.md`](docs/components.md) for the full schema.

### Streaming

Long-running subprocess (`exec` + `follow: true`) or WebSocket
connection. Events arrive over time; the bound component receives them
without resetting cursor / filter / scroll.

**Into a logview** — lines append as-is:

```yaml
data_sources:
  logs:
    type: exec
    follow: true
    command: [kubectl, logs, -f, "-n", default, my-pod]

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
screens:
  pods:
    layout: {component: pods_table}
    on_enter:
      - {source: pods_table, push: detail}
  detail:
    title: ${selection.Name}
    layout: {component: pod_inspector}
initial: pods

data_sources:
  pod_detail:
    type: http
    url: http://localhost:8001/api/v1/namespaces/${selection.Namespace}/pods/${selection.Name}
```

`esc` pops; breadcrumbs accumulate at the top of the screen.

### Actions

Bind a key to a subprocess. Confirms can be required; errors land in a
modal with the captured stderr.

```yaml
screens:
  pods:
    layout: {component: pods_table}
    actions:
      - key: x
        label: exec
        source: pods_table
        confirm: "Open shell in ${selection.Name}?"
        run: [kubectl, exec, -it, -n, "${selection.Namespace}", "${selection.Name}", --, sh]
      - key: D
        label: delete
        source: pods_table
        confirm: "Delete pod ${selection.Name}? Cannot be undone."
        interactive: false
        run: [kubectl, delete, -n, "${selection.Namespace}", pod, "${selection.Name}"]
```

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
| `examples/colors.yaml` | Per-component `colors:` overrides |
| `examples/multi.yaml` | Multi-screen drilldown with breadcrumbs |
| `examples/http_countries.yaml` | Live REST API table (restcountries.com) |
| `examples/http_github.yaml` | GitHub API drilldown: users → repos → repo detail |
| `examples/http_github_auth.yaml` | Authenticated GitHub (`${env.GITHUB_TOKEN}`) |
| `examples/http_refresh.yaml` | CoinGecko prices with 10s polling |
| `examples/exec_local.yaml` | `exec` source: `git log` as a table |
| `examples/file_fixture.yaml` | `file` source with live re-read |
| `examples/merge_sources.yaml` | `merge` source unioning file + 2 exec children |
| `examples/stream_exec.yaml` | `exec` follow mode → logview |
| `examples/stream_websocket.yaml` | `websocket` source → logview |
| `examples/stream_trades_table.yaml` | `websocket` source → live table (`max_rows: 100` ring buffer of bitstamp BTC/USD trades) |
| `examples/stream_l1.yaml` | L1 ticker JOINED from two Binance.us streams (`bookTicker` for fast bid/ask + `@ticker` for last price + 24h stats), merged by symbol via `row_key: data.s`. Deep-merge composes both sources' fields onto each row |
| `examples/prompts_boot.yaml` | Boot-time form modal collects params (`app.prompts`) before the main screen renders; values become env vars, feed into the source URL via `${env.USER}` |
| `examples/action_prompts.yaml` | Per-action form modal collects input (`action.prompts`); `${prompt.<key>}` substitutes into run argv + confirm message at fire time |
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
  config/             # YAML schema (one Go struct per shape) + validator + DAG check
  datasource/         # Source interface + http / exec / file / websocket / merge
  pipeline/           # named pipelines over sources (passthrough today; operators TBD)
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
