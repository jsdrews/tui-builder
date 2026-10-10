# Config reference

Every field tui-builder's YAML accepts, one page per area. Each section
has a field table, the validation rules that apply, and a minimal
example. For a guided tour, start with the [README](../../README.md); for
complete, runnable configs, browse [`examples/`](../../examples/).

| Page | Covers |
|---|---|
| [app.md](app.md) | The top-level shape, `app:` (title, theme, glyphs, borders, keys, boot prompts, env), `tui.screen` / `tui.screens`, and layout nodes |
| [components.md](components.md) | Every component kind: list, table, logview, textview, tree, inspector, plus columns, color rules, colors and `on_cursor:` |
| [sources.md](sources.md) | Leaf sources: http, exec, file, websocket, static, merge, plus `parameters:`, `paginate:`, `window:` and `cache:` |
| [pipelines.md](pipelines.md) | Operator sources: passthrough, filter, project, derive, sort, union, compose, join, cache, and inline `pipe:` chains |
| [actions.md](actions.md) | The `actions:` registry, screen action bindings, inputs and generated forms, reserved keys |
| [templating.md](templating.md) | Every `${…}` token and where it resolves, dot-paths, the expression language, color values |

## Conventions

- **Field tables** use the columns **Field | Type | Default | Valid
  on/with | What it does**. "—" means no default (unset) or no
  restriction.
- **Types:** `string`, `int`, `bool`, `duration` (a Go duration string
  such as `500ms`, `30s`, `5m`), `path` (a dot-path, or a list of
  dot-paths tried in order; see [templating.md](templating.md#dot-paths)),
  `expr` (an expression; see
  [templating.md](templating.md#expressions)), `template` (a string that
  takes `${…}` tokens), `color` (see
  [templating.md](templating.md#color-values)), `map` and `list`.
- **Validation** is done at load, by both `tui-builder` and `wrangl`. A
  rule listed under a section is a load error with a message naming the
  field, not a silent fallback.

A test (`internal/config/reference_test.go`) fails when a config field
has no row in these pages, so a schema change and its documentation land
together.
