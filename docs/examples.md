# Examples

Every file in [`examples/`](https://github.com/jsdrews/tui-builder/tree/main/examples),
embedded from the repository. From a clone, run one with
`task example NAME=<name>` (or `tui-builder examples/<name>.yaml`), or
browse and run them all with `task examples`, which opens
`example-launcher`.

Each file is commented and meant to be copied and adapted. Every one is
loaded by the test suite, so they stay valid as the schema changes.

## Components

### list

Filterable list and navigation keys.

```sh
task example NAME=list
```

??? example "list.yaml"

    ```yaml
    --8<-- "examples/list.yaml"
    ```

### table

Filterable, sortable table and the filter syntax (`key:value`, `~regex`).

```sh
task example NAME=table
```

??? example "table.yaml"

    ```yaml
    --8<-- "examples/table.yaml"
    ```

### table_columns

Column sizing (fixed, auto, flex, `max_width`) and alignment.

```sh
task example NAME=table_columns
```

??? example "table_columns.yaml"

    ```yaml
    --8<-- "examples/table_columns.yaml"
    ```

### table_styled

Colored cells, clickable hyperlinks and `initial_sort`.

```sh
task example NAME=table_styled
```

??? example "table_styled.yaml"

    ```yaml
    --8<-- "examples/table_styled.yaml"
    ```

### table_wide

A wide table with horizontal scroll (`←`/`→`, `0`/`$`).

```sh
task example NAME=table_wide
```

??? example "table_wide.yaml"

    ```yaml
    --8<-- "examples/table_wide.yaml"
    ```

### logview

A log pane with `/` search, filter mode and `initial_query`.

```sh
task example NAME=logview
```

??? example "logview.yaml"

    ```yaml
    --8<-- "examples/logview.yaml"
    ```

### textview

Static text with `content:`, plus a source-bound clock; `/` search and `w` wrap.

```sh
task example NAME=textview
```

??? example "textview.yaml"

    ```yaml
    --8<-- "examples/textview.yaml"
    ```

### tree

A static hierarchy: expand, collapse, search, `initial_depth`.

```sh
task example NAME=tree
```

??? example "tree.yaml"

    ```yaml
    --8<-- "examples/tree.yaml"
    ```

### tree_source

A source-bound tree bucketed with `group_by:`, keeping its state across `refresh:`.

```sh
task example NAME=tree_source
```

??? example "tree_source.yaml"

    ```yaml
    --8<-- "examples/tree_source.yaml"
    ```

### inspector

A label/value record viewer with nested groups.

```sh
task example NAME=inspector
```

??? example "inspector.yaml"

    ```yaml
    --8<-- "examples/inspector.yaml"
    ```

### inspector_auto

`auto: true`: an inspector that derives its fields from any JSON response.

```sh
task example NAME=inspector_auto
```

??? example "inspector_auto.yaml"

    ```yaml
    --8<-- "examples/inspector_auto.yaml"
    ```

### marking

Multi-select with `markable:` and `mark_key:`; marks stay on rows that reorder every poll.

```sh
task example NAME=marking
```

??? example "marking.yaml"

    ```yaml
    --8<-- "examples/marking.yaml"
    ```

## Layout, theme and chrome

### layout

Nested `vstack` / `hstack` with mixed flex weights.

```sh
task example NAME=layout
```

??? example "layout.yaml"

    ```yaml
    --8<-- "examples/layout.yaml"
    ```

### themes

Every built-in theme.

```sh
task example NAME=themes
```

??? example "themes.yaml"

    ```yaml
    --8<-- "examples/themes.yaml"
    ```

### chrome

`app.glyphs` and `app.borders`: an ASCII-safe glyph set, rounded panes and slot brackets.

```sh
task example NAME=chrome
```

??? example "chrome.yaml"

    ```yaml
    --8<-- "examples/chrome.yaml"
    ```

### colors

Per-component `colors:` overrides.

```sh
task example NAME=colors
```

??? example "colors.yaml"

    ```yaml
    --8<-- "examples/colors.yaml"
    ```

### demo

A list and a table side by side.

```sh
task example NAME=demo
```

??? example "demo.yaml"

    ```yaml
    --8<-- "examples/demo.yaml"
    ```

## Data sources

### http_countries

A live REST API table with `refresh: 5m` and per-column dot-paths.

```sh
task example NAME=http_countries
```

??? example "http_countries.yaml"

    ```yaml
    --8<-- "examples/http_countries.yaml"
    ```

### http_refresh

Prices polled every 10s; filter, sort and cursor survive each refresh.

```sh
task example NAME=http_refresh
```

??? example "http_refresh.yaml"

    ```yaml
    --8<-- "examples/http_refresh.yaml"
    ```

### http_github

A GitHub drill-down: users → repos → repo detail, templated from the selection.

```sh
task example NAME=http_github
```

??? example "http_github.yaml"

    ```yaml
    --8<-- "examples/http_github.yaml"
    ```

### http_github_auth

Authenticated GitHub with `${env.GITHUB_TOKEN}` in a header. Needs `GITHUB_TOKEN`.

```sh
task example NAME=http_github_auth
```

??? example "http_github_auth.yaml"

    ```yaml
    --8<-- "examples/http_github_auth.yaml"
    ```

### exec_local

An `exec` source: `git log` as a table.

```sh
task example NAME=exec_local
```

??? example "exec_local.yaml"

    ```yaml
    --8<-- "examples/exec_local.yaml"
    ```

### file_fixture

A `file` source re-read every 5s; edit the file and watch the table change.

```sh
task example NAME=file_fixture
```

??? example "file_fixture.yaml"

    ```yaml
    --8<-- "examples/file_fixture.yaml"
    ```

### merge_sources

A `merge` of a file and two commands, tagged by source.

```sh
task example NAME=merge_sources
```

??? example "merge_sources.yaml"

    ```yaml
    --8<-- "examples/merge_sources.yaml"
    ```

### params_demo

A source with `parameters:`, bound with `wrangl --param` or a push.

```sh
task example NAME=params_demo
```

??? example "params_demo.yaml"

    ```yaml
    --8<-- "examples/params_demo.yaml"
    ```

## Paging

### http_paginated

`paginate:` walks every page up front into one list.

```sh
task example NAME=http_paginated
```

??? example "http_paginated.yaml"

    ```yaml
    --8<-- "examples/http_paginated.yaml"
    ```

### http_window

`window:` over http: the server pages and filters across millions of rows while the table holds a hundred.

```sh
task example NAME=http_window
```

??? example "http_window.yaml"

    ```yaml
    --8<-- "examples/http_window.yaml"
    ```

### exec_window

`window:` over exec: `git log --skip/--max-count/--author/--grep` answers the table's paging and filtering.

```sh
task example NAME=exec_window
```

??? example "exec_window.yaml"

    ```yaml
    --8<-- "examples/exec_window.yaml"
    ```

## Streaming

### stream_exec

`exec` with `follow: true` streaming into a logview.

```sh
task example NAME=stream_exec
```

??? example "stream_exec.yaml"

    ```yaml
    --8<-- "examples/stream_exec.yaml"
    ```

### stream_websocket

A `websocket` source streaming into a logview.

```sh
task example NAME=stream_websocket
```

??? example "stream_websocket.yaml"

    ```yaml
    --8<-- "examples/stream_websocket.yaml"
    ```

### stream_trades_table

A websocket feeding a live table as a ring buffer (`max_rows: 100`).

```sh
task example NAME=stream_trades_table
```

??? example "stream_trades_table.yaml"

    ```yaml
    --8<-- "examples/stream_trades_table.yaml"
    ```

### stream_l1

Two streams merged by symbol into one keyed table with `row_key:`.

```sh
task example NAME=stream_l1
```

??? example "stream_l1.yaml"

    ```yaml
    --8<-- "examples/stream_l1.yaml"
    ```

## Pipelines

### filter_demo

The operator showroom: filter, project, derive, sort, union, join and pipeline parameters.

```sh
task example NAME=filter_demo
```

??? example "filter_demo.yaml"

    ```yaml
    --8<-- "examples/filter_demo.yaml"
    ```

### inline_pipe_demo

Inline `pipe:` chains on a source and on a pipeline.

```sh
task example NAME=inline_pipe_demo
```

??? example "inline_pipe_demo.yaml"

    ```yaml
    --8<-- "examples/inline_pipe_demo.yaml"
    ```

## Screens, prompts and actions

### multi

Multi-screen drill-down with breadcrumbs and `${selection}`.

```sh
task example NAME=multi
```

??? example "multi.yaml"

    ```yaml
    --8<-- "examples/multi.yaml"
    ```

### push_actions

Push actions: `enter` drills into repos, a keyless action opens starred repos from the menu.

```sh
task example NAME=push_actions
```

??? example "push_actions.yaml"

    ```yaml
    --8<-- "examples/push_actions.yaml"
    ```

### on_cursor

`on_cursor:`: a repos table drives a detail inspector below it.

```sh
task example NAME=on_cursor
```

??? example "on_cursor.yaml"

    ```yaml
    --8<-- "examples/on_cursor.yaml"
    ```

### on_cursor_fs

`on_cursor:` from a filesystem tree; `${cursor.path}` feeds `stat`.

```sh
task example NAME=on_cursor_fs
```

??? example "on_cursor_fs.yaml"

    ```yaml
    --8<-- "examples/on_cursor_fs.yaml"
    ```

### prompts_boot

`app.prompts`: a boot form whose answers feed the URL, title and a column.

```sh
task example NAME=prompts_boot
```

??? example "prompts_boot.yaml"

    ```yaml
    --8<-- "examples/prompts_boot.yaml"
    ```

### action_prompts

The action menu, and inputs collected in a generated form. Runs anywhere.

```sh
task example NAME=action_prompts
```

??? example "action_prompts.yaml"

    ```yaml
    --8<-- "examples/action_prompts.yaml"
    ```

### action_http_argocd

`type: http` actions against Argo CD: bearer auth, `success:` and `error_message: ${body.message}`.

```sh
task example NAME=action_http_argocd
```

??? example "action_http_argocd.yaml"

    ```yaml
    --8<-- "examples/action_http_argocd.yaml"
    ```

## Kubernetes

### kube

Namespaces → pods → pod detail and a tailing log. Needs `kubectl proxy --port=8001`.

```sh
task example NAME=kube
```

??? example "kube.yaml"

    ```yaml
    --8<-- "examples/kube.yaml"
    ```

### kube2

Contexts from `kubectl config view` as the entry point. Needs `kubectl`.

```sh
task example NAME=kube2
```

??? example "kube2.yaml"

    ```yaml
    --8<-- "examples/kube2.yaml"
    ```

### kube_multi

Three clusters merged into one pods table with cross-cluster drill-down.

```sh
task example NAME=kube_multi
```

??? example "kube_multi.yaml"

    ```yaml
    --8<-- "examples/kube_multi.yaml"
    ```
