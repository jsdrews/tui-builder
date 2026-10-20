# Actions

Actions are how a user changes things. They live in a top-level
`actions:` map, separate from `data:`, because sources are re-fetched all
the time (on a timer, on `r`, on returning to a screen) and a re-fetch
must never repeat a delete.

Each action is split in two:

- The **action** says what to run and what inputs it needs. It never
  refers to the screen, so it's reusable anywhere.
- A **binding** on a screen says which key, which component's row fills
  the inputs, and whether to ask first.

```yaml
actions:
  pod_delete:
    description: Delete a pod
    inputs:
      namespace: {required: true}
      name: {required: true}
    run: [kubectl, delete, -n, "${inputs.namespace}", pod, "${inputs.name}"]
    message: "deleted ${inputs.name}"

tui:
  screens:
    pods:
      layout: {component: pods_table}
      actions:
        - key: D
          action: pod_delete
          from: pods_table
          bind:
            namespace: ${selection.Namespace}
            name: ${selection.Name}
          confirm: "Delete ${selection.Name}?"
```

Every field is in [Reference → Actions](../reference/actions.md).

## The action menu

`a` opens a menu of every action the screen has. An action that can't
run right now (because several rows are marked and it isn't `multi`, or
because it's already running on this row) is shown with the reason.

- `key: enter` fires straight from the screen. A double click does too.
- Any other `key:` is a shortcut inside the menu.
- No `key:` at all is fine: the action is in the menu.

## Kinds

| Kind | Runs | Success by default |
|---|---|---|
| `exec` (default) | `run:`, an argv, with no shell unless you write `[sh, -c, …]` | exit code 0 |
| `http` | a request to `url:` | status below 400 |
| `push` | opens another screen | — |

`success:` overrides the verdict for tools and APIs that report oddly,
for example `code < 400 or code == 409` for an API that answers "already
running" with 409. `message:` and `error_message:` set the summary; for
JSON APIs, `error_message: ${body.message}` shows the API's own reason.

## Inputs and forms

Inputs are typed slots. The binding's `bind:` fills what it can from the
focused row; anything left over is asked for in a form generated from the
input declarations: `type: bool` is a toggle, `options:` a select, and
anything else a text field.

```yaml
actions:
  scale:
    run: [kubectl, scale, "deploy/${inputs.name}", "--replicas=${inputs.replicas}"]
    inputs:
      name: {required: true}
      replicas: {type: int, default: "3"}
```

Bound with only `name` from the row, this asks for `replicas`, starting at
3.

## Calling an API

An `http` action sends a request. Give it the same
[preset](../reference/sources.md#presets) as the API's sources and it
takes the base url, headers, query, auth and timeout from there, so a
write and the read beside it can't drift apart. A body written as
`json:` is templated before it's encoded: a quote in an input can't
break it, `"${inputs.count}"` is sent as a number for an `int` input,
and a blank optional input is left out.

```yaml
data:
  presets:
    api:
      type: http
      url: ${env.API_URL}/v1
      auth: {bearer: "${env.API_TOKEN}"}

actions:
  rename:
    extends: api
    method: PATCH
    url: /items/${inputs.id}         # → ${env.API_URL}/v1/items/…
    inputs:
      id:   {required: true, segment: true}
      name: {required: true}
    json: {name: "${inputs.name}"}
```

`segment: true` keeps an id one path segment: `a/b` is sent as `a%2Fb`.
The token is masked as `•••` in anything the action reports. Actions
never retry. See [Reference → extending presets](../reference/actions.md#extending-presets)
and [`examples/http_pages.yaml`](../examples.md#http_pages).

## Where results go

Every run reports to the output console (`o`): its output streams in as
it happens, the summary shows in the statusbar, and a badge counts unread
results and turns red after a failure. Nothing pops up a blocking error.

Actions don't refresh views. A view that should reflect a change polls
(`refresh:`), and `r` refetches on demand.

## Several rows at once

With `multi: true`, an action runs once per marked row, each run tracked,
logged and cancellable on its own. Without it, the action is disabled
while several rows are marked rather than picking one.

`exclusive:` (on by default for http) refuses a second run against a row
that's already being worked on.

## Claim the rows you act on

On a component with [`activity:`](components.md#rows-that-are-being-worked-on),
a row only spins once a poll reports it busy. A `claim:` makes the rows an
action targets spin as soon as it starts:

```yaml
actions:
  restart:
    run: [kubectl, rollout, restart, "deploy/${inputs.name}", --wait]
    inputs: {name: {required: true}}
    claim: Restarting                          # spins until the command returns
  sync:
    type: http
    url: ${env.ARGOCD_URL}/api/v1/applications/${inputs.app}/sync
    inputs: {app: {required: true}}
    claim: {label: Running, until: status}    # the API answers 202; the poll takes over
```

A failed run stops its rows at once, and fan-out claims each row on its
own. See [Reference → Claims](../reference/actions.md#claims).

## Interactive commands

`interactive: true` on a binding hands the terminal to the command
(`vim`, `ssh`, `kubectl exec -it`) and returns to the TUI when it exits.

## Drill down with push

A push action opens another screen. The focused row of `from:` is
available there as `${selection…}`, and `bind:` fills the destination's
source parameters:

```yaml
actions:
  open_pod: {push: pod}
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
      title: ${selection.Name}
      layout: {component: pod_detail}
```

## From the command line

`wrangl` can list actions and show exactly what one would run, without a
terminal UI. There's deliberately no way to run one from `wrangl`: the
safety gates (confirm, exclusivity, the form) are part of the TUI.

```sh
wrangl --list-actions ops.yaml
wrangl --describe-action ops.yaml pod_delete --param namespace=default --param name=web-0
```
