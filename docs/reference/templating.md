# Templating, paths and expressions

Four small languages appear in the schema:

- **Templates** are strings with `${…}` tokens, substituted before use
  (`url`, `command`, `title`, `confirm`, …).
- **Dot-paths** name a value inside data (`root`, a column's `value`, a
  field's `path`).
- **Expressions** compute a value per item (`where`, `keep`, `compute`,
  `by`, `on`, `success`).
- **Color values** are used in `color_rules` and `colors`.

Field tables in the other pages give each field's type as `template`,
`path`, `expr` or `color`.

## Tokens

| Token | Resolves to | Where |
|---|---|---|
| `${env.NAME}` | The environment variable `NAME`; empty when unset | Everywhere a template is accepted |
| `${params.NAME}` | A bound source parameter | A source's `url`, `method`, `body`, `headers`, `path`, `command`, `env`, `initial_messages` |
| `${selection}` | The focused row of a binding's `from:` component: a list's item, a table's first cell, a tree node's label | Binding `bind:` and `confirm:`; a pushed screen's title, component content and source fields |
| `${selection.N}` | A table row's Nth cell, 1-based | As above |
| `${selection.Title}` | The cell in the column titled `Title` (case-insensitive, exact) | As above |
| `${cursor}` … | The live focused row of an `on_cursor` driver (see [below](#cursor)) | `on_cursor.bind` |
| `${inputs.NAME}` | An action input, after `bind:` and the form | An action's `run`, `url`, `method`, `headers`, `body`, `message`, `error_message`; a binding's `confirm` |
| `${code}`, `${output}` | Exit or HTTP status; stdout or response body | `message`, `error_message` |
| `${body.PATH}` | A field of the action's JSON response, by dot-path | `message`, `error_message` |
| `${window.*}` | The window being requested (offset, limit, filter, sort) | A windowed exec source's `command`; see [sources.md](sources.md#window) |

### When tokens resolve

- **`${env.*}`** is substituted once over the whole config: by
  `tui-builder` after `app.prompts` runs (so a boot prompt can supply a
  variable), and by `wrangl` right after load. A resolved value is data:
  if it contains `${env.OTHER}`, that text is left alone.
  `wrangl --describe` deliberately shows the unsubstituted template, so
  secrets aren't printed.
- **`${params.*}`** is substituted when a caller binds the source. A
  token naming an undeclared parameter is left literal, so the typo is
  visible in the request.
- **`${selection…}`** is substituted when a binding fires or a screen is
  pushed, freezing the row at that moment. A column title that doesn't
  match is left literal.
- **`${cursor…}`** is substituted on every cursor move. Unresolved
  lookups become empty, so a missing column can't produce a request
  storm against a broken URL.
- **`${inputs.*}`** is substituted when the action runs. A known input
  with no value becomes empty. Unknown `${…}` text in an exec `run:` is
  left as written, so shell syntax like `${PAGER:-less}` survives.

Fields that name data rather than display text are never templated:
dot-paths (`root`, `value`, `path`, `item`, `label`) and expressions
(`where`, `keep`, `compute`, `by`, `on`, `success`).

A good habit: let sources reference only `${params.*}` and `${env.*}`,
and map selections onto parameters in `bind:`. The source then works the
same from a screen, from `on_cursor`, and from
`wrangl --param`.

### Cursor

`${cursor…}` tokens read the driver named by `on_cursor.source`:

| Token | Table | List | Tree |
|---|---|---|---|
| `${cursor}` | first cell | the item | the node's label |
| `${cursor.N}` | Nth cell | `${cursor.1}` is the item | Nth path element (root is `.1`) |
| `${cursor.Title}` | cell by column title (case-insensitive, exact) | `${cursor.item}` | — |
| `${cursor.label}` | — | same as `${cursor}` | same as `${cursor}` |
| `${cursor.depth}` | number of cells | number of cells | number of path elements |
| `${cursor.path}` | cells joined with `/` | the item | path from the root, joined with `/` (e.g. a real file path) |

## Dot-paths

A dot-path walks into parsed data: `metadata.name`, `items.0.status` or
`items[0].status`. An empty path is the value itself. A missing key, an
out-of-range index or a type mismatch gives an empty result rather than
an error.

Fields typed `path` (a column's `value`, a tree's `label`, `children`
and `group_by`, `mark_key`, `row_key`) also accept a list, tried in
order until one gives a non-empty value. This is how a kubectl-style
STATUS column falls back through container states:

```yaml
value:
  - status.containerStatuses.0.state.waiting.reason
  - status.containerStatuses.0.state.terminated.reason
  - status.phase
```

## Expressions

Operator fields and an action's `success:` use
[expr-lang](https://expr-lang.org/docs/language-definition) expressions.

- **In operators**, each item's top-level fields are in scope
  unprefixed: `status.phase == 'Running'`. The whole item is also `item`,
  which is how a plain text line is read: `item contains 'ERROR'`. An
  operator's own parameters are `params.NAME` (strings; convert with
  `int(params.min)`).
- **In `success:`**, the variables are `code`, `output` and `body`.
- A missing field is `nil`, not an error.
- A predicate (`where`) is true for `true`, non-zero numbers and
  non-empty strings, and false for `false`, `nil`, zero and `""`.
- **Functions:** expr-lang's built-ins (`len`, `hasPrefix`, `hasSuffix`,
  `indexOf`, `contains`, `matches`, `in`, `int`, `float`, `string`,
  `trim`, `split`, `join`, date functions, …) plus `lower`, `upper` and
  `now`.
- **Index syntax** is `items[0]`, not the dotted `items.0` form that
  dot-paths accept.

Built-in names (`count`, `sum`, `len`, `keys`, `values`, `filter`,
`map`, `all`, `any`, `one`, `none`, `sort`, `sortBy`, `contains`,
`startsWith`, `endsWith`) shadow item fields of the same name. Rename
such a field with `project:` before referencing it.

```yaml
where: "status.phase == 'Running' && !(metadata.namespace in ['kube-system'])"
compute:
  containers: "len(spec.containers)"
  name_upper: "upper(metadata.name)"
success: "code == 0 or output contains 'already exists'"
```

## Color values

| Form | Example | Notes |
|---|---|---|
| Named | `red`, `bright_green` | `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`, `gray` / `grey`, and `bright_` versions of each |
| Palette index | `"160"` | 0–255 |
| Hex | `"#ff8800"` | Not usable for `column_separator` / `header_rule` or color rules, which need a palette color |
| Theme token | `theme:accent` | Follows the active palette, including when themes cycle: `accent`, `current`, `muted`, `subtle`, `key`, `border-active`, `border-inactive`, `bar-bg`, `bar-fg`, `info-bg`, `info-fg`, `error-bg`, `error-fg` |

## Durations

Go duration strings: `250ms`, `5s`, `1m30s`, `2h`. Used by `refresh`,
`timeout`, `ttl` and `type: duration` inputs.
