# Quick start

## Install

### Install script (Linux and macOS)

Downloads the latest release, checks its checksum, and puts
`tui-builder` and `wrangl` in `~/.local/bin`. No root needed and no
Go toolchain: the binaries are static, so they run on any Linux
(CentOS 7, Rocky, Alpine, …) on amd64 or arm64.

```sh
curl -fsSL https://raw.githubusercontent.com/jsdrews/tui-builder/main/scripts/install.sh | sh
```

Pin a version or choose another directory with environment variables:

```sh
curl -fsSL https://raw.githubusercontent.com/jsdrews/tui-builder/main/scripts/install.sh \
  | VERSION=v0.5.0 INSTALL_DIR=/usr/local/bin sh
```

### Download a release

Every [release](https://github.com/jsdrews/tui-builder/releases) has an
archive per platform (`linux` / `darwin` × `amd64` / `arm64`, plus
`windows_amd64.zip`) holding both binaries, and a `checksums.txt`.

```sh
VERSION=0.5.0  # without the leading v
curl -fsSLO https://github.com/jsdrews/tui-builder/releases/download/v$VERSION/tui-builder_${VERSION}_linux_amd64.tar.gz
tar -xzf tui-builder_${VERSION}_linux_amd64.tar.gz tui-builder wrangl
sudo install -m 755 tui-builder wrangl /usr/local/bin/
```

On macOS, a browser download is quarantined by Gatekeeper; clear it
with `xattr -d com.apple.quarantine tui-builder wrangl`. The install
script doesn't need this.

### With Go

Needs Go 1.25 or later.

```sh
go install github.com/jsdrews/tui-builder/cmd/tui-builder@latest
go install github.com/jsdrews/tui-builder/cmd/wrangl@latest
```

### From a clone

This also gives you the examples:

```sh
git clone https://github.com/jsdrews/tui-builder.git
cd tui-builder
task build        # → bin/tui-builder, bin/wrangl, bin/example-launcher
task examples     # browse and run every example
```

Check what you have with `tui-builder --version` or `wrangl --version`.

## A first config

Write a starter config in an empty directory:

```sh
mkdir my-project && cd my-project
tui-builder init        # writes tui-builder.yaml
tui-builder tui-builder.yaml
```

It runs offline. Try `/` to filter (`status:running` scopes to a
column), `]` to sort, `?` for every key, and `q` to quit. The mouse
works too: click to focus and select, scroll with the wheel.

This is the file, with `app.title` taken from the directory's name:

```yaml
--8<-- "internal/scaffold/testdata/starter.golden.yaml"
```

It has both layers of every config. The `data:` block says where items
come from; here a `static` source holds them inline. The `tui:` block
says how they're shown: a table whose columns each take a value from an
item by path, laid out on one screen. `wrangl tui-builder.yaml items`
prints the source's data without the UI.

## Live data

Swap the static source for one that fetches: this is the starter's
commented-out http source, with a table to match. Save it as
`users.yaml`:

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
