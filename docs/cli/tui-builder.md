# tui-builder

Renders a config as an interactive terminal UI.

```sh
tui-builder <config.yaml>
tui-builder <command> [args]
```

The run form takes one argument, the config file. Its only flags are
`--version` (or `-v`), which prints the version and exits, and `--help`
(or `-h`).

A [subcommand](#subcommands)'s name wins over a config file of the same
name: open a file called `init` as `./init` (or name it `init.yaml`). A
name close to a command's that isn't a file, such as `versoin`, fails
with a hint naming the command.

## What happens at startup

1. The config is loaded and validated. Any schema error (an unknown
   field value, a missing required field, a bad reference) stops here
   with a message naming the field.
2. `app.env` is checked: a missing required variable stops the load,
   defaults are applied, and undeclared-but-referenced variables print a
   warning.
3. If `app.prompts` is set, its form is shown. Each answer is set as an
   environment variable; cancelling exits.
4. `${env.*}` is substituted across the config.
5. The first screen (`tui.screen`, or `tui.initial` from `tui.screens`)
   is built and its sources start fetching.

## Environment

- Any variable the config reads as `${env.NAME}`. A boot prompt whose
  variable is already set starts pre-filled, so
  `GH_USER=octocat tui-builder repos.yaml` lets you skip straight
  through the form.

## Keys and mouse

The shell's keys are listed in [Guide → App](../guide/app.md#the-app-shell);
`?` shows every key the focused component accepts.

The mouse focuses panes and moves cursors, the wheel scrolls, a double
click is `enter`, and a right click opens the action menu on the row
under the pointer. Hold `shift` (or `alt` in iTerm2) while dragging to
select text for copying.

## Subcommands

Each has its own `--help`.

### `tui-builder init`

```sh
tui-builder init [-o FILE] [--title TITLE] [--dry-run] [--force]
```

Writes a small commented config that runs straight away, offline: a
`static` source, a filterable table bound to it, and one screen. An http
source and an exec source sit commented out under `data.sources` as next
steps, and each section links its reference page. It asks nothing; the
flags are the only choices. The file it writes is shown in the
[quick start](../quickstart.md#a-first-config).

| Flag | Default | What it does |
|---|---|---|
| `-o`, `--output FILE` | `tui-builder.yaml` | The file to write. Its directory must exist. |
| `--title TITLE` | the name of the directory the file goes into | `app.title`. |
| `--dry-run` | off | Prints the config to stdout and writes nothing. |
| `--force` | off | Overwrites an existing file. Without it, an existing file is an error that names it and suggests `--force` or `-o`. |

After writing, it prints the command that runs the file:
`run: tui-builder tui-builder.yaml`.

### `tui-builder add`

Writes a source, and a component that shows it, for you. Its one kind so
far is [`exec`](#tui-builder-add-exec).

### `tui-builder add exec`

```sh
tui-builder add exec [flags] -- <command> [args…]
```

Runs the command once, reads the JSON it prints, and prints a complete
config to stdout (or, with `--into`, adds to one you have): an exec source for the command, and a table that
shows it (an `auto: true` inspector when the output is a single object),
on one screen. Save it and run it:

```sh
tui-builder add exec -- kubectl get pods -o json > pods.yaml
tui-builder pods.yaml
```

- **The command** goes after `--`, exactly as the source will run it:
  no shell, so pipes and redirects need `sh -c "…"`. It runs with your
  environment and working directory, as at runtime; no environment
  variable is copied into the config. It prints `running: <command>` on
  stderr first, and `Ctrl-C` cancels it.
- **The rows:** a top-level list; or an object holding exactly one list
  of objects (a Kubernetes `List`'s `items`), which becomes `root:`; or
  one JSON value per line (NDJSON), of which the first 50 are sampled. An
  object with several lists of objects stops and asks for `--root`.
- **The columns** come from every field seen across the rows. Nested
  objects flatten two levels (`metadata.name`), a list of plain values
  is one column shown as `a, b`, and lists of objects and fields null in
  every row are left out. Titles are the field name in words
  (`creationTimestamp` → "Creation timestamp"); when two clash, the
  parent's name goes first ("Spec name"). Every column sorts: `sort:
  number` for numbers, `sort: si` for sizes like `12Ki` or `187MB`, text
  otherwise.
- **Which come first:** columns are ranked, keeping the order they first
  appear within a rank:
  1. the identity: `name`, `metadata.name` or `title`, else the mark
     key's field (below), else `id`;
  2. states: a name like `status`, `state`, `phase`, `health`, `level`,
     `result` or `ready`, a boolean, or text whose values repeat, with 2
     to 8 of them;
  3. timestamps, by name (`created_at`, `createdAt`, `ts`…) or because
     every value is one;
  4. numbers and sizes;
  5. other short text;
  6. long text, averaging over 40 characters;
  7. fields empty in every row.

  The first 8 are written (`--max-columns` changes that), with one slot
  kept for the first long-text column: it takes the last, in place of
  the last short one. The rest follow as commented-out lines, so
  uncommenting one adds it.
- **Style,** unless `--no-style`:
  - the identity is bold;
  - states get colour rules for the words the sample shows, from a
    built-in list (`Running`, `Ready`, `Succeeded` → `theme:success`;
    `Pending`, `Waiting`, `Degraded` → `theme:warning`; `Failed`,
    `Error`, `CrashLoopBackOff` → `theme:error`…), then catch-alls for
    other failure and warning words. A column with none of the words
    gets no rules, so unknown values stay plain;
  - a boolean is coloured (true good, false bad) only when its name
    reads as a check (`ready`, `healthy`, `ok`, `enabled`);
  - a count named like failures (`restarts`, `errors`, `failures`)
    warns above zero;
  - numbers and timestamps are right-aligned;
  - the long-text column (else the identity) gets `flex: 1`, and states
    `max_width: 20`.
- **Marking:** the first of `id`, `uid`, `metadata.uid`, `name` and
  `metadata.name` (any case) that's set and unique in every row becomes
  `mark_key:`, with `markable: true`.
- **The name** of the source and component is the last word before the
  first flag that isn't a verb like `get` or `list` (`kubectl get pods`
  → `pods`, `docker ps` → `ps`), or a file argument's base name.
- **Streams.** A command still running 3 seconds after it started,
  having printed something, is a stream (`--follow` says so up front;
  `--no-follow` says to wait for it to finish, up to `--timeout`). Up to
  3 seconds or 20 lines are sampled, then it's stopped. The source gets
  `follow: true`, and:
  - one JSON object per line (`kubectl get pods -w -o json | jq -c .`)
    makes a **streaming table**, its columns found as above. When every
    event carries one of `id`, `uid`, `metadata.uid`, `name` or
    `metadata.name`, it's the table's `row_key`, so each event updates
    its row (a value seen twice in the sample is an update); otherwise
    the table keeps the newest 100 events, newest first;
  - anything else (`tail -f`, `kubectl logs -f`) is `format: text`,
    shown in a **logview**.

  A stream reads one event per line, so a command that prints each JSON
  value over several lines gets a warning to ask for compact output.
  `--refresh` and `--root` don't apply to a stream.
- **Windows,** for a command that can return one page of a larger set.
  Run it with real values and name them: `--window offset=0 --window
  limit=50` swaps the argument equal to each value for
  `${window.offset}` and `${window.limit}` (also inside an argument,
  as with `--param`). The tokens are `offset`, `limit`, `search`,
  `sort`, `sort_dir`, `cursor`, `dir` and `filter.NAME` (which becomes
  `${window.filters.NAME}`). Writing `${window.…}` into the command
  yourself works too; it's sampled as the first page (offset 0, limit
  50, nothing searched or sorted, walking older), and an argument whose
  tokens are all empty is dropped, as at runtime.
  - `offset` and `limit` make a **seekable** window. It needs a total:
    a number beside the rows named `total`, `count`, `totalCount`,
    `numFound` (or similar), or `--total-path`.
  - `cursor`, `dir` and `limit` make an **anchored** window, opening at
    the newest item. Each item's cursor is the first of `ts`, `time`,
    `timestamp`, `@timestamp` or `id` in every row, or `--cursor-path`.
    The summary points at jumping to a time as a next step.

  An anchored window, or rows that read as events (a timestamp plus a
  `msg`, `message`, `text`, `line`, `log`, `stdout` or `output`), are
  shown by an **eventlog**: keyed by the mark-key rule (or, anchored, by
  the cursor when it's unique), drawing the message-like field (else the
  longest text), with the timestamp in the gutter. When the text starts
  with its level, `ERROR`/`FATAL` lines are `theme:error` and `WARN`
  lines `theme:warning`. Otherwise it's a **table**: filterable only
  when `search` or a `filter.*` was marked, with each `filter.NAME`
  mapped to the column showing `NAME`; sortable only when both `sort`
  and `sort_dir` were (the command gets the column's title in
  `${window.sort}`); never markable when seekable. Over an anchored
  window it's a span table, which needs a unique key.
  `--growing` makes the eventlog follow a set that's still gaining
  items. A window can't be combined with `--refresh`, `--follow` or
  `--no-run`.
- **Adding to a config you have** (`--into app.yaml`): the source and
  component go into it instead of printing a new config. Where the
  component goes depends on the config's shape:
  - one screen: into its layout. A lone `component:` becomes a `vstack`
    of the old and the new; a `vstack` gets one more item;
  - a screen with tabs: a new tab, labelled with the component's title;
  - several screens: a new screen named like the source, plus an item in
    the initial screen's menu. With no menu there, it goes onto the
    initial screen's layout instead, with a note saying so.

  `--place stack|tab|screen` picks instead; a one-screen config is never
  turned into several. Only the new lines are written: comments, blank
  lines, quoting and the file's indentation are left as they are (a lone
  `component:` line is the one line replaced). Anything the edit can't
  make safely is refused, naming the line: an inline (`{…}` or `[…]`)
  parent, an alias or `<<:` merge, a parent ending in a `|+` value, an
  `hstack` or `zstack` layout, more than one YAML document, tab
  indentation or CRLF endings. So is a name that's taken: "source `pods`
  already exists — pass `--name pods2`". Before writing, the result is
  checked to be the old config plus exactly the new entries, and loaded;
  if either fails nothing is written. `--dry-run` prints the change as a
  unified diff instead; otherwise the file is replaced in one step
  (keeping its permissions) and a line says what went where.
- **Flags win; inference fills the rest.** After the config, stderr
  lists each choice and where it came from (`name: pods (inferred)`,
  `component: list (--component)`), with a warning for any flag the
  sample doesn't bear out.
- **Parameters** (`--param ns=default`): run the command with real
  values, then name them. The argument equal to the value (or the one
  argument containing it once, as in `--namespace=default`) becomes
  `${params.ns}`, and the source declares `ns` with the value as its
  `default`, so it still runs as it did. A value that's missing, or
  appears more than once, is an error. Each parameter gets an input
  field (a toggle for a `bool`) above the component, which reads it
  through `on_input`; `--no-inputs` leaves them out, for a source a push
  will fill instead.
- **Failures print nothing to stdout:** a non-zero exit (with the last
  lines of the command's stderr), no output, output that isn't JSON, or
  a timeout. Output past 50 MB is cut there; the values before the cut
  are used.

| Flag | Default | What it does |
|---|---|---|
| `--timeout D` | `10s`, the exec source's default | How long the sample run may take. When given, it's also written to the source as `timeout:`. |
| `--root PATH` | inferred | The dot-path to the list of rows. A path that isn't a list in the output is an error. |
| `--max-columns N` | `8` | How many columns to write; the rest are commented out. At least 1. |
| `--no-style` | off | Writes no bold, colour rules, alignment or widths. The columns are still ranked. |
| `--param NAME=VALUE[:TYPE]` | — | Makes the argument equal to `VALUE` the parameter `NAME`, with an input field. `TYPE` is `string` (the default), `int`, `bool` or `duration`. Repeatable. |
| `--no-inputs` | off | Writes the `--param` parameters without input fields. |
| `--columns PATH[=Title],…` | ranked | The columns, in this order, in place of ranking. Style still applies. A path no sampled row has is written with a warning. |
| `--component KIND` | `table`, or `inspector` for a single object | `table`, `list` (showing the identity field, or the first `--columns` path), `inspector` (`auto: true`), `eventlog` or `logview`. A kind the output can't fill is an error: an eventlog needs a window, a logview text or a stream, and a window is shown by a table or an eventlog. |
| `--name NAME` | from the command | The source's and component's name. |
| `--title TITLE` | the name, title-cased | The component's title, and `app.title`. |
| `--refresh D` | fetched once | Polls the command on this interval (`refresh:`). |
| `--follow` | detected | The command streams: sample it without waiting to see, and write `follow: true`. With `--no-run`, writes a stream source and a logview (or a table of the `--columns` given). |
| `--no-follow` | detected | The command finishes, however long it runs: wait for it, up to `--timeout`. |
| `--window TOKEN=VALUE` | — | Makes the argument equal to `VALUE` the window token `TOKEN`: `offset`, `limit`, `search`, `sort`, `sort_dir`, `cursor`, `dir` or `filter.NAME`. Repeatable. |
| `--total-path PATH` | inferred | Where a seekable window's total is, in the command's output. |
| `--cursor-path PATH` | inferred | Each item's cursor, for an anchored window. |
| `--growing` | off | The eventlog's set is still gaining items: it follows the newest (`growing: true`). Only on an eventlog. |
| `--follow-every D` | `2s` | How often a growing window is polled (`follow_every:`). Needs `--growing`. |
| `--into FILE` | — | Adds the source and component to this config, instead of printing a new one. |
| `--place WHERE` | by the config's shape | With `--into`: `stack` (the screen's layout), `tab` (a new tab) or `screen` (a new screen, linked from the initial screen's menu if it has one). |
| `--dry-run` | off | With `--into`: prints the change as a unified diff and writes nothing. |
| `--no-run` | off | Doesn't run the command, for one that's slow or changes something. With no sample to take columns from, the source is shown by an `auto: true` inspector, or by a table or list of the `--columns` given. |

### `tui-builder version`

```sh
tui-builder version
```

Prints `tui-builder version <version>`, the same line as `--version`.
It takes no arguments or flags.

## Exit status

The same for the run form and every subcommand:

- `0`: success, or a normal quit (`q`).
- `1`: an error, printed on stderr: the config fails to load, the
  program fails to start, or a command fails.
- `2`: a usage error, printed on stderr with a pointer to `--help`: an
  unknown flag, a missing config file, or too many arguments.

## Related

- [`wrangl`](wrangl.md) runs the same config's data layer without a UI.
- `example-launcher` (or `task examples` in a clone) lists every example
  and runs the one you pick. `-dir PATH` points it at another directory
  of `*.yaml` configs; the default is `./examples`, or `../examples`.
