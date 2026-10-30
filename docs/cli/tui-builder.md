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

### `tui-builder gen`

```sh
tui-builder gen [flags] <spec>
```

Reads an OpenAPI 3.0 or 3.1 document and writes a tui-builder config for
the API to stdout (or `-o FILE`). The spec is a file, an `http(s)://`
URL, or `-` for stdin, in JSON or YAML; `$ref`s to other files and URLs
are followed. It isn't validated strictly: real specs that bend the rules
still generate.

- **The preset:** one for the whole API under `data.presets`, named from
  `info.title` (`Things API` → `things_api`; `--name` overrides), which
  every source extends. Its `url:` is `${env.THINGS_API_URL}`, declared
  in `app.env` with the first server's URL (variables at their defaults)
  as its default. Other servers and a variable's allowed values are
  comments; `--prompt-server` asks which server at startup instead. A
  relative server URL resolves against the spec's URL; from a file or
  stdin, and with no servers at all, `--base-url` supplies it.
- **Auth** comes from the security requirement: bearer, basic, API keys
  (header, query or cookie), OAuth2 client credentials, and OpenID
  Connect (its token URL read from the discovery document, for a spec
  fetched from a URL). Every secret is a required `app.env` entry with
  commented-out `from:` examples, never a value. Other OAuth2 flows read a
  bearer token from the environment, with a comment on getting one; a
  scheme tui-builder can't send (mutualTLS) is a warning. Of alternatives
  the first is used; schemes required together go in as headers or query
  parameters. An operation with `security: []` gets `unset: [auth]`, and
  one with another scheme its own `auth:`.
- **`retry: {}`** goes on the preset when the spec documents a 429 or
  rate-limit headers.
- **Screens,** one per GET it can show, named from the path (`/things` →
  `things`, `/things/{thing_id}` → `thing`, `/owners/{owner_id}/things` →
  `owner_things`):
  - a **list** answers an array, or an object holding one list of objects
    (FastAPI's `Page[T]`), which becomes `root:`; with several, the one
    named `entries`, `items`, `data`, `results`, `records` or `values`. It's
    a table (a list of strings is one column; see Columns, below);
  - a **detail** (`/things/{thing_id}`, answering an object) is pushed by
    `enter` on its list's row, with the row's `thing_id`, `id`,
    `thing_id`-less-`thing_` (`slug` for `report_slug`) filling the path
    parameter, and shown by an `auto: true` inspector;
  - a **sub-list** (`/owners/{owner_id}/things`) is pushed from its parent
    list's row through the action menu, on a key from its name (`a`, then
    `i`);
  - a **singleton** (no path parameters, one object) is an inspector.

  A required query parameter is asked for at startup (`app.prompts`; an
  enum is a select) and sent from the environment; optional ones are left
  out. A sub-list whose parent list isn't generated (the spec has none, or
  `--tag` left it out) is a list of its own, its path parameter asked for
  at startup the same way. A spec with nothing to show stops with
  "nothing to generate".
- **Columns** come from the row schema: scalars, nested objects
  flattened two levels (`labels.team`), and lists of plain values (shown
  `a, b`); lists of objects, maps, alternatives of different shapes and
  `writeOnly` fields are left out. They're ranked as `add exec` ranks a
  sample's, with the schema as the evidence: the identity (a name-like
  field, else the id a detail binds), then states (an enum, a boolean, a
  status-like name), timestamps (`date-time` / `date`), numbers, short
  text, and long text (a description-like name, or a `maxLength` over
  80). The first 8 are written, one slot kept for the first long-text
  column, and the rest follow commented out. The style is `add exec`'s
  too: the identity bold, status colours for the enum's values,
  `> 0` warnings on failure counts, numbers right-aligned with
  `sort: number`, and `flex: 1` on the long text. Titles are the field
  name in words, not the schema's `title`. A column a push binds is
  always written, hidden when it's past the cap. Off a window, a table
  whose rows have a required `id` is markable by it. A detail's or
  singleton's inspector colours its enum fields the same way, each rule
  scoped by `path:`.
