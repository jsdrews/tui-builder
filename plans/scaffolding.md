# Scaffolding: `init` and `add exec`: the locked spec

Two `tui-builder` subcommands that write config for you:

- `tui-builder init` writes a commented starter config that runs
  straight away, offline.
- `tui-builder add exec -- <command…>` runs a command once, samples its
  JSON, and writes an exec source (plain, windowed or streaming) plus
  the component that shows it. The result is printed as a complete
  config, or added to an existing one.

It's locked: every decision was made on the map
[Map: Scaffolding — init and exec sources from a command](https://github.com/jsdrews/tui-builder/issues/127).
Each section links the ticket that holds its detail and reasoning; where
a later ticket refined an earlier one, this plan states the result.
Eight PRs, in the [order at the bottom](#implementation-order).

Terms follow [`CONTEXT.md`](../CONTEXT.md) (Source, Parameter,
Component, Input field, Window, Seekable, Anchored, Cursor, Stream). No
new terms.

## 1. Subcommands

From [Subcommands for tui-builder](https://github.com/jsdrews/tui-builder/issues/129).

- `tui-builder` moves to Cobra (already a dependency through wrangl).
- **The run form is unchanged.** The root command takes one optional
  positional config path, so `tui-builder config.yaml` works as before.
- **Commands:** `init`, `add exec -- <command…>` (`add` is a parent,
  leaving room for `add http`), `version`. `gen` is reserved for the
  OpenAPI generator, which
  [its map](https://github.com/jsdrews/tui-builder/issues/113) made
  `tui-builder gen <spec>`.
  `--version` / `-v` keep working.
- **A subcommand wins over a file of the same name.** Open such a file as
  `./init` or `init.yaml`; the usage text says so.
- **Flags live on the commands that write.** The root has only
  `--version` and `--help`.
- **Exit codes:** 0 success; 1 error (load failure, failed command);
  2 usage error (unknown flag, missing arguments).
- **Help:** `tui-builder --help` shows the run form first, then the
  subcommands. `docs/cli/tui-builder.md` gets a section per subcommand,
  and a test like wrangl's `TestDocsListEveryFlag` keeps it complete.

## 2. `tui-builder init`

From [What tui-builder init writes](https://github.com/jsdrews/tui-builder/issues/130).

- **File:** `tui-builder.yaml` in the current directory, or
  `-o, --output <path>`. `--dry-run` prints to stdout instead.
- **Contents**, the smallest config showing both layers:
  - `app.title` (`--title`, default the directory's name);
  - one `static` source, `items`, with three or four rows;
  - a filterable table bound to it with `value:` paths, one column
    sortable;
  - one screen.

  A bound source rather than inline `rows:`, so `wrangl tui-builder.yaml
  items` works on it and switching to http is a one-block edit. Each
  section has a one-line comment linking its reference page; a
  commented-out http source and exec source sit under `data.sources` as
  next steps.
- **An existing file** is refused with an error naming it and suggesting
  `--force` or `-o`. `--force` overwrites.
- **Flags only, no prompts, one starter.** After writing it prints
  `run: tui-builder tui-builder.yaml`.
- **Docs and tests:** the quickstart's "A first config" opens with
  `tui-builder init` and shows the file; `docs/cli/tui-builder.md` gets
  an `init` section; a test loads the starter with `config.Load` and
  compares it to a golden copy.

## 3. Running and sampling a command

From [Running and sampling a command](https://github.com/jsdrews/tui-builder/issues/131).

- **No prompt.** It prints `running: <command>` to stderr first.
  `--no-run` skips sampling (for slow commands or ones with side
  effects) and writes the source with an `auto: true` inspector: a table
  can't load without columns, and the inspector needs no shape.
- **Argv, no shell**, exactly as given after `--`, which is how the
  source stores it. Pipes and redirects need an explicit `sh -c "…"`.
- **Timeout:** the exec default, 10s, raised with `--timeout`; Ctrl-C
  cancels. The written source carries `timeout:` only when one was
  passed.
- **Failures stop with an error and write nothing:**
  - a non-zero exit: the code and the last lines of stderr;
  - no output: "the command printed nothing to sample";
  - not JSON: the first bytes, with a hint at `-o json` or
    `format: text`.

  Output past `max_body` (50MB) is cut there and the first part that
  parses is used.
- **NDJSON:** one JSON value per line; the first 50 lines are the sample
  rows.
- **Environment and working directory** are inherited, as at runtime.
  Env vars are never copied into the config.

## 4. Inferring a source and component

From [Inferring a source and component from a JSON sample](https://github.com/jsdrews/tui-builder/issues/132)
and [Column importance and style](https://github.com/jsdrews/tui-builder/issues/138),
which replaced the first one's column order. The guaranteed long-text
slot came later, from the generator's
[Which response properties become columns](https://github.com/jsdrews/tui-builder/issues/120),
so both follow one ranking.

**Rows (`root:`)**
- A top-level array: no `root:`.
- An object with exactly one array-of-objects field (any name; a
  Kubernetes `List` is this case): `root:` is that field.
- Several such arrays: stop and ask for `--root`, listing them.
- A single object with none: an `inspector` with `auto: true`, no cap.
- NDJSON lines are already rows.

**Columns**, from the union of all sampled rows:
- Scalars become columns; nested objects flatten to dot-paths two levels
  deep (`metadata.name`); arrays of scalars become one comma-joined
  column; arrays of objects, and fields null in every row, are skipped.
- **Ranked:**
  1. identity: `name`, `metadata.name` or `title`, else the `mark_key`
     field, else `id`. (Changed in PR 4 from "the `mark_key` field
     first", which made a Kubernetes table lead with its Uid.)
  2. status: names like `status`, `state`, `phase`, `health`, `level`,
     `severity`, `result`, `ready`, or a string with ≤ 8 distinct values
     in the sample; booleans rank here;
  3. timestamps: timestamp-like names or ISO-looking values;
  4. numbers;
  5. other short strings;
  6. long text (average over 40 characters).

  Fields empty in every row go last.
- **The top 8 are kept**, in rank order (`--max-columns N` changes the
  8), with **one slot guaranteed to the top long-text column** when
  there is one: it takes the last slot, displacing the last short
  string. The rest are written as **commented-out column lines**
  (`# - {title: Restart count, value: status.restartCount}`), so
  uncommenting adds one.
- **Titles** come from the last path segment in words
  (`creationTimestamp` → "Creation timestamp"); clashes are prefixed by
  their parent ("Spec name").
- **Sorting:** numbers get `sort: number`, values like `12Ki` / `3.5M` in
  every row get `si`, the rest the string default. Every column on a
  plain source is `sortable: true` (it sorts locally); windowed tables
  differ (section 7).
- **`mark_key`:** the first of `id`, `uid`, `metadata.uid`, `name`,
  `metadata.name` present in every sampled row and unique across them;
  none otherwise.
- **Names:** source and component are named from the program plus its
  first non-flag argument (`kubectl get pods` → `pods`).
- **No inspector of the focused row** is added: one can't follow a
  table's focused item without a parameterised source of its own (see
  [Follow-ups](#follow-ups)).

**Style**
- **Bold** on the identity column only.
- **Status colours:** a built-in vocabulary matched case-insensitively
  against the distinct values in the sample. Rules are written only for
  words seen, plus `~regex` catch-alls for the bad and warning families.
  No default rule: unknown values render plain.
  - `theme:success`: Running, Ready, Succeeded, Healthy, Active, OK,
    true, Pass, Complete…
  - `theme:warning`: Pending, Waiting, Progressing, Degraded, Unknown,
    Warn…
  - `theme:error`: Failed, Error, CrashLoopBackOff, Unhealthy, False,
    Fatal…

  The `theme:` tokens follow the palette across `t` and every surface
  (already built; see [What's already in place](#whats-already-in-place)).
- **Booleans** are coloured (true good, false bad) only when the column
  name reads as a check (`ready`, `healthy`, `ok`, `enabled`).
- **Empty and zero** values aren't coloured, except counts named like
  failures (`restarts`, `errors`, `failures`), which get
  `{when: ">0", color: theme:warning}`.
- **Timestamps** are shown as-is, right-aligned.
- **Widths:** `flex: 1` on the long-text column, else on identity;
  `max_width: 20` on status columns; `align: right` on numbers; the rest
  auto.

## 5. Flags that shape what's written

From [Flags that shape what add exec writes](https://github.com/jsdrews/tui-builder/issues/133).

- **A flag always wins; inference fills the rest.** A flag the sample
  contradicts (`--root items` with no `items` array, `--component
  eventlog` with no text-like field) is an error naming both. The summary
  after writing marks each choice as inferred or from a flag.
- **`--param NAME=VALUE[:TYPE]`**, repeatable; TYPE is `string`
  (default), `int`, `bool` or `duration`. The argv element equal to VALUE
  becomes `${params.NAME}`, also inside an element (`--namespace=default`)
  when unambiguous; a missing or ambiguous match is an error. It writes
  the parameter with `default: VALUE`, so the source stays runnable.
- **Each `--param` adds an input field** (a text input; a toggle for
  bool) titled from the name, starting at VALUE. The fields share one
  row above the component, which gets
  `on_input: {bind: {NAME: "${input.NAME}"}}`. `--no-inputs` writes the
  parameter only, for a source reached by a push.
- **`--columns path[=Title],…`** fixes the set and order and skips
  ranking; styling still applies. An untitled path is titled from its
  last segment. A path missing from every sample row warns, not fails.
- **`--component table|list|inspector|eventlog|logview`**, checked
  against the source's shape: `eventlog` only on a windowed source,
  `logview` only on text or a stream; `inspector` gets `auto: true`;
  `list` uses the identity field as its `item:`. No `tree`.
- **`--name`** names both source and component (clash checks use it).
- **`--title`** sets the component's title, and `app.title`; default
  the name, title-cased.
- **`--no-run` with `--columns`** writes a table (or, with
  `--component list`, a list) of those columns; without `--columns`
  it's the `auto: true` inspector.
- **`--refresh 30s`** writes `refresh:`; without it the source is
  one-shot. With `--follow` or `--window` it's an error.
- **`--no-style`** writes no bold, colour rules or widths.

## 6. Streaming commands

From [Streaming commands as stream sources](https://github.com/jsdrews/tui-builder/issues/136).

- **Detected or asked for:** a command still running after the sample
  window is a stream, with the note "still running after 3s — treating
  it as a stream (`follow: true`)". `--follow` says so up front (no
  wait); `--no-follow` says it should finish (then it fails at
  `--timeout`).
- **Sampling:** up to 3s or 20 lines, whichever comes first, then stop
  the command. One JSON object per line is a JSON stream; anything else
  is text.
- **JSON event streams** (`kubectl get pods -w -o json`): `follow: true`
  and a **streaming table**, columns inferred and styled as in section 4.
  Events carrying an id-like key (the `mark_key` candidates, present in
  every event, but not required to be unique: a repeat is an update) get
  `row_key:`, so each event updates its row; otherwise a ring buffer,
  `max_rows: 100`, newest first.
- **Detection, as built:** still running after 3s *and* having printed
  something. A command that's quiet that long may be a slow one that
  prints at the end, so it's waited for until it prints (a stream) or
  exits (not one), up to `--timeout`. The sampled command's process group
  is killed when sampling stops, so `sh -c "… | jq -c ."` stops too.
- **Text streams, following logs** (`tail -f`, `kubectl logs -f`):
  `format: text` and a **logview**, `follow: true`.
- **Pretty-printed JSON streams:** the sampler reads concatenated JSON
  values, not just one per line. The runtime's exec stream splits on
  lines (checked in PR 6), so such a stream gets a warning pointing at
  compact output (`jq -c`, the tool's own flags).
- **`--no-run --follow`** writes the stream source and a logview, the one
  component that needs no sampled shape.

## 7. Windowed commands, tables and eventlogs

From [Windowed exec sources, tables and eventlogs](https://github.com/jsdrews/tui-builder/issues/137).

- **Marking the window**, like `--param`: sample with real values, then
  name them. `--window TOKEN=VALUE` swaps the argv element equal to VALUE
  for `${window.TOKEN}`. Tokens: `offset`, `limit`, `search`, `sort`,
  `sort_dir`, `cursor`, `dir`, and `filter.NAME=VALUE` →
  `${window.filters.NAME}`. Missing or ambiguous is an error naming it.
  Writing `${window.…}` straight into the command also works; it's
  sampled with defaults (offset 0, limit 50, empty search, no cursor).
- **Seekable or anchored, by the tokens:** `offset` + `limit` is
  seekable; `cursor` + `dir` is anchored; mixing the sets is an error.
  - Seekable needs `total_path`, inferred from an envelope field like
    `total`, `count`, `totalCount`, `numFound` beside the `root:` array,
    or `--total-path`. None found: stop with "a seekable window needs a
    total — add one to the command's output, or use a plain source".
  - Anchored needs `cursor:`, inferred from a timestamp-like or id-like
    field (`ts`, `time`, `timestamp`, `@timestamp`, `id`), or
    `--cursor-path`. It opens at the newest; jump-to-time inputs aren't
    written, and the summary mentions them as a next step.
- **Table or eventlog:** an eventlog by default for anchored windows and
  for samples that read as events (a timestamp plus a message-like text
  field); a table otherwise. `--component` overrides.
  - `key`: the `mark_key` rule; for anchored, cursor + key must be unique.
  - `text`: the longest string field, preferring `msg`, `message`,
    `text`, `line`, `log`.
  - `mark`: the timestamp field, if any.
  - Colour rules match the item's text: when `text` carries the level,
    `~^(ERROR|FATAL)` → `theme:error` and `~^WARN` → `theme:warning`. A
    separate level field gets nothing (see [Follow-ups](#follow-ups)).
- **`--growing`** writes `growing: true`, plus `follow_every` only when
  given. A sample can't show growth, so it's opt-in. Without an eventlog
  it's an error.
- **A windowed table offers only what the command answers:**
  `filterable: true` only if `search` or a `filter.*` token was marked;
  columns sortable only if `sort` and `sort_dir` were marked.
- **As built (PR 7):** an anchored window also needs `${window.limit}`,
  as every exec window does. An eventlog's text also prefers `stdout` and
  `output` (an AWX job's events), and over an anchored window with no
  unique id it's keyed by the cursor when that's unique. Each
  `filter.NAME` maps to the column showing `NAME`; with none, it's an
  error pointing at `--columns`. `--no-run` with a window is refused,
  since the total, cursor and key come from a sample. Numbers in the
  command (a page size) never name the source.

## 8. Writing the result

From [Adding to an existing config](https://github.com/jsdrews/tui-builder/issues/134)
and [Editing YAML without losing comments](https://github.com/jsdrews/tui-builder/issues/128).

- **Without `--into`:** print a **complete, runnable config** (`app`,
  `data`, `tui`, one screen), so
  `tui-builder add exec -- … > pods.yaml && tui-builder pods.yaml`
  works. No fragments.
- **With `--into <config>`, placed by the config's shape:**
  - a single screen with no tabs: into its layout. A lone `component: X`
    becomes a `vstack` of old + new; an existing `vstack` gets one more
    item;
  - a screen with tabs: a new tab labelled with the component's title;
  - several screens: a new screen, plus an item in the initial screen's
    `type: menu` when there is one; without a menu, onto the initial
    screen's layout as above, with a note saying so.

  `--place stack|tab|screen` overrides. A single-screen config is never
  converted to multi-screen.
- **Splice text into the original bytes**; yaml.v3 only finds where to
  insert. Re-encoding through an AST reformats the file (the research,
  on the local branch `research/yaml-editing`, measured a median of 10
  changed lines with yaml.v3; splicing changed at most 1 across all
  examples).
- **Refuse, naming the line,** when splicing isn't safe: a
  flow-collection parent (`{}`), an alias or `<<:` merge, a last entry
  ending in a `|+` scalar, multiple documents, tab indentation or CRLF.
- **Name clashes** are refused, naming the clash and suggesting `--name`
  ("source `pods` already exists — pass `--name pods2`"). No silent
  renames.
- **Before writing:** re-parse and check the result is the old config
  plus exactly the new entries, then `config.Load` it. On failure
  nothing is written and the error says why.
- **Output:** `--dry-run` prints a unified diff and writes nothing; a
  normal run writes atomically (temp + rename) and prints a summary
  ("added source `pods`, table `pods` to screen `main`"). No `.bak`.
- **As built (PR 8):** an `hstack` or `zstack` layout is refused too
  (adding to one means re-indenting it); 46 of the 55 examples take a
  splice and the other 9 refuse for that reason. The load check stands
  in for required env vars that aren't set, since the old config needs
  them as much as the new. A missing `data:` goes just above `tui:` and
  its comments; the added source carries a `# Added by …` comment.

## What's already in place

- The `theme:success`, `theme:warning` and `theme:error` colour tokens,
  on tuilib v0.44.0's `Palette.Success`. Building them fixed colour
  rules on hex palettes, which used to render a `theme:` colour plain.
- Exec windows, eventlogs, streaming tables and logviews, `bind:`,
  input fields and parameter defaults: everything `add exec` writes
  already loads and runs.
- Cobra, through wrangl.
- **Runtime fixes made for PR 3**, since `add exec` writes what they
  read: a one-shot exec source reads NDJSON (and concatenated values, as
  `go list -json` prints) as a list; a cell holding a list of plain
  values shows `a, b` instead of `[a b]`; and `sort: si` understands
  binary and byte units (`12Ki`, `2GiB`, `900kB`, `187MB`), which it
  used to read as 0.

## Implementation order

Each PR is usable on its own and leaves `go test ./...` green.

1. **Subcommands** (section 1): Cobra root with the unchanged run form,
   `version`, an empty `add`, exit codes, `docs/cli/tui-builder.md`
   sections and the docs-flags test.
2. **`init`** (section 2), with its golden test and the quickstart
   change.
3. **`add exec`: sampling and inference** (sections 3 and 4, without
   style): run, sample, infer `root:`, columns, sorts, `mark_key`,
   names; print a complete config. Golden tests from recorded samples
   (a top-level array, a `List`, NDJSON, a single object, several
   arrays).
4. **Column ranking and style** (the style half of section 4):
   ranking, the 8-column cap and commented-out rest, bold, the status
   vocabulary, widths, `--max-columns`, `--no-style`.
5. **Shaping flags** (section 5): `--param` with input fields and
   `--no-inputs`, `--columns`, `--component`, `--name`, `--title`,
   `--refresh`, contradiction errors, the inferred-or-flag summary.
6. **Streams** (section 6): detection, `--follow` / `--no-follow`,
   streaming tables with `row_key` or `max_rows`, logviews, and the
   check on the runtime's stream framing.
7. **Windows** (section 7): `--window`, seekable and anchored,
   `--total-path`, `--cursor-path`, eventlogs, `--growing`, windowed
   filter and sort rules.
8. **`--into`** (section 8): the splicer with its refusals, placement,
   `--place`, clash checks, verify + `config.Load`, `--dry-run` diff,
   atomic write. Tested against every example config.

## Follow-ups

Left for after v1; each needs a runtime feature or a decision of its own.

- **Exec actions from commands**, not just sources.
- **An inspector of a table's focused item, with no refetch**, so
  `add exec` could show every field of the focused row under the table.
- **Relative age for timestamps** ("3m ago"): a cell format.
- **An eventlog item's colour read from a field** (`color_by: level`).
- **Not planned:** flags for `env`, the working directory, `pipe:`
  operators and `initial_sort` (hand-edit after); template variants for
  `init`; commands that print text tables; editing configs
  interactively.
