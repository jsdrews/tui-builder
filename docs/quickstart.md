# Quick start

## Install

tui-builder needs Go 1.25 or later.

```sh
go install github.com/jsdrews/tui-builder/cmd/tui-builder@latest
go install github.com/jsdrews/tui-builder/cmd/wrangl@latest
```

Or build from a clone, which also gives you the examples:

```sh
git clone https://github.com/jsdrews/tui-builder.git
cd tui-builder
task build        # → bin/tui-builder, bin/wrangl, bin/example-launcher
task examples     # browse and run every example
```

## A first config

Save this as `hello.yaml`:

```yaml
app:
  title: Cities
  theme: nord

tui:
  components:
    cities:
      type: table
      filterable: true
      columns:
        - {title: City,   sortable: true}
        - {title: Region, sortable: true}
        - {title: Pop,    sortable: true, sort: si, align: right}
      rows:
        - [London,    Europe, "9M"]
        - [Tokyo,     Asia,   "37M"]
        - [Reykjavík, Europe, "130K"]
  screen:
    layout:
      component: cities
```

Run it:

```sh
tui-builder hello.yaml
```

Try `/` to filter (`region:europe` scopes to a column), `]` to sort,
`?` for every key, and `q` to quit. The mouse works too: click to focus
and select, scroll with the wheel.

## Live data

Replace the static rows with a source. Components bind to sources by
name, and each column plucks its value from the item with a dot-path.
Save this as `users.yaml`:

```yaml
app:
  title: Users

data:
  sources:
    users:
      type: http
      url: https://jsonplaceholder.typicode.com/users
      refresh: 1m

tui:
  components:
    users:
      type: table
      source: users
      filterable: true
      columns:
        - {title: Name,    value: name,         sortable: true}
        - {title: City,    value: address.city, sortable: true}
        - {title: Company, value: company.name}
        - {title: Website, value: website}
  screen:
    layout:
      component: users
```

`refresh: 1m` polls; cursor, filter and sort survive each refresh, and
`r` refetches now.

The same config works without the TUI. `wrangl` runs only the `data:`
block:

```sh
wrangl --list users.yaml                         # every source in the config
wrangl users.yaml users | jq '.[0].address'      # one source's data
```

## Reshape it

Add a [pipeline](guide/data.md#pipelines) entry that reads `users` and
keeps only the `.biz` websites, then point the table's `source:` at it:

```yaml
data:
  sources:
    users: {type: http, url: "https://jsonplaceholder.typicode.com/users"}
    biz_users:
      type: filter
      from: users
      where: "hasSuffix(website, '.biz')"
```

## Add an action

Actions are the write side. Declare what to run and what it needs, then
bind it on a screen and say which row feeds it:

```yaml
actions:
  open_site:
    run: [open, "https://${inputs.site}"]   # xdg-open on Linux
    inputs:
      site: {required: true}

tui:
  screen:
    layout:
      component: users
    actions:
      - key: enter
        action: open_site
        from: users
        bind: {site: "${selection.Website}"}
```

`enter` on a row runs it; `a` opens the action menu listing every action
on the screen. Output and results land in the output console (`o`).

## Next

- The [Guide](guide/app.md) walks through each part of a config.
- The [Reference](reference/README.md) lists every field.
- [Examples](examples.md) has a runnable config for each feature.
