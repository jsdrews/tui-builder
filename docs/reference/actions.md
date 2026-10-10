# Actions

Actions are the write side: named units of work in the top-level
`actions:` map. They are kept out of `data.sources:` on purpose, since
sources are re-fetched on a timer, on `r` and on screen re-entry, and a
re-fetch must never re-run a mutation.

The work is split in two:

- An **action** says what to run, what typed inputs it needs, and how to
  tell success from failure. It never reads a selection, so it can be
  reused on any screen, and `wrangl --list-actions` can describe it.
- A **binding**, on a screen, says which key fires it, which component's
  focused row fills its inputs, and whether to confirm first.

```yaml
actions:
  restart:
    description: Restart a deployment
    run: [kubectl, rollout, restart, "deployment/${inputs.name}", -n, "${inputs.ns}"]
    inputs:
      name: {required: true}
      ns:   {default: default}
    message: "restarted ${inputs.name}"

tui:
  screen:
    layout: {component: deployments}
    actions:
      - key: R
        action: restart
        from: deployments
        bind: {name: "${selection.Name}", ns: "${selection.Namespace}"}
        confirm: "Restart ${selection.Name}?"
```

Every action a screen binds appears in the action menu (`a`, see
`app.actions_key`), with its shortcut and, when it can't run, the
reason. `enter` (or a double click) fires its binding directly.

