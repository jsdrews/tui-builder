# Pipelines

Operator entries live in the same `data.sources:` map as leaf sources,
each with a `type:`. An operator reads one or more other entries (leaf or
operator) and transforms their output. Components and `wrangl` address
an operator by name exactly as they would a leaf, so a source can be
reshaped or swapped without touching what consumes it.

| Operator | Reads | Does | Streams |
|---|---|---|---|
| [`passthrough`](#passthrough) | 1 | gives an entry a second, stable name | yes |
| [`filter`](#filter) | 1 | drops items not matching a predicate | yes |
| [`project`](#project) | 1 | rebuilds each item from declared keys | yes |
| [`derive`](#derive) | 1 | adds computed fields to each item | yes |
| [`sort`](#sort) | 1 | reorders items | no |
| [`union`](#union) | N | concatenates homogeneous lists, tagging rows | when every child streams |
| [`compose`](#compose) | N | bundles different shapes into one object | no |
| [`join`](#join) | 1 + lookups | enriches each row with per-row fetches | no |
| [`cache`](#cache) | 1 | holds one snapshot for a TTL | passes through |

Expressions (`where`, `keep`, `compute`, `by`, `on`) use the expression
language described in [templating.md](templating.md#expressions). Each
item's top-level fields are in scope unprefixed (`status.phase`); for a
plain text line the line is `item`.

Names starting with `_` are hidden from `wrangl --list` (`--all` shows
them) but stay usable everywhere. Inline `pipe:` stages use this for the
intermediate entries they create.

**Validation:** every name an operator reads must be defined, the graph
must have no cycles, and no operator may read a windowed source.

## Fields shared by operators

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | — | required | `passthrough`, `filter`, `project`, `derive`, `sort`, `union`, `compose`, `join` or `cache`. |
| `from` | string | — | passthrough, filter, project, derive, sort, cache | The entry this operator reads. |
| `parameters` | map | — | — | Typed inputs, available in this operator's expressions as `params.NAME`. See [parameters](#parameters). |
| `pipe` | list | — | — | Further stages applied to this operator's output. See [`pipe`](#pipe). |

## `passthrough`

Delegates unchanged. Useful as a stable public name in front of an entry
you expect to swap, and as the carrier for a `pipe:` chain.

```yaml
pods:
  type: passthrough
  from: _pods_raw
```

## `filter`

Keeps items for which `where` is true. On a single value, returns it or
nothing. On a stream, drops events that don't match.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `where` | expr | — | required | The predicate. A missing field evaluates to nil, so `metadata.namespace == 'default'` is simply false on an item without `metadata`. |

```yaml
running_pods:
  type: filter
  from: pods
  where: "status.phase == 'Running'"

errors_only:
  type: filter
  from: app_logs          # a streamed text source
  where: "item contains 'ERROR'"
```

## `project`

Rebuilds each item with only the keys in `keep`. The left side is the
output key and the right side an expression, so one entry can rename,
flatten and compute. Non-map items are dropped.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `keep` | map of expr | — | required | Output key → expression over the input item. |

```yaml
user_summary:
  type: project
  from: users
  keep:
    username: username
    city: address.city
    domain: "lower(website)"
```

## `derive`

Copies each item and adds the fields in `compute`. Every original field
survives; a computed key that already exists replaces it. Non-map items
pass through.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `compute` | map of expr | — | required | New field → expression over the item. |

```yaml
labelled_users:
  type: derive
  from: users
  compute:
    is_biz: "hasSuffix(website, '.biz')"
    name_len: "len(username)"
```

## `sort`

Reorders a list by a key expression. Stable on ties. Numbers compare
numerically, strings lexically, `false` before `true`, times
chronologically; mixed types fall back to their string form; missing
keys sort first in ascending order. A non-list passes through. Not
available on streams.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `by` | expr | — | required | Evaluated per item to produce its sort key. |
| `order` | string | `asc` | — | `asc` or `desc`. |

```yaml
by_restarts:
  type: sort
  from: pods
  by: "status.containerStatuses[0].restartCount"
  order: desc
```

## `union`

Concatenates several entries' lists into one, tagging each row with
where it came from. Like the [`merge`](sources.md#merge) source, but its
children can be operators as well as leaves, so each one can be filtered
or reshaped first.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `sources` | list of string | — | not with `children` | Child entries, by name. |
| `tag_field` | string | — | `sources` | Each row gets a tag under this key whose value is the child's name. |
| `children` | list | — | not with `sources` | Children with per-child `tags` (`source`, `tags`), as on [merge](sources.md#merge-children). |
| `meta_key` | string | `_meta` | — | Where tags are written on each row. `""` writes them at the top level. |
| `on_error` | string | `fail` | — | `fail` aborts on any child error; `skip` drops failed children. |

```yaml
all_running:
  type: union
  sources: [prod_running, staging_running]
  tag_field: cluster
  on_error: skip
```

## `compose`

Bundles several entries into one object, one key per entry, keeping
their different shapes apart. Children are fetched in parallel.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `parts` | map of string | — | required | Output key → entry name. |
| `on_error` | string | `fail` | — | `fail` aborts on any child error; `skip` leaves that key out (and errors only if every child fails). |

```yaml
fleet:
  type: compose
  parts:
    pods: pods_all
    deployments: deployments_all
  # → {pods: [...], deployments: [...]}
```

## `join`

For each row of a driver list, runs one or more lookup sources with
parameters computed from that row, and attaches the results. Lookups run
in parallel per row and are cached by parameter values (see
[`cache`](sources.md#cache)).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `driver` | map | — | required | `from`: the entry whose rows drive the join. |
| `lookups` | map | — | required, at least one | Output name → lookup. Each lookup has `from` (a leaf source that declares `parameters:`) and `on` (that source's parameter → expression over the driver row). |
| `emit` | string | `separate` | — | `separate` gives `{row: <driver row>, <lookup>: <result>, …}` per row; `merged` flattens lookup fields into the row (both must be maps; colliding keys become `<lookup>_<key>`). |
| `on_error` | string | `fail` | — | `fail` aborts the join on any lookup error; `skip` drops that row. |

**Validation:** each lookup's `from` is a leaf source with `parameters:`,
and every `on:` key is one of its parameters.

```yaml
users_with_posts:
  type: join
  driver: {from: users}
  lookups:
    posts:
      from: user_posts     # declares parameters: user_id
      on: {user_id: id}
  emit: separate
```

## `cache`

Holds one snapshot of an entry for a TTL, so several consumers of the
same upstream within the window cause one fetch. Errors aren't cached.
Streams pass through. For per-parameter caching on a leaf source, use the
[`cache:` field](sources.md#cache).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `ttl` | duration | — | required | How long the snapshot stays fresh. |

```yaml
cached_pods:
  type: cache
  from: pods
  ttl: 30s
```

## Parameters

An operator can declare `parameters:` (same fields as
[source parameters](sources.md#parameters)). Bound values are available
in its expressions as `params.NAME`, separate from the item's fields.
They apply only to that operator's own expressions and are not forwarded
to the entries it reads. `wrangl --param` binds an operator's parameters
when it declares some, and otherwise passes through to the underlying
source.

```yaml
long_usernames:
  type: filter
  from: users
  parameters:
    min: {type: int, default: "8"}
  where: "len(username) >= int(params.min)"
```

## `pipe`

A chain of single-input stages applied to an entry's output, without
naming each step. Each stage is a `filter`, `project`, `derive`, `sort`
or `cache` entry written without `from:` (its input is the previous
stage).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `pipe` | list | — | leaf kinds except `merge`; operators | The stages, in order. |

At load, a leaf with `pipe:` becomes a hidden `_NAME_raw` entry plus the
chain, and the chain's final stage takes the original name. Intermediate
stages are hidden `_NAME_stepN` entries. `parameters:` on the leaf still
bind to the raw fetch.

**Validation:** stages have a `type:` from the five allowed and no
`from:`. Multi-input operators (`union`, `compose`, `join`) can't be
stages; declare them as named entries instead.

```yaml
biz_users:
  type: http
  url: https://jsonplaceholder.typicode.com/users
  pipe:
    - {type: filter, where: "hasSuffix(website, '.biz')"}
    - {type: project, keep: {name: name, site: website}}
    - {type: sort, by: name}
```
