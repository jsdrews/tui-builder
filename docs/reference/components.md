# Components

Components live in `tui.components:`, keyed by name, and are placed on
screens by layout nodes (see [app.md](app.md#layout-nodes)). A component
is either **static**, holding content declared in the YAML, or
**source-bound**, populated from a `data.sources` entry named by
`source:` after every fetch.

| Kind | Shows | Static content | Source-bound mapping |
|---|---|---|---|
| [`list`](#list) | one string per row | `items:` | `item:` |
| [`table`](#table) | rows and columns | `rows:` | each column's `value:` |
| [`logview`](#logview) | appended lines, searchable | `lines:` | none (lines as-is) |
| [`textview`](#textview) | one block of text | `content:` | none (text as-is) |
| [`tree`](#tree) | a hierarchy, expandable | `root:` | `label:` + `children:` or `group_by:` |
| [`inspector`](#inspector) | label/value pairs, nested | `fields:` | each field's `path:`, or `auto:` |
| [`eventlog`](#eventlog) | a timeline of paged events or lines | — | `key:` + `text:` (windowed sources only) |

## Common fields

These apply to every kind unless the column says otherwise.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | — | required | The kind: `list`, `table`, `logview`, `textview`, `tree`, `inspector` or `eventlog`. |
| `title` | template | — | — | Shown on the pane border. |
| `source` | string | — | — | The `data.sources` entry that populates this component. When set, static content (`items`, `rows`, `lines`, `content`, `root`, `fields` values) is ignored. |
| `colors` | map | — | — | Per-component color overrides. See [`colors`](#colors). |
| `color_rules` | list | — | list, logview, eventlog | Colors each item or line by its value. Tables put rules on each column and inspectors on each field. See [color rules](#color-rules). |
| `filterable` | bool | `false` | list, table, inspector, eventlog | `/` opens a filter that narrows the rows (`f` on an eventlog, where `/` is search). |
| `searchable` | bool | `false` | logview, textview, tree, eventlog | `/` opens a search that highlights matches; `n` / `N` move between them. |
| `filter_placeholder` | template | — | any filterable or searchable kind | Hint shown inside the empty filter or search input. |
| `initial_filter` | template | — | list, table | Starts with this filter applied. |
| `initial_query` | template | — | logview, textview, tree, inspector | Starts with this search or filter query applied. |
| `initial_cursor` | int | `0` | list, table, tree, inspector | Row the cursor starts on. |
| `markable` | bool | `false` | list, table, tree | Multi-select: adds a mark gutter and binds `x` (toggle), `X` (extend from the last mark), `A` (all), `D` (none). Actions with `multi: true` then run once per marked row. |
| `mark_key` | path | — | source-bound markable table | The stable identity marks are held by, read from the source item (not the rendered cells), e.g. `metadata.uid`. Required there. |
| `on_cursor` | map | — | source-bound target | Refetches this component's source as another component's cursor moves. See [`on_cursor`](#on_cursor). |

**Validation:**

- `type` is one of the seven kinds; `source:` names a defined entry.
- `markable` only on list, table, tree. On a table bound to a source,
  `mark_key` is required. It isn't accepted on lists (they key on the item
  string), trees (the node path), static tables (row position) or without
  `markable: true`. It isn't defaulted to the first column because a
  non-unique column collapses rows onto one mark and a volatile one (AGE,
  STATUS) loses the marks on the next poll.
- `markable` with a windowed source is an error: a window holds rows
  without keys.

## `list`

One string per row.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `items` | list of template | — | static | The rows. |
| `item` | string | — | required when source-bound | Dot-path to each element's display string. |

```yaml
regions:
  type: list
  title: Regions
  source: regions
  item: name
  filterable: true
```

## `table`

Rows and columns, with sorting, filtering and horizontal scroll.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `columns` | list | — | required | Column definitions. See [columns](#columns). |
| `rows` | list of list | — | static | Each row is a list of cells. A cell is a string, `{value, color}` for a colored cell, or `{label, url}` for a hyperlink. |
| `initial_sort` | map | — | — | Sort to start with: `column` (a column title, case-insensitive prefix, or a 1-based number) and `desc` (bool). Only `sortable` columns match; anything else is ignored. |
| `sort_debounce` | duration | tuilib's (400ms) | windowed source | How long sort input must go quiet before the source is asked for the new order, so stepping through columns with `[` / `]` sends one request. `0` sends every change. |
| `max_rows` | int | `100` | streaming source, no `row_key` | Ring-buffer size: each arriving event is prepended as a row and the oldest drop past this size. `-1` is unbounded. |
| `row_key` | path | — | streaming source | Keyed-upsert mode: an event whose key matches a row updates it in place; a new key appends. Without it, a streaming table is a ring buffer. Distinct from `mark_key`. |

How a table fills depends on its source:

- **Fetched or polled:** the rows are replaced on each fetch; cursor,
  filter, sort and marks survive.
- **Streaming** (`follow: true`, websocket): each event adds or updates a
  row, per `max_rows` / `row_key`.
- **Windowed** (the source declares `window:`): the table holds only the
  rows on screen and the server answers the filter and sort. It switches
  to remote filtering and sorting automatically. Committing a filter or
  sort keeps the current rows on screen, dimmed, until the answer lands;
  a failed query keeps the rows and the typed filter, and `r` retries.
  Each query's outcome is logged to the output console under the
  source's name. See [sources.md](sources.md#window).

**Validation:** `columns` is required. When source-bound, every column
needs `value:`. On a windowed table, a `sortable` column needs
`window.sort_param:`. `sort_debounce` is only accepted on a windowed
table, since a local sort is instant.

### Columns

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `title` | template | — | required | Header text. Also the name used by `${selection.Title}`, `${cursor.Title}`, `title:value` filter terms and `window.filters`. |
| `value` | path | — | required when source-bound | Dot-path to the cell value in each item. A list of paths is tried in order and the first non-empty wins. |
| `width` | int | `0` | — | `0` sizes to content; a positive number is a fixed width. |
| `flex` | int | `0` | — | Share of leftover width. |
| `max_width` | int | — | — | Caps a flex or auto column. |
| `align` | string | `left` | — | `left`, `right` or `center`. |
| `sortable` | bool | `false` | — | The column can be sorted from the keyboard. |
| `sort` | string | `string` | `sortable` | Comparator: `string` (case-insensitive), `number` (commas stripped), `si` (number with K/M/B/G/T suffix). |
| `hidden` | bool | `false` | — | Not drawn, but still in the row: it matches filters and resolves in `${selection.*}` / `${cursor.*}`. For identity columns a drilldown needs. |
| `color_rules` | list | — | — | Colors each cell by its value. See [color rules](#color-rules). |

```yaml
pods_table:
  type: table
  source: pods
  filterable: true
  markable: true
  mark_key: metadata.uid
  initial_sort: {column: Restarts, desc: true}
  columns:
    - {title: Name, value: metadata.name, flex: 1}
    - {title: Namespace, value: metadata.namespace, hidden: true}
    - title: Status
      value: [status.containerStatuses.0.state.waiting.reason, status.phase]
      color_rules:
        - {when: Running, color: green}
        - {when: "~Err|Crash", color: red}
    - {title: Restarts, value: status.containerStatuses.0.restartCount, align: right, sortable: true, sort: number}
```

## `logview`

Lines appended over time, with search and an optional filter mode. The
natural target for streaming sources.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `lines` | list of template | — | static | Initial lines. |
| `max_lines` | int | `10000` | — | Lines kept before the oldest drop. `-1` is unbounded. |
| `filter_mode` | bool | `false` | `searchable` | Starts in filter mode: the search hides non-matching lines instead of highlighting matches. |

When source-bound, a `format: text` body is split on newlines, a JSON
list of strings is used as-is, and a streaming source appends one line
per event.

```yaml
pod_logs:
  type: logview
  title: Logs
  source: pod_logs        # http with follow: true
  searchable: true
  color_rules:
    - {when: "~ERROR", color: red}
```

## `textview`

One block of text, replaced on each fetch. For help panes, command
output and documents.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `content` | string | — | static | The text. Replaced by the source's value when source-bound. |
| `wrap` | bool | `false` | — | Word-wrap. `w` toggles it at runtime. |

When source-bound, a `format: text` body passes through verbatim and a
JSON value is shown formatted.

```yaml
help:
  type: textview
  title: Help
  searchable: true
  wrap: true
  content: |
    Press / to search, w to toggle wrap.
```

## `tree`

A hierarchy with expand and collapse. Static trees declare `root:`;
source-bound trees build nodes from the data.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `root` | node | — | required when static | The root node: `label` (template) and `children` (a list of nodes). |
| `label` | path | — | required when source-bound | Dot-path to each node's display label. |
| `children` | path | — | source-bound; not with `group_by` | Dot-path on each node to its list of child nodes, for nested data (filesystem trees, org charts). Nodes with none are leaves. |
| `group_by` | path | — | source-bound; not with `children` | Buckets a flat list by this value; each bucket becomes a parent node labelled with it (e.g. `group_by: kind`). |
| `root_label` | template | `title`, then the source name | source-bound | Label of the root node. Kept stable across refreshes so expansion state survives. |
| `initial_depth` | int | `0` | — | Expands every node shallower than this: `0` root only, `1` root expanded, `2` one level more. |

Cursor and expansion state survive refreshes.

```yaml
by_team:
  type: tree
  title: People
  source: people
  label: name
  group_by: team
  initial_depth: 1
  searchable: true
```

## `inspector`

Label/value pairs for one record, with nested groups that expand.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `fields` | list | — | not with `auto` | The rows. See [inspector fields](#inspector-fields). |
| `auto` | bool | `false` | source-bound; not with `fields` | Builds the rows from whatever the source returns: maps expand into groups, arrays into `[0]`, `[1]`…, scalars render as-is. |
| `initial_depth` | int | `0` | — | Expands every group shallower than this. |

### Inspector fields

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `label` | template | — | required | The left-hand text. |
| `value` | template | — | — | Static right-hand text. Leave empty for a group header. |
| `path` | string | — | source-bound | Dot-path into the source's value; overrides `value`. |
| `color_rules` | list | — | — | Colors the value. See [color rules](#color-rules). |
| `children` | list | — | — | Nested fields, shown as an expandable group. |

```yaml
pod_detail:
  type: inspector
  source: pod
  fields:
    - {label: Name, path: metadata.name}
    - label: Status
      children:
        - {label: Phase, path: status.phase, color_rules: [{when: Running, color: green}]}
        - {label: Node, path: spec.nodeName}
```

## `eventlog`

A timeline of events or lines read in pages from a source too large to
hold: an AWX job's events, a search index, a long log. Each item draws as
the lines of its text. Unlike a logview, which holds a stream whole, an
eventlog binds only to a [windowed source](sources.md#window) and fetches
as you scroll. Over an [Anchored](sources.md#anchored-windows) source it
opens at the newest item and walks older as you scroll up.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `key` | path | — | required | Each item's identity. Bare `${selection}` when enter fires an action. |
| `text` | path | — | required | The text an item draws as, split on newlines. An empty value draws as a dim "· no output" line: the item still happened, and enter still opens it. |
| `mark` | path | — | — | Shown as-is in the gutter, normally the server's timestamp. |
| `start` | string | `oldest` | — | Where the view opens once the source isn't growing: `oldest` or `newest`. A [growing](sources.md#window) source always opens at the newest item and follows. |
| `max_items` | int | tuilib's | — | Items held at once; the end furthest from the view is trimmed. |

**Keys:** `/` searches the items held, and `n` / `N` jump between
matches. `f` filters (narrowing the query the source answers) when
`filterable:` is set. `G` goes to the newest item and follows it; moving
away while the source is growing counts new items as "↓ N new" instead
of moving you.

**Enter** (or a double click) fires the screen's `key: enter` action if
one is bound with this eventlog as `from:`, with the item as
`${selection}`: bare `${selection}` is the key, and every field of the
item is `${selection.PATH}` (`${selection.event}`,
`${selection.event_data.host}`). With no action bound, enter opens the
item's raw data in an inspector; `esc` closes it.

**Validation:** `source:` must declare `window:`; `key` and `text` are
required; `start` is `oldest` or `newest`. These fields are rejected on
other kinds. An eventlog can't be `markable`.

```yaml
events:
  type: eventlog
  title: Job events
  source: job_events      # declares window:
  key: id
  text: stdout
  mark: created
  searchable: true
  color_rules:
    - {when: "~^fatal|FAILED", color: red}
```

## Color rules

A rule pairs a matcher with a color. Rules are tried in order and the
first match colors the text; with no match it renders plain. The
selected-row background passes through.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `when` | string | — (always matches) | — | Exact text (case-insensitive), `~regex` (case-insensitive), or a numeric comparison: `> 5`, `<= 10`, `== 0`, `!= 0`, with an optional K/M/B/G/T suffix on the number. Empty matches everything, which makes a final default rule. |
| `color` | color | — | required | See [color values](templating.md#color-values). |

## `colors`

Overrides individual theme colors on one component. Unset fields keep
the theme's. Values are [color values](templating.md#color-values),
including `theme:<token>`, which follows the palette when themes cycle.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `border_active` | color | theme | all | Border color when focused. |
| `border_inactive` | color | theme | all | Border color when unfocused. |
| `spinner` | color | theme | all | The loading spinner. |
| `selected` | color | theme | list | The focused item. |
| `selected_fg` | color | theme | table | Focused row text. |
| `selected_bg` | color | theme | table | Focused row background. |
| `header` | color | theme | table | Header text. |
| `cell` | color | theme | table | Ordinary cell text. |
| `column_separator` | color | theme | table | The line between columns. Palette colors only (named or 0–255). |
| `header_rule` | color | theme | table | The line under the header. Palette colors only. |
| `label` | color | theme | inspector | Field labels. |
| `value` | color | theme | inspector | Field values. |
| `match` | color | theme | inspector, logview, tree | Search-match highlight. |
| `current_line_bg` | color | theme | inspector, logview, tree | Background of the cursor line. |

## `on_cursor`

Refetches this component's source whenever another component's cursor
moves, binding the driver's focused row into the source's
`parameters:`. This is the "table on top, detail below" pattern, without
pushing a screen. Repeat visits to a row hit the source's param cache.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `source` | string | — | required | The driver: a table, list or tree on the same screen. (It names a component, not a data source.) |
| `bind` | map of template | — | — | Maps this component's source parameters to templates over the driver's focused row: `${cursor}`, `${cursor.N}`, `${cursor.Title}`, `${cursor.path}`, plus `${env.*}`. See [templating.md](templating.md#cursor). |

**Validation:** the driver is a table, list or tree placed on the same
screen; the component declaring `on_cursor` must itself have a `source:`.

```yaml
repo_detail:
  type: inspector
  source: repo            # declares parameters: owner, name
  auto: true
  on_cursor:
    source: repos_table
    bind:
      owner: ${cursor.Owner}
      name:  ${cursor.Repo}
```
