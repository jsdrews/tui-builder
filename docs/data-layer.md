# The data layer

The data layer (sources and pipelines) is the heart of tui-builder: the
TUI is one consumer of it and `wrangl` is another. This page covers how
it is put together and how `wrangl` drives it. For every field, see the
reference: [sources](reference/sources.md),
[pipelines](reference/pipelines.md) and
[templating](reference/templating.md).

## One map, two kinds of entry

Everything under `data.sources:` is one map. Each entry's `type:` picks
either a **leaf** kind, which fetches from outside the program (`http`,
`exec`, `file`, `websocket`, `static`, `merge`), or an **operator**,
which reads other entries and transforms them (`passthrough`, `filter`,
`project`, `derive`, `sort`, `union`, `compose`, `join`, `cache`).
Components and `wrangl` address either kind by name, so swapping an
`http` source for an `exec` one, or putting a `filter` in front of it,
changes nothing downstream.

The split in the YAML mirrors the code: `data:` is read by both binaries,
`tui:` only by `tui-builder`.

## The model

Every entry, leaf or operator, satisfies one contract:

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

`Fetch` returns the value with `root:` already applied. `Refresh`
reports the polling interval. Streaming sources also implement
`Subscribe`, pushing one `Event` per frame until cancelled. The TUI and
`wrangl` consume sources through these interfaces and nothing else.

## Lifecycle

Two independent properties, both inferred from the config (there is no
`lifecycle:` field), and reported by `wrangl --list` and `--describe`:

| Cadence | When |
|---|---|
| `streamed` | `websocket`, or `exec` / `http` with `follow: true` |
| `polled` | `refresh:` is set |
| `one-shot` | neither |

| Binding | When |
|---|---|
| self-contained | no parameters, or every required one has a default |
| `needs params` | at least one required parameter without a default |

They combine: `polled (refresh: 5s) · needs params`,
`streamed (follow) · needs params`.

## wrangl

`wrangl <config.yaml>` runs the data layer with no TUI: to inspect a
config, dump a source, or pipe one into other tools.

| Invocation | Behaviour |
|---|---|
| `wrangl <config>` | Lists every source and operator (same as `--list`). |
| `wrangl --list [--all] <config>` | Lists entries; `--all` (`-a`) includes hidden `_`-prefixed ones. |
| `wrangl --list-actions <config>` | Lists the actions and their inputs. |
| `wrangl --describe-action <config> <action> [--param k=v]` | Shows an action's inputs and a dry run of the command or request it would send. |
| `wrangl <config> <target>` | Writes the entry's value to stdout. |
| `wrangl <config> <target> --describe` | Prints the entry's kind, lifecycle, request template and parameters, without substituting `${env.*}`. |
| `wrangl <config> <target> --param k=v` | Binds a parameter; repeatable. |
| `wrangl <config> <target> --pretty` | Indents one-shot JSON output. |
| `wrangl <config> <target> --raw` | Writes text values and text frames as plain text, not JSON strings. |
| `wrangl <config> <target> --limit N` | Stops a stream after N events. |
| `wrangl <config> <target> --for D` | Stops a stream after duration D. |

### Output contract

| Source shape | Default stdout | With `--raw` |
|---|---|---|
| `format: json`, one-shot or polled | one JSON value and a newline | unchanged |
| `format: text`, one-shot or polled | one JSON-quoted string | the body as-is |
| streamed JSON frames | NDJSON, one value per line | unchanged |
| streamed text frames | NDJSON of JSON strings | one plain line per frame |

Errors in the middle of a stream are written as `{"error": "..."}` lines,
even with `--raw`, so the line protocol holds. A windowed source returns
its first page.

For log-like sources, prefer streaming (`follow: true`) over a polled
`format: text` source, which re-sends the whole buffer on every tick.

## The boundary

The data layer never depends on the TUI:

- `internal/config`, `internal/datasource`, `internal/pipeline`,
  `internal/expr`, `internal/action`, `internal/output` and `cmd/wrangl`
  must not import `internal/screen`, `internal/build` or any tuilib
  package.
- `scripts/check-data-layer-boundary.sh` walks `go list -deps` in CI and
  fails the build on a violation.

Data wrangling is the product and the TUI is one sink. `wrangl` stays
cheap to build and test without Bubble Tea. See [AGENTS.md](../AGENTS.md)
for the rule.