- **Paging,** from a list's query parameters and response:
  - page-number or offset parameters (`page`, `p`, `offset`, `skip`…),
    a page size (`per_page`, `limit`, `page_size`…) and a total (a
    `total` / `count` / `totalCount`-like field or `meta.total`, or an
    `X-Total-Count` header) make a **window**: the table fetches only the
    rows on screen, and the server sorts and filters. Its page size is
    the size parameter's maximum up to 100, else its default, else 100;
    a page parameter's minimum is its first page;
  - otherwise a list **walks** every page, up to 20 (`max_pages`): by
    the next URL (a `next` field, or a `Link` header), by a cursor (a
    `next_cursor`-like field sent back in `cursor` / `page_token` /
    `after`; or Stripe's `starting_after` with `has_more`), or by page or
    offset, stopping on a short page when there's no total. Two
    parameters that could page a list make it walk too, with a note;
  - with no paging parameters, it's one request.

  On a window, the sort parameter (`order_by`, `sort`, `ordering`…) and
  its enum become `sort_fields` (a `-` prefix means descending, and a
  value that matches a column only once case and underscores are ignored
  gets a `sorts:` entry); a `q` / `search`-like parameter is the search;
  a parameter named like a column's field filters that column, as do
  range pairs (`size_gte` / `size_lte`, `created_after` /
  `created_before`); enum columns list their values for completion; and
  the table is filterable only with somewhere to send a filter. Columns
  the enum doesn't list don't sort. Off a window, every column sorts
  and filters the rows held. Notes after the summary say which lists
  walk, and why one that looked windowed doesn't.
- **Writes** (POST, PUT, PATCH, DELETE) become actions bound on their
  list's screen, in the action menu (`a`):
  - on the list's own path, a **collection** action (`POST /things`,
    `n` to create); on the item's path, or below it, an **item** action
    on the focused row (`PATCH /things/{id}` is `e` to edit, `PUT` is
    Replace, `DELETE` is `d` and asks "Delete thing-001?" first,
    `POST /things/{id}/restart` takes a key from its name). The row's id
    fills the path, as for a detail;
  - the body's fields (JSON or form) and the query parameters are asked
    for in a form, in the schema's order: required as declared (an edit
    asks every field optional, so a PATCH sends only what's typed), an
    enum as a select, a constraint (`maxLength`, `pattern`, `minimum`…)
    as a check with a message, a password masked. A spec default is
    shown, never filled in. An optional boolean is a select with a blank
    (left out), and a non-integer number is typed as text; both carry a
    comment. `readOnly` fields aren't asked for; an object or list field
    is left out with a note, and if it's required the action is skipped;
  - a create reports "created ${body.name}", and every write ends
    "(r to refresh)": the list doesn't refresh itself after a write;
  - on a table that marks rows (off a window), a DELETE or a body-less
    item POST runs over every marked row;
  - not generated: multipart uploads, writes with two path parameters,
    and writes with no list to attach to. `--read-only` generates none.

  With both pushes and writes on a screen, `app.action_groups` puts the
  menu's read actions first.
- **Navigation:** lists and singletons are the top-level screens. One is
  the app; two to five are tabs on a home screen titled `info.title`; six
  or more are a home menu, each item showing its method and path, grouped
  by first tag in the spec's `tags:` order (untagged last, under `other`),
  with each tag's description as a comment above its group. `--layout`
  picks tabs or a menu instead. Lists are titled for what they hold
  ("Things"), singletons by their summary; a label two screens share
  becomes each one's name ("User issues"). A pushed screen takes its title
  from the row that opened it: "Thing ${selection.Name}" for a detail
  (the id when the list has no name-like column), "${selection.Name}'s
  things" for a sub-list.
- **Big specs** are generated whole. Past 40 top-level screens, or 200
  operations, a hint after the summary suggests `--tag`, with how many
  screens each tag has (or `--operation`, for a spec with no tags).
- **Not generated,** with a line each on stderr and in the config's
  header comment: operations that answer something other than JSON (or no
  schema, or a single value), need a header or cookie parameter, are
  `deprecated: true`, or have a path parameter no list fills; a response
  with several unnamed lists; the writes above; webhooks.
  A detail whose parameter matches no column of its list is generated with
  a warning: nothing opens it.
