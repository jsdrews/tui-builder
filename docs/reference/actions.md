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
| `url` | template | — | required on http | The endpoint. Tokens are escaped by where they sit, as in a [source url](sources.md#http): path-escaped (keeping `/`, unless the input is `segment: true`) or query-escaped. |
| `headers` | map of template | — | http | Request headers. |
| `body` | template | — | http; one of `body` / `json` / `form` | Request body, sent as written after substitution. `Content-Type: application/json` unless `headers:` sets one. |
| `json` | any | — | http; one of `body` / `json` / `form` | Request body as any YAML value, encoded as JSON. Strings are templated before encoding, so a quote in an input can't break it. A string that is exactly one token, `"${inputs.count}"`, takes the input's type (a number for `int`, a boolean for `bool`), and drops its key when it resolves empty, so blank optional inputs are left out. |
| `form` | map of template or list | — | http; one of `body` / `json` / `form` | Request body as form fields, `application/x-www-form-urlencoded`. A list repeats the key; empty values are dropped. |
| `query` | map of template or list | — | http | Query parameters laid over the url's, templated and escaped. A list repeats the key; empty values are dropped. |
| `auth` | map | — | http | Authenticates the request, as on a source: one of `bearer`, `basic`, `api_key`, `oauth2`. See [auth](sources.md#auth). Its secrets are masked as `•••` in console entries, error messages and `wrangl` dry runs. oauth2 tokens are shared with sources that have the same auth. |
| `extends` | string | — | http (implies it) | Takes the request from a [preset](sources.md#presets): see [extending presets](#extending-presets). |
| `unset` | list of string | — | with `extends` | Inherited fields to drop: `url`, `headers`, `query`, `auth`, `timeout`. |
| `timeout` | duration | `30s` | http | Request limit. |
| `push` | string | — | push | Opens this screen from `tui.screens`. See [push actions](#push-actions). |
| `success` | expr | exec: `code == 0`; http: `code < 400` | exec, http | Decides whether the run worked, over `code` (exit or HTTP status), `output` (stdout or response body) and `body` (the parsed JSON body). For tools that exit non-zero on success, or an API's harmless 409: `code < 400 or code == 409`. |
| `message` | template | `action complete` | exec, http | Summary on success: shown in the statusbar and heading the console entry. Takes `${inputs.*}`, `${env.*}`, `${code}`, `${output}` and `${body.PATH}` (a field of a JSON response). |
| `error_message` | template | last stderr line (exec), status code (http) | exec, http | Summary on failure; same tokens. For JSON APIs, `${body.message}` (or wherever the API puts its reason) beats a bare status code. |
| `multi` | bool | `false` | exec, http | Runs over every marked row: one run per row, each tracked, cancellable and logged on its own. With `false`, the action is shown disabled while several rows are marked, rather than picking one. |
| `exclusive` | bool | `true` on http, `false` otherwise | — | Refuses a second run against a target it's already running on; the menu shows why until the first finishes. Other targets are unaffected. |
| `claim` | string or map | — | exec, http; not push or interactive | Makes the rows this action targets spin from the moment it starts, on a component with `activity:`. See [claims](#claims). |

**Validation:**

- exec needs `run:` and takes no `url`, `method`, `headers`, `body`,
  `json` or `form`.
- http needs `url:` (or one from its preset) and takes no `run:`;
  `json`, `form` and `body` are mutually exclusive. `auth:` follows the
  source rules, including that `headers:` can't also set the header it
  owns.
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

A PATCH that sends only what was filled in, and clears a field with a
literal (a literal `""` or `null` isn't a token, so it's kept):

```yaml
actions:
  edit_item:
    type: http
    method: PATCH
    url: ${env.API_URL}/items/${inputs.id}
    inputs:
      id:    {required: true, segment: true}
      name:  {}
      count: {type: int}
    json:
      name: ${inputs.name}       # left out when blank
      count: ${inputs.count}     # a number
      note: null                 # always sent, clearing the note
```

## Extending presets

An http action can `extends:` the same [preset](sources.md#presets) its
API's sources do, so reads and writes share one base url and one auth.

```yaml
data:
  presets:
    api:
      type: http
      url: ${env.API_URL}/v1
      auth: {bearer: "${env.API_TOKEN}"}
  sources:
    items: {extends: api, url: /items, root: entries}

actions:
  delete_item:
    extends: api
    method: DELETE
    url: /items/${inputs.id}       # joins onto the preset's url
    inputs: {id: {required: true, segment: true}}
```

**Inherited:** `url` (a relative action url joins onto it, as a
source's does), `headers` and `query` (merged key by key, the action's
winning; `""` drops one), `auth` and `timeout` (the action's own, else
the preset's, else 30s).

**Ignored:** every other preset field — `format`, `root`, `paginate`,
`window`, `refresh`, `retry`, `max_body`, `conditional` and the rest
belong to sources. In particular actions never retry: a POST or DELETE
isn't safe to send twice. `method` stays POST unless the action says
otherwise.

A preset whose inherited fields use `${params.*}` can't be extended by
an action, which fills `${inputs.*}` instead — declare the value in the
action's own url or headers.

Every http action sends `User-Agent: tui-builder/<version>` unless
`headers:` sets one.

## Claims

Without a claim, a row on an [`activity:`](components.md#activity)
component starts spinning only when the next poll reports it busy. A
claim covers that gap: the targeted rows spin as soon as the action
starts (after any confirm), labelled with the claim, until the data can
take over.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `label` | string | — | required | Shown beside the spinner. Use the server's own word for the work (`Running`), so the row reads the same from the keypress to the poll that confirms it. `claim: Restarting` is shorthand for `{label: Restarting}`. |
| `until` | string | `done` | — | Who knows when the work ends. `done`: the action does; the claim ends when it returns (a command that blocks until it's finished). `status`: the server does; the action only acknowledges (an HTTP 202), and the claim hands over to the polled status. |
| `settle` | int | `1` | `until: status` | How many reads may repeat the status from before the action before the claim gives up, for a server that reports new work late. Each claim has its own, so a slow verb's allowance doesn't hold up a fast one on the same component. |

- **Targets** are the binding's selection: the marked rows, or the cursor
  row, of its `from:` component, which must have `activity:`.
- **Fan-out** (`multi: true`) makes one claim per row, so a row whose run
  fails stops on its own.
- **A failed run** (non-zero exit, HTTP error) withdraws the claim at once;
  the error is reported as usual.
- **Finished between polls.** When an `until: status` claim ends without
  any read seeing the row busy, the console says so (`sync web: no change
  observed`, or `finished between polls` when the status moved), so the
  action doesn't look ignored.
- **Success message.** Without `message:`, a claiming action reports
  "<label> requested" for `until: status` (the work has only started)
  and "<label> completed" for `until: done`.

```yaml
actions:
  restart:
    run: [kubectl, rollout, restart, "deploy/${inputs.name}", --wait]
    inputs: {name: {required: true}}
    claim: Restarting                 # until: done
  sync:
    type: http
    url: ${env.ARGOCD_URL}/api/v1/applications/${inputs.app}/sync
    inputs: {app: {required: true}}
    claim: {label: Running, until: status, settle: 2}
```

**Validation:** `label` is required; `until` is `done` or `status`;
`settle` only with `until: status`. A claiming action can't be a push,
and a binding that fires one needs `from:` naming a component with
`activity:`, and can't be `interactive`.

## Inputs

An input is a typed slot an action needs filled. The same field
vocabulary describes [boot prompts](app.md#appprompts). Any input the
binding doesn't fill is collected in a form generated from these fields,
so every input is fillable one way or another.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | `string` | — | The data type: `string`, `int`, `bool` or `duration`. It also picks the form widget: `bool` is a yes/no toggle, anything else a text input, unless `options:` makes it a select. |
| `required` | bool | `false` | not with `default` | Must have a value before the action runs. On a select, the form starts with nothing highlighted, so the answer is picked on purpose. |
| `default` | string | — | not with `required` | The value when nothing else supplies one, and the form field's starting value. For `bool`, `"true"` / `"false"`; with `options:`, one of the options. |
| `description` | string | — | — | Shown by `wrangl --list-actions`. |
| `segment` | bool | `false` | http | The value is one URL path segment: in the `url:` path it's fully escaped, `/` included. An OpenAPI `in: path` parameter. |
| `label` | string | the input name | — | The form field's caption. |
| `placeholder` | string | — | text inputs | Hint inside an empty text input. |
| `options` | list of string | — | not with `mask` | Turns the field into a select over these values. |
| `mask` | bool | `false` | not with `options` | Shows typed characters as bullets. Display only: the real value is used everywhere else. |
| `order` | int | `0` | — | Field order in the form; ties break alphabetically. |
| `validate` | expr | — | — | An [expression](templating.md#expressions) over `value` (the answer: a string, or a bool for a yes/no field), true when it's acceptable. The form won't submit until it is: `value matches '^[a-z0-9-]+$'`, `int(value) <= 10`. An empty optional answer isn't checked. |
| `invalid` | string | `not accepted` | `validate` | The message shown on the field when `validate` is false. |

```yaml
inputs:
  name:
    validate: "value matches '^[a-z0-9-]+$'"
    invalid: lowercase letters, digits and dashes only
  replicas: {type: int, validate: "int(value) <= 10", invalid: at most 10}
  region: {options: [eu, us], required: true}
```

**Validation:** `type` is one of the four; `required` and `default` are
mutually exclusive; `mask` and `options` conflict; `validate` must
compile, and `invalid` needs it. `validate` / `invalid` are errors on a
source's `parameters:`, which no form collects.

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
| `from` | string | — | see validation | The list, table, tree or eventlog whose focused row supplies `${selection.*}` (or whose marked rows a `multi` action runs over). |
| `bind` | map of template | — | — | Fills the action's `inputs:` (or, on a push, the destination's source `parameters:`) from `${selection.*}` and `${env.*}`. |
| `confirm` | template or map | — | — | Shows a yes/no dialog before running. Takes `${selection.*}` and `${inputs.*}` (resolved values, after the form). The map form, `{message: …, default: yes}`, starts the dialog on Yes; the default is `no` (Cancel). See [`confirm`](#confirm). |
| `interactive` | bool | `false` | exec | Hands the terminal to the process (vim, ssh, `kubectl exec`) instead of capturing its output into the console. |
| `notice` | template | — | `interactive` | Printed once after the TUI suspends, before the process starts. |

**Validation:**

- A `key` can't be reserved or one of the menu's own keys (below), can't be
  any shell key as configured (`app.quit_key`, `app.suspend_key`,
  `app.help_key`, `app.actions_key`, `app.output_key`, `app.theme_key`),
  and can't repeat on one screen.
- `from:` is required when `bind:` or `confirm:` reads `${selection…}`,
  or the action is a push, and forbidden otherwise. It must be a list,
  table, tree or eventlog placed on this screen.
- Every `bind:` key is a declared input (except on a push).
- `interactive: true` isn't allowed on http actions or with `multi`.

### `confirm`

A string is the message. The map form also picks the button the dialog
starts on.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `message` | template | — | required | The question, as in the string form. |
| `default` | string | `no` | — | `no` starts on Cancel, the safe side for a destructive action; `yes` starts on Yes. |

```yaml
- key: R
  action: restart
  from: pods
  confirm: {message: "Restart ${selection}?", default: yes}
```

`default: yes` applies when the action runs from `enter`, after its
input form, or over marked rows. A single-row action picked from the
action menu is confirmed by the app shell's own dialog, which always
starts on Cancel.

**Validation:** `default` is `yes` or `no`, and the map form needs a
`message`.

### Reserved keys

| Key | Owned by |
|---|---|
| `q` (`app.quit_key`), `ctrl+c` | quits the app |
| `ctrl+z` (`app.suspend_key`) | suspends the app |
| `?` (`app.help_key`) | opens the key overlay |
| `esc` | pops the current screen |
| `tab`, `shift+tab` | cycles focus |
| `r` | refreshes every bound source |

Every key except `enter` is a shortcut inside the action menu, so the
menu's own keys are reserved too. The menu handles them first, so a
shortcut on one would never fire:

| Key | Inside the menu |
|---|---|
| `up`, `k`, `down`, `j` | moves the cursor |
| `g`, `G` | jumps to the first or last action |
| `ctrl+u`, `ctrl+d` | scrolls half a page |

`/` and `enter` are not reserved: `/` belongs to the focused component
only while it has focus, and `enter` is the direct activation key.

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
sources' `parameters:`; a required parameter with no argument is reported
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
