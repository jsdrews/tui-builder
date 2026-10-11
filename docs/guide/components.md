# Components

A component is a pane on screen. Declare it under `tui.components:` with
a name and a `type:`, give it static content or a `source:`, and place it
in a layout.

| Kind | Shows | Fill it with |
|---|---|---|
| `list` | one string per row | `items:`, or `source:` + `item:` |
| `table` | rows and columns | `rows:`, or `source:` + a `value:` per column |
| `logview` | lines that keep arriving | `lines:`, or a (usually streaming) source |
| `textview` | one block of text | `content:`, or a source |
| `tree` | a hierarchy | `root:`, or `source:` + `label:` and `children:` or `group_by:` |
| `inspector` | one record as label/value pairs | `fields:`, or `source:` with `auto: true` |
| `eventlog` | a long timeline of events or lines, read a page at a time | a windowed source + `key:` and `text:` |

Every field is in [Reference → Components](../reference/components.md).

## Binding to data

A bound component re-renders after every fetch. Cursor, filter, sort,
marks and tree expansion survive refreshes, so polling a source every few
seconds doesn't disturb the user.

Tables pluck each cell with a [dot-path](templating.md#dot-paths). A list
of paths is a fallback chain, tried until one gives a value:

```yaml
pods_table:
  type: table
  source: pods
  columns:
    - {title: Name, value: metadata.name}
    - title: Status
      value: [status.containerStatuses.0.state.waiting.reason, status.phase]
```

What a table does with a streaming source depends on `row_key:`. Without
it, each event is a new row at the top (a live tape, capped by
`max_rows:`); with it, an event updates the row with the same key (a
status board).

## Filter, search and sort

`filterable: true` (list, table, inspector, eventlog) makes `/` narrow the rows
(on an eventlog the key is `f`).
Terms are space-separated and all must match:

| Term | Matches |
|---|---|
| `nginx` | any cell containing "nginx" |
| `region:europe` | the column whose title starts with "region" |
| `~^web-\d+` | a case-insensitive regex |
| `status:~crash` | a regex in one column |

`searchable: true` (logview, textview, tree, eventlog) makes `/` highlight matches
instead; `n` and `N` move between them.

Columns marked `sortable: true` can be sorted: `[` and `]` step the sort
through the sortable columns, and `s` flips the direction. `sort: number` or `sort: si` (`2K`, `1.5M`) compare
numbers properly.

## Color

`color_rules:` colors values by what they say: on each column for
tables, on each field for inspectors, and on the component for lists and
logviews.

```yaml
color_rules:
  - {when: Running, color: green}       # exact text
  - {when: "~Crash|Error", color: red}  # regex
  - {when: "> 5", color: yellow}        # number
  - {when: "", color: gray}             # everything else
```

`colors:` overrides individual theme colors on one component. A
`theme:accent`-style value follows the palette when themes cycle.

## Selecting several rows

`markable: true` (list, table, tree) adds a mark gutter: `x` toggles a
row, `X` extends from the last mark, `A` marks all, `D` clears. Actions
with `multi: true` then run once per marked row.

Marks are held by key so they stay on the right rows when a poll
reorders them. A source-bound table names that key with `mark_key:`, a
field that's stable and unique per row (such as `metadata.uid`), even if
no column shows it.

## Detail that follows the cursor

`on_cursor:` refetches a component's source whenever another component's
cursor moves, without opening a new screen:

```yaml
repo_detail:
  type: inspector
  source: repo                # declares parameters: owner, name
  auto: true
  on_cursor:
    source: repos_table       # the component whose cursor drives this
    bind:
      owner: ${cursor.Owner}
      name: ${cursor.Repo}
```

Rows already visited are served from the source's parameter cache.

## Rows that are being worked on

`activity:` makes rows spin while the data says work is running on them:
a pod `Pending`, a sync `Running`. Every poll re-reads every row, so a row
starts and stops spinning when the server says so.

```yaml
pods_table:
  type: table
  source: pods            # refresh: 5s
  mark_key: metadata.uid
  activity:
    status: status.phase
    busy: [Pending, Terminating]
  columns:
    - {title: Name,  value: metadata.name}
    - {title: Phase, value: status.phase, width: 14}
```

When a polled source's reads start failing, its panes say
`· reads failing` on the title until a read succeeds, and the error is
reported once. Actions can make their rows spin straight away with a
[claim](actions.md#claim-the-rows-you-act-on).

## Long timelines: the eventlog

A job's events, a search index, an application log: too long to hold, so
an `eventlog` reads them a page at a time from a
[windowed source](data.md#big-remote-sets) and fetches more as you scroll.
Each item draws as the lines of its `text:`.

```yaml
events:
  type: eventlog
  source: job_events      # declares window:
  key: id
  text: stdout
  mark: created
  searchable: true        # / searches what's loaded
  filterable: true        # f narrows on the server
```

Enter on an item runs a `key: enter` action with the item as
`${selection}`, or, with none bound, opens its raw fields in an inspector.
A source that's still growing (a running job) makes the eventlog follow
the newest item; see [data](data.md#still-growing).

## Tables over Anchored sources

A table bound to an [Anchored](data.md#walking-from-an-edge) source is a
*span table*: it holds a run of rows walked from the newest (or oldest),
grows as you scroll towards either end, and is capped by `max_items:`.
Rows are keyed by `mark_key:`, which it requires, so marks hold as more
rows load. Its order is the walk's, so columns can't be sorted.