- **Overrides.** A spec doesn't say everything: a server may filter on
  fields it doesn't document (`name__icontains`), refuse a sort its enum
  lists, or need a column, title or secret source the spec can't name.
  Don't edit the generated file; put changes in an overrides file and
  pass `--overrides FILE` every time, so a regeneration keeps them.
  `--init-overrides FILE` writes a starter listing every generated
  operation, commented out, with the hints each can take. The file has
  two parts:

  ```yaml
  gen:                                  # hints, keyed by the spec's names
    name: things_api                    # any global flag but --header
    layout: menu
    security: ApiKey                    # use this alternative, not the first
    order: ["GET /owners", "GET /things"]
    operations:
      "GET /things":                    # or its operationId
        title: Stock                    # tab, menu label and titles
        group: inventory                # menu group
        hide: true                      # off the menu and tabs
        columns: [name, status, size]   # these, in this order
        window:                         # merged into the window
          search_param: name__icontains
          filter_ops: {contains: "${column.value}__icontains"}
        sort_refuse: [status]           # the server won't sort by it
        max_pages: 50                   # a walk's cap
        root: data                      # where the rows are
        anchored: {cursor: id, older: {…}, newer: {…}}
        skip: true                      # don't generate it
      "DELETE /things/{thing_id}":
        confirm: false                  # or true, or the question
  app:                                  # config, merged on top
    env:
      - {name: THINGS_API_TOKEN, from: {command: [pass, show, things-api/token]}}
  tui:
    components:
      things:
        columns:
          - {title: Description, $drop: true}
  ```

  - **Hints** survive regenerating because they name operations the
    spec's way. A flag given on the command line beats the file's key.
    `filter_ops` brings a `filter_except` of the columns it can't match
    (numbers, booleans, enums, ids, timestamps), unless you give one. A
    hint that doesn't fit what the operation became (columns on a
    detail, max_pages on a window, anything but confirm on a write) is an
    error, and a hint for an operation nothing was generated from is a
    warning.
  - **Config** (`app`, `data`, `actions`, `tui`) is merged onto the
    output by its generated names, JSON-merge-patch style: maps merge,
    other values replace, `null` deletes. Lists replace too, except
    those whose items have an identity, which merge item by item: `env`
    by `name`, `prompts` by `key`, `columns` by `title`, a screen's
    `actions` by `action`, a menu's `items` by `screen`, `tabs` by
    `label`. `{…, $drop: true}` removes one.
  - **Refused:** unknown keys (with the nearest one suggested), `skip`
    with other hints, a `$drop` that matches nothing, and a field set by
    both a hint and the config. A hint for an operation the spec no
    longer has is an error, or a warning with `--prune-overrides`.
- **New operations.** A comment at the end of the file lists every
  operation in the spec.
  When `-o` replaces a config gen wrote, it reads that list first and
  says what changed since: `new in the spec: GET /status` (generated
  with defaults, or skipped with a reason like any other), and `gone
  from the spec: GET /legacy`. The whole spec is listed, not only what
  was generated, so a different `--tag` doesn't make anything look new.
- **Before writing,** the config is loaded as `tui-builder` would load
  it; if that fails, nothing is written. The same spec and flags always
  give the same bytes.

| Flag | Default | What it does |
|---|---|---|
| `-o`, `--output FILE` | stdout | Writes the config to `FILE`. An existing file is an error without `--force`. |
| `--force` | off | Overwrites an existing `-o` or `--init-overrides` file. |
| `--header 'Name: value'` | — | Sent when fetching the spec (and its `$ref`s) from a URL, for specs behind auth. Repeatable. Never written to the config. |
| `--timeout D` | `30s` | How long fetching from a URL may take. |
| `--tag TAG` | all | Only operations with this tag. Repeatable; globs (`admin*`). |
| `--exclude-tag TAG` | — | Leaves out operations with this tag, after `--tag`. Repeatable; globs. |
| `--operation ID` | all | Only these operationIds. Repeatable; globs (`list_*`). |
| `--exclude-operation ID` | — | Leaves out these operationIds, after `--operation`. Repeatable; globs. |
| `--read-only` | off | Generates nothing from POST, PUT, PATCH or DELETE. |
| `--include-deprecated` | off | Keeps operations marked `deprecated: true`. |
| `--name NAME` | from `info.title` | The preset's name; upper-cased, the prefix of its env vars. |
| `--base-url URL` | the first server | The base URL's default. Needed when the spec's server is relative and it's read from a file, or when it lists none. |
| `--layout tabs\|menu` | by count | The home screen: tabs (one per top-level screen) or a menu, whatever the count. |
| `--prompt-server` | off | When the spec lists several servers, asks which at startup (`app.prompts`). |
| `--overrides FILE` | — | Merges this file's hints and config into what's generated. See Overrides, above. |
| `--prune-overrides` | off | Warns, rather than fails, on hints for operations the spec no longer has. |
| `--init-overrides FILE` | — | Writes a starter overrides file listing every generated operation, instead of the config. Refuses an existing file without `--force`. |
| `--quiet` | off | Prints no summary, hints or skipped operations. Warnings still print. |

Exit status: `0` written (skipped operations aren't a failure), `1` an
error (the spec can't be read, nothing to generate, an existing `-o`
file, a config that doesn't load), `2` a usage error.

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