## Action fields

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | `exec`, or `push` when `push:` is set | — | `exec`, `http` or `push`. |
| `description` | string | — | — | One line, shown by `wrangl --list-actions`. |
| `inputs` | map | — | exec, http | Typed slots referenced as `${inputs.NAME}`. Filled from the binding's `bind:`, else collected in a generated form, else `default`. See [inputs](#inputs). |
| `run` | list of template | — | required on exec | The argv. Not run through a shell: use `[sh, -c, "…"]` explicitly, so the quoting is visible. `${inputs.*}` and `${env.*}` substitute; other `${…}` (such as a shell's `${PAGER:-less}`) is left as written. |
| `method` | template | `POST` | http | HTTP method. |
| `url` | template | — | required on http | The endpoint. |
| `headers` | map of template | — | http | Request headers. |
| `body` | template | — | http | Request body, sent as written after substitution. |
| `timeout` | duration | `30s` | http | Request limit. |
| `push` | string | — | push | Opens this screen from `tui.screens`. See [push actions](#push-actions). |
| `success` | expr | exec: `code == 0`; http: `code < 400` | exec, http | Decides whether the run worked, over `code` (exit or HTTP status), `output` (stdout or response body) and `body` (the parsed JSON body). For tools that exit non-zero on success, or an API's harmless 409: `code < 400 or code == 409`. |
| `message` | template | `action complete` | exec, http | Summary on success: shown in the statusbar and heading the console entry. Takes `${inputs.*}`, `${env.*}`, `${code}`, `${output}` and `${body.PATH}` (a field of a JSON response). |
| `error_message` | template | last stderr line (exec), status code (http) | exec, http | Summary on failure; same tokens. For JSON APIs, `${body.message}` (or wherever the API puts its reason) beats a bare status code. |
| `multi` | bool | `false` | exec, http | Runs over every marked row: one run per row, each tracked, cancellable and logged on its own. With `false`, the action is shown disabled while several rows are marked, rather than picking one. |
| `exclusive` | bool | `true` on http, `false` otherwise | — | Refuses a second run against a target it's already running on; the menu shows why until the first finishes. Other targets are unaffected. |

**Validation:**

- exec needs `run:` and takes no `url`, `method`, `headers` or `body`.
- http needs `url:` and takes no `run:`.
- push needs `push:` and takes no `run:`, `url:`, `inputs:` or
  `multi: true`.
- Inputs follow the [input rules](#inputs).

```yaml
actions:
  sync_app:
    type: http
    url: ${env.ARGOCD_URL}/api/v1/applications/${inputs.app}/sync
    headers: {Authorization: "Bearer ${env.ARGOCD_TOKEN}"}
    inputs: {app: {required: true}}
    success: code < 400 or code == 409
    error_message: ${body.message}
```

## Inputs

An input is a typed slot an action needs filled. The same field
vocabulary describes [boot prompts](app.md#appprompts). Any input the
binding doesn't fill is collected in a form generated from these fields,
so every input is fillable one way or another.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | `string` | — | The data type: `string`, `int`, `bool` or `duration`. It also picks the form widget: `bool` is a yes/no toggle, anything else a text input, unless `options:` makes it a select. |
| `required` | bool | `false` | not with `default` | Must have a value before the action runs. |
| `default` | string | — | not with `required` | The value when nothing else supplies one, and the form field's starting value. For `bool`, `"true"` / `"false"`; with `options:`, one of the options. |
| `description` | string | — | — | Shown by `wrangl --list-actions`. |
| `label` | string | the input name | — | The form field's caption. |
| `placeholder` | string | — | text inputs | Hint inside an empty text input. |
| `options` | list of string | — | not with `mask` | Turns the field into a select over these values. |
| `mask` | bool | `false` | not with `options` | Shows typed characters as bullets. Display only: the real value is used everywhere else. |
| `order` | int | `0` | — | Field order in the form; ties break alphabetically. |

**Validation:** `type` is one of the four; `required` and `default` are
mutually exclusive; `mask` and `options` conflict.

## Bindings

A screen's `actions:` list binds actions to that screen. A binding names
a registry action with `action:`, or declares one inline with `name:`
plus the action fields; inline declarations are moved into the top-level
map at load, so the two forms behave identically.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `action` | string | — | not with an inline declaration | The registry action this binding fires. |
| `name` | string | — | required for an inline declaration | The registry name the inline action gets. |
| `key` | string | — (menu only) | — | `enter` fires directly (a double click is its mouse form). Any other key is a shortcut inside the action menu. Omit it to list the action in the menu only. |
| `label` | string | action name; a push: `open <screen>` | — | The action's name in the menu and help. |
| `from` | string | — | see validation | The list, table or tree whose focused row supplies `${selection.*}` (or whose marked rows a `multi` action runs over). |
| `bind` | map of template | — | — | Fills the action's `inputs:` (or, on a push, the destination's source `parameters:`) from `${selection.*}` and `${env.*}`. |
| `confirm` | template | — | — | Shows a yes/no dialog before running. Takes `${selection.*}` and `${inputs.*}` (resolved values, after the form). |
| `interactive` | bool | `false` | exec | Hands the terminal to the process (vim, ssh, `kubectl exec`) instead of capturing its output into the console. |
| `notice` | template | — | `interactive` | Printed once after the TUI suspends, before the process starts. |

**Validation:**

- A `key` can't be reserved (below), can't be `app.actions_key`,
  `app.output_key` or `app.theme_key`, and can't repeat on one screen.
- `from:` is required when `bind:` or `confirm:` reads `${selection…}`,
  or the action is a push, and forbidden otherwise. It must be a list,
  table or tree placed on this screen.
- Every `bind:` key is a declared input (except on a push).
- `interactive: true` isn't allowed on http actions or with `multi`.

### Reserved keys

| Key | Owned by |
|---|---|
| `q`, `ctrl+c` | quits the app |
| `ctrl+z` | suspends the app |
| `?` | opens the key overlay |
| `esc` | pops the current screen |
| `tab`, `shift+tab` | cycles focus |
| `r` | refreshes every bound source |

`j`, `k`, `/` and `enter` are not reserved: the first three belong to the
focused component only while it has focus, and `enter` is the direct
activation key.

```yaml
actions:
  - key: enter
    action: open_detail
    from: pods_table
    bind: {name: "${selection.Name}"}
  - action: delete_pod          # menu only
    from: pods_table
    bind: {name: "${selection.Name}"}
    confirm: "Delete ${selection.Name}?"
  - key: n
    name: new_namespace         # inline declaration
    run: [kubectl, create, namespace, "${inputs.ns}"]
    inputs: {ns: {required: true}}
```

## Push actions

A push opens another screen from `tui.screens`, carrying the focused row
along. It's navigation, so it has no inputs, output or exclusivity.

On the destination screen, the focused row of the binding's `from:`
component is available as `${selection…}` in the screen title and in its
sources' templated fields. The binding's `bind:` fills the destination's
sources' `parameters:`; a required parameter left unbound is reported
when the push happens.

```yaml
actions:
  open_pod:
    push: pod
tui:
  screens:
    pods:
      layout: {component: pods_table}
      actions:
        - key: enter
          action: open_pod
          from: pods_table
          bind: {namespace: "${selection.Namespace}", name: "${selection.Name}"}
    pod:
      title: "${selection.Name}"
      layout: {component: pod_detail}   # source declares namespace, name
```
