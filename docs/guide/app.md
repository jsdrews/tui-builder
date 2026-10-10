# App, screens and layout

A config is one YAML file with up to four blocks:

| Block | Holds | Read by |
|---|---|---|
| `app:` | Title, theme, global keys, boot prompts, required env vars | both binaries |
| `data:` | Sources and pipelines | both binaries |
| `tui:` | Components and screens | `tui-builder` |
| `actions:` | Units of work the user can trigger | `tui-builder` (and `wrangl --list-actions`) |

Every field is listed in [Reference → App, screens and layout](../reference/app.md).

## The app shell

`app:` configures what surrounds every screen: the breadcrumb title, the
starting `theme`, and the keys the shell claims for itself.

```yaml
app:
  title: Ops
  version: v1.4
  theme: tokyo-night
```

The shell always provides:

| Key | Does |
|---|---|
| `?` | Opens the key overlay for the focused component, searchable |
| `a` | Opens the action menu (`app.actions_key`) |
| `o` | Opens the output console (`app.output_key`) |
| `t` | Cycles the theme (`app.theme_key`) |
| `tab` / `shift+tab` | Moves focus between components |
| `r` | Refetches every source on the screen |
| `esc` | Goes back one screen |
| `q` | Quits |

Set any of the three configurable keys to `-` to turn that feature off.
`glyphs:` and `borders:` change the marks and border shapes every
palette draws with; see [`examples/chrome.yaml`](../examples.md#chrome).

### Ask at startup

`app.prompts` shows a form before the first screen. Each answer becomes
an environment variable, so the rest of the config reads it as
`${env.KEY}`. A variable that's already set pre-fills its field, so the
form can be skipped from the shell.

```yaml
app:
  prompts:
    - {key: GH_USER, label: GitHub user, required: true}
    - {key: GH_TOKEN, label: Token, mask: true}
data:
  sources:
    repos:
      type: http
      url: https://api.github.com/users/${env.GH_USER}/repos
      headers: {Authorization: "Bearer ${env.GH_TOKEN}"}
```

### Fail early on missing env vars

`app.env` declares the variables a config needs. A missing required one
stops the load with one message listing every missing name and its
description, instead of a confusing 401 later.

```yaml
app:
  env:
    - {name: AWX_HOST, required: true, description: "Tower base URL"}
    - {name: AWX_TOKEN, required: true, description: "Users → Tokens"}
```

## One screen or several

`tui.screen:` is a single screen. For drill-downs, use `tui.screens:`, a
map of named screens, with `tui.initial:` naming the first one. Other
screens are opened by [push actions](actions.md#drill-down-with-push),
which carry the selected row along; `esc` goes back.

```yaml
tui:
  initial: users
  screens:
    users:
      title: Users
      layout: {component: users_list}
      actions:
        - {key: enter, name: open_repos, push: repos, from: users_list}
    repos:
      title: "${selection}'s repos"
      layout: {component: repos_table}
```

## Layout

A layout is a tree. Each node is one of:

- `component:` a component from `tui.components`,
- `vstack:` children top to bottom,
- `hstack:` children side by side,
- `zstack:` an `overlay` node drawn over a `base` node.

Stack children size with `flex:` (a share of the remaining space, default
1) or `fixed:` (exact rows or columns).

```yaml
layout:
  vstack:
    - fixed: 8
      component: summary
    - hstack:
        - {flex: 2, component: pods}
        - {flex: 1, component: logs}
```

```
┌ summary ────────────────────────────┐
│                                     │
└─────────────────────────────────────┘
┌ pods ──────────────────┐┌ logs ─────┐
│                        ││           │
│                        ││           │
└────────────────────────┘└───────────┘
```

A component can appear once per screen; the same component can be placed
on several screens.
