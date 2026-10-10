# wrangl

Runs only the data layer of a config (its `data:` block) and writes the
result to stdout as JSON or NDJSON. No terminal UI is loaded or even
linked, so it's quick to build, script and test with.

```sh
wrangl [flags] <config.yaml> [target]
```

With no target it lists the config's entries. With a target (a source or
pipeline name), it writes that entry's value.

## Flags

| Flag | Does |
|---|---|
| `--list` | Lists every source and pipeline with its kind, lifecycle and upstream. Also the default with no target. |
| `-a`, `--all` | With `--list`, includes hidden entries (names starting with `_`, such as `pipe:` intermediates). |
| `--describe` | Prints a target's kind, lifecycle, request template and parameters. `${env.*}` is not substituted, so secrets aren't printed. |
| `--param name=value` | Binds a parameter on the target. Repeatable. |
| `--pretty` | Indents one-shot JSON output. Streams stay one value per line. |
| `--raw` | Writes text values and text stream frames as plain text, not JSON strings. |
| `--limit N` | Stops a stream after N events. |
| `--for D` | Stops a stream after duration D, e.g. `10s`. |
| `--list-actions` | Lists the config's actions with their kinds and inputs. |
| `--describe-action` | Shows one action's inputs and a dry run of the command or request it would send. Bind inputs with `--param`. |

There is no flag to run an action: confirm dialogs, exclusivity and input
forms only exist in the TUI, and a headless run would skip them all.

## Examples

```sh
# What's in this config?
wrangl --list examples/http_countries.yaml

# One entry, pretty-printed, or piped into jq
wrangl --pretty examples/http_countries.yaml all_countries
wrangl examples/http_countries.yaml all_countries | jq '.[0].name.common'

# A parameterized source
wrangl examples/params_demo.yaml posts_by_user --param user_id=3

# A stream, bounded
wrangl --limit 5 examples/stream_l1.yaml l1
wrangl --for 10s --raw examples/stream_exec.yaml ticker

# Actions: what exists, and what one would run
wrangl --list-actions examples/kube.yaml
wrangl --describe-action examples/kube.yaml delete_pod --param name=nginx --param namespace=default
```

## Output

| Source shape | Default stdout | With `--raw` |
|---|---|---|
| `format: json`, one-shot or polled | one JSON value and a newline | unchanged |
| `format: text`, one-shot or polled | one JSON-quoted string | the body as-is |
| streamed JSON frames | NDJSON, one value per line | unchanged |
| streamed text frames | NDJSON of JSON strings | one plain line per frame |

A polled source is fetched once. Errors in the middle of a stream are
written as `{"error": "..."}` lines, even with `--raw`, so the line
protocol holds. A windowed source returns its first page.

For log-like sources, prefer a streaming source (`follow: true`) with
`--raw` over a polled `format: text` source, which returns the whole
buffer each time.

## Parameters

`--param` binds to the target's own `parameters:`. When the target is a
pipeline operator that declares none, the values pass through to the
source underneath. A required parameter that isn't bound is an error.

## Environment

`app.env` is checked the same way as in `tui-builder`; a missing required
variable exits non-zero, which CI can catch. `app.prompts` isn't shown,
so set those variables in the environment.

## Exit status

`0` on success; non-zero, with the error on stderr, when the config fails
to load, a target doesn't exist, or a fetch fails.
