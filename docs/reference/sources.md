# Sources

Every entry in `data.sources:` has a `type:`. **Leaf** kinds fetch from
outside the program and are covered here: `http`, `exec`, `file`,
`websocket`, `static` and `merge`. **Operator** kinds transform other
entries and are covered in [pipelines.md](pipelines.md). Both share one
map, so a component or `wrangl` addresses either by name.

How a source delivers data over time follows from its fields; there is
no `lifecycle:` setting:

| Cadence | When |
|---|---|
| streamed | `websocket`, or `http` / `exec` with `follow: true`: events arrive as they happen |
| polled | `refresh:` is set: fetched again on that interval |
| one-shot | neither: fetched once (and again on `r`) |

A source that declares a required parameter with no default "needs
params": it can only run once a caller passes arguments for them (a push binding,
`on_cursor`, a join, or `wrangl --param`).

## Fields shared by leaf sources

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | — | required | `http`, `exec`, `file`, `websocket`, `static` or `merge` (or an operator, see [pipelines.md](pipelines.md)). |
| `parameters` | map | — | any kind | Typed inputs the source references as `${params.NAME}`. See [parameters](#parameters). |
| `root` | string | — (whole value) | — | Dot-path selecting the iterable inside the response, e.g. `items` or `data.results`. |
| `format` | string | `json` | — | `json` parses the response; `text` keeps it as a raw string (for logs and plain-text endpoints). |
| `refresh` | duration | — (fetch once) | not with `follow` | Poll interval. A hidden tab or a screen under a push skips its polls and refetches when shown again. |
| `poll_hidden` | bool | `false` | — | Keeps polling while the source's tab is hidden or its screen is covered by a push. TUI-only: `wrangl` ignores it. |
| `timeout` | duration | `10s` | http, exec, websocket | Per-fetch limit. On websocket it bounds only the initial connect; ignored with `follow: true`. |
| `cache` | map | — | sources with `parameters:` | Memoises fetches per parameter combination. See [`cache`](#cache). |
| `pipe` | list | — | — | Transform stages applied to this source's output. See [pipelines.md](pipelines.md#pipe). |
| `extends` | string | — | any leaf kind | The [preset](#presets) this source inherits fields from. |
| `unset` | list | — | with `extends` | Inherited fields to drop, by name: `unset: [paginate, refresh]`. |
| `resume` | map | — | streams: websocket, or exec / http with `follow: true` | Reconnects exactly: tracks each line's timestamp, resumes from the last one and skips repeats. See [`resume`](#resume). |

**Validation:** `format` is `json` or `text`; `refresh`, `timeout` and
`cache.ttl` parse as durations. Every `${params.NAME}` in a templated
field names a declared parameter.

## Presets

A preset is a named, partial leaf under `data.presets:`. A source names
one with `extends:` and inherits its fields, so ten sources against one
API don't each repeat its base URL, headers and timeout. A preset
produces nothing: a component can't bind to it and `wrangl` can't run
it.

http [actions](actions.md#extending-presets) can extend a preset too,
taking its url, headers, query, auth and timeout.

```yaml
data:
  presets:
    github:
      type: http
      url: https://api.github.com
      headers: {Accept: application/vnd.github+json}
      timeout: 15s
  sources:
    issues:
      extends: github
      url: /repos/${params.repo}/issues     # → https://api.github.com/repos/…
      parameters:
        repo: {type: string, required: true}
```

How a source's fields combine with its preset's:

| Field kind | Fields | Rule |
|---|---|---|
| Single values | `method`, `timeout`, `format`, `root`, `refresh`, `body`, `follow`, … | The source's replaces the preset's. |
| String maps | `headers`, `env` | Merged key by key. The source wins on a shared key, and `""` drops an inherited key. |
| Lists | `command`, `initial_messages` | The source's replaces the whole list. |
| Blocks | `paginate`, `window`, `resume`, `cache` | The source's replaces the whole block; they never merge. |
| `url` | | An absolute url (with a scheme, or starting with a token like `${env.BASE}`) replaces the preset's. Any other is appended to the preset's path with one `/` between: `/v1` + `/users` is `/v1/users`. Query strings in both merge, the source's winning on a shared key. |
| `parameters` | | The union of both. A source that redeclares a name replaces the preset's whole declaration, and `${params.NAME}` anywhere, the preset's url included, is filled by the source's caller. |
| `type` | | Inherited. If both set it, they must match. |

`unset: [field, …]` drops inherited fields. Setting a field to `null`,
`false` or `0` reads the same as leaving it out, so it can't override a
preset's value: unset it instead. A preset may itself `extends:` one other preset,
so org-wide settings can sit under per-API ones.

**Validation:** a preset is any leaf field except `pipe:`, and its keys
are checked strictly: an unknown one is an error that suggests the
nearest field (`unknown field "timout" (did you mean timeout?)`). Its
`type`, if set, is a leaf kind. `extends:` names a preset that exists,
and chains have no cycles. A preset and a source can't share a name.
Everything else is checked on the merged source, so a preset can leave
out what a source must have.

`wrangl --list` shows presets in their own section, and `wrangl
--describe` on a source shows its preset chain and marks each inherited
field `(from <preset>)`. `wrangl --describe <preset>` shows the preset
and the sources that extend it.

## `http`

Requests a URL and parses the response. For REST and JSON APIs,
GraphQL (see [`graphql`](#graphql)), kube proxies, and streamed
endpoints with `follow: true`.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `url` | template | — | required | The endpoint. Tokens are escaped by where they sit; see [requests](#requests). |
| `method` | template | `POST` with a body, else `GET` | — | HTTP method. |
| `headers` | map of template | — | — | Request headers. They win over every header set for you (`Accept`, `User-Agent`, `Content-Type`). Credentials belong in `auth:`, which keeps them out of output. |
| `auth` | map | — | — | Authenticates every request: `bearer`, `basic`, `api_key` or `oauth2`. See [`auth`](#auth). |
| `retry` | map | — (no retries) | — | Tries a failed request again, honouring the server's rate-limit headers. See [`retry`](#retry). |
| `conditional` | bool | `true` | — | Conditional GET: send `If-None-Match` / `If-Modified-Since`, and keep the last value on a `304`. Applies to a `GET` source with no `paginate`, `window` or `follow`. `false` turns it off. |
| `max_body` | size | `50MB` | — | The largest response read (per page): `512KB`, `200MB`. A bigger one fails the source. |
| `query` | map of template or list | — | — | Query parameters, laid over any written in `url`. A list becomes repeated keys; an empty value drops its key. See [requests](#requests). |
| `json` | any | — | not with `form`, `body` | Request body written as YAML and sent as JSON, with `Content-Type: application/json`. See [requests](#requests). |
| `form` | map of template or list | — | not with `json`, `body` | Request body sent URL-encoded, with `Content-Type: application/x-www-form-urlencoded`. Values work as in `query`. |
| `body` | template | — | not with `json`, `form` | Request body, sent as written after substitution. |
| `graphql` | map | — | not with `json`, `form`, `body` | A GraphQL request: `query` and `variables`. See [GraphQL](#graphql). |
| `empty_on` | list of int | — | not a 2xx | Statuses answered with the empty result (`null`) instead of an error, e.g. `[404]` for a lookup. See [responses](#responses). |
| `error_path` | [locator](#locators) | built-in guesses | — | Where an error's message is. On a 2xx, a value there makes the response an error. See [responses](#responses). |
| `follow` | bool | `false` | not with `paginate`, `window` | Streams: holds the response open and emits one event per line (kube `?follow=true`, SSE, NDJSON feeds). |
| `paginate` | map | — | json; not with `follow`, `window` | Walks every page up front into one list. See [`paginate`](#paginate). |
| `window` | map | — | json; not with `follow`, `paginate` | Fetches only the rows on screen and lets the server filter and sort. See [`window`](#window). |

```yaml
releases:
  type: http
  url: https://api.github.com/repos/jsdrews/tui-builder/releases
  refresh: 5m
  timeout: 15s
  headers:
    Accept: application/vnd.github+json
  auth:
    bearer: ${env.GITHUB_TOKEN}
```

### Requests

**The query string** is built in layers, each winning over the one
before on a shared key:

1. parameters written in `url:`
2. `query:`
3. what a [`window`](#window) or [`paginate`](#paginate) sets on each
   request

```yaml
issues:
  type: http
  url: https://api.github.com/repos/${params.repo}/issues?per_page=50
  query:
    state: ${params.state}       # left out when the parameter is empty
    labels: [bug, triage]        # ?labels=bug&labels=triage
```

A key that a window writes on every request (`offset_param`,
`limit_param`, and an Anchored window's `older:` / `newer:` keys when
there's no body) can't also be set in `url:` or `query:`, since it would
be overwritten each time; that's a load error. A key it writes only
some of the time (`search_param`, `sort_param`, `filters`) may: the
value written is the default until the user searches, sorts or
filters.

A next URL that `paginate:` follows is used exactly as the server sent
it, without `query:`.

**Tokens in `url:`** are escaped by where they sit, so a value with a
space, `?` or `&` can't break the request:

| Where | Escaping | Example |
|---|---|---|
| before the path | none: it's the base | `${env.API_URL}/items` |
| in the path | path-escaped, keeping `/` | `/repos/${params.full_name}` with `owner/my repo` → `/repos/owner/my%20repo` |
| in the path, [`segment: true`](#parameters) | fully path-escaped | `/users/${params.id}` with `a/b` → `/users/a%2Fb` |
| in the query string | query-escaped | `?q=${input.search}` with `a&b` → `?q=a%26b` |

Some servers and proxies decode `%2F` back into a separator, or reject
it; that's the server's behaviour, not something a config can change.

**Bodies:** at most one of `json:`, `form:` and `body:`. `json:` is
any YAML value, encoded as JSON. Its strings are templated *before*
encoding, so a quote in a value can't break it, and a string that is
exactly one token takes a typed value: `"${params.limit}"` is a number
for an `int` parameter and a boolean for a `bool` one. A token inside a
longer string substitutes as text, and an exact-token value that
resolves empty drops its key.

```yaml
search:
  type: http
  url: ${env.ES_URL}/logs/_search          # POST: it has a body
  json:
    size: "${params.limit}"                # a number
    query: {match: {message: "${params.q}"}}
```

A window's `older:` / `newer:` patches and `find.body` deep-merge into
`json:`, and merge as top-level fields into `form:`.

**Headers set for you**, unless `headers:` sets them: `Accept:
application/json`, `User-Agent: tui-builder/<version>` (`wrangl/<version>`
from wrangl), and the `Content-Type` of a `json:` or `form:` body.

### Responses

**Empty results:** a `204`, a `2xx` with no body, and any status listed
in `empty_on:` answer with the empty result (`null`, or `""` with
`format: text`): a component shows nothing, and a join lookup finds no
match.

**Errors:** any other non-`2xx` is an error, reported as
`<source>: HTTP 404 Not Found — <message>`. The message comes from
`error_path:` when it's set; otherwise the first of these that holds a
string: `message`, `error.message`, `error`, `error_description`,
`detail`, `title`, `errors.0.message`; otherwise the first 200 bytes of
the body. The output console shows the request and the body it got
back.

A `2xx` is an error only when `error_path:` is set and finds something
there: the way a GraphQL API reports errors on a `200`. If `root:` still
finds data in an error's body, that data is shown alongside the error.

```yaml
user:
  type: http
  url: https://api.example.com/users/${params.id}
  empty_on: [404]                 # no such user: show nothing
  error_path: error.detail        # where this API puts its message
```

### Locators

The response-level fields (`error_path`, `paginate.next_path`,
`window.total_path`) take a **locator**: where in the response a value
is read from.

| Locator | Reads |
|---|---|
| `meta.total` | a dot-path into the raw response body, before `root:` |
| `header:X-Total-Count` | a response header (any case; the first value if it's sent twice) |
| `link:next` | the URL of the `Link` header entry with that `rel`, resolved against the request URL |
| `last:id` | a dot-path into the last item, after `root:` |

A value that isn't there means "not there": no total, no next page, no
message. A total that isn't a number counts as missing. A `Link` entry
marked `results="false"` (Sentry's last page) counts as missing.

Fields that read inside each item (`window.cursor`, `find.position`, a
column's `value:`, `mark_key:`) take a dot-path only.

### GraphQL

`graphql:` sends `{query, variables}` as a JSON `POST`. A GraphQL API
reports errors on a `200`, so the source sets `error_path:
errors.0.message` unless you set your own: a response with errors is an
error, and whatever `root:` finds alongside them is still shown.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `query` | string | — | required | The GraphQL document, sent exactly as written. It's never templated: pass values through `variables`. |
| `variables` | map | — | — | Templated like `json:`: `"${params.first}"` is a number for an `int` parameter, and a value that resolves empty is left out. |

Every path reads the raw response, so they start at `data.`; there's no
shortcut for `root:`. Relay pagination is an ordinary cursor walk, with
the cursor going into the variables:

```yaml
repos:
  type: http
  url: https://api.github.com/graphql
  auth: {bearer: "${env.GITHUB_TOKEN}"}
  graphql:
    query: |
      query($after: String) { viewer { repositories(first: 50, after: $after) {
        nodes { name } pageInfo { endCursor hasNextPage } } } }
  root: data.viewer.repositories.nodes
  paginate:
    strategy: cursor
    cursor_path: data.viewer.repositories.pageInfo.endCursor
    more_when: data.viewer.repositories.pageInfo.hasNextPage
    patch: {variables: {after: "${paginate.cursor}"}}
```

Window `older:` / `newer:` patches merge into the request the same way.

### `auth`

Authenticates every request of an `http` source (and the upgrade request
of a `websocket` one). Set exactly one scheme. Secrets come from the
environment, `${env.NAME}`, rather than the YAML; in a flow map like
`{bearer: …}` the token needs quotes.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `bearer` | template | — | — | Sends `Authorization: Bearer <value>`. |
| `basic` | map | — | — | HTTP basic auth: `user` and `pass`. |
| `user` | template | — | `basic`; required | The user name (Stripe-style APIs put the key here and leave `pass` empty). |
| `pass` | template | — | `basic` | The password. May be empty. |
| `api_key` | map | — | — | A key sent as `in` / `name` / `value`. |
| `in` | string | — | `api_key`; required | `header`, `query` or `cookie`. |
| `name` | string | — | `api_key`; required | The header, query parameter or cookie it goes in. |
| `value` | template | — | `api_key` | The key. |
| `oauth2` | map | — | `http` only | The OAuth 2 client-credentials grant: fetches a token and sends it as a bearer token. |
| `token_url` | template | — | `oauth2`; required | Where to get the token. A relative URL resolves against the source's `url`. |
| `client_id` | template | — | `oauth2`; required | The client ID. |
| `client_secret` | template | — | `oauth2`; required | The client secret. |
| `scopes` | list of string | — | `oauth2` | Scopes to ask for, sent space-separated. |
| `client_auth` | string | `body` | `oauth2` | How the client proves itself to `token_url`: `body` (in the form) or `basic`. |

```yaml
data:
  presets:
    github:
      type: http
      url: https://api.github.com
      auth: {bearer: "${env.GITHUB_TOKEN}"}
    spotify:
      type: http
      url: https://api.spotify.com/v1
      auth:
        oauth2:
          token_url: https://accounts.spotify.com/api/token
          client_id: ${env.SPOTIFY_ID}
          client_secret: ${env.SPOTIFY_SECRET}
          client_auth: basic
    weather:
      type: http
      url: https://api.openweathermap.org/data/2.5
      auth: {api_key: {in: query, name: appid, value: "${env.OWM_KEY}"}}
```

**Where it goes:** auth sets its header (`Authorization`, an `api_key`
header, or `Cookie`) after `headers:`, so setting the same header in
`headers:` too is a load error; `unset: [auth]` drops an inherited auth
first. A `query` key is set last on every request, including the next
URLs `paginate:` follows, and can't also be set in `url:` or `query:`.

**OAuth 2:** the token is fetched on the first request and kept in
memory, shared by every source with the same token URL, client and
scopes. It's fetched again 30 seconds before `expires_in` runs out, and
once more if a request comes back `401` (which is then tried once
again). A failed token request fails the source: `auth: token request
failed — HTTP 401 Unauthorized — invalid_client`.

**Missing secrets:** an empty value fails the source when it runs,
naming what was empty: `auth: bearer is empty (${env.GITHUB_TOKEN} is
unset)`. Other sources keep working.

**Masking:** every resolved `auth:` value, and every `headers:` value
whose name is `Authorization`, `Proxy-Authorization` or `Cookie` or
contains `token`, `key` or `secret`, shows as `•••` in error messages,
the output console, and response snippets. `wrangl --describe` prints
templates as written and literal values as `•••`.

### `retry`

Tries a failed request again. Off unless set; a preset turns it on for
a whole API. Every request is retried on its own: each page of a
`paginate:` walk, each window fetch.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `max` | int | `3` | — | Attempts, the first included. |
| `backoff` | duration | `500ms` | — | The base of the exponential curve: attempt *n* waits a random time up to `backoff × 2ⁿ⁻¹`. |
| `max_backoff` | duration | `30s` | — | Caps the curve. |
| `max_wait` | duration | `60s` | — | The longest wait a server can ask for. A longer one fails at once: `rate limited until 14:32:05 (HTTP 429)`. |
| `on` | list of int | `[408, 429, 500, 502, 503, 504]` | not `401` or a success | Replaces the statuses retried. |

```yaml
retry: {max: 4, backoff: 1s}
```

**What's retried:** network errors and timeouts always; a status in
`on:`; and a `403` that says no requests remain (`X-RateLimit-Remaining:
0`, GitHub's rate limit). A `401` never is. Every method is retried, a
`POST` included, since a source only reads.

**How long it waits:** the server's word beats the curve. `Retry-After`
first (seconds, fractional allowed, or a date); then
`X-RateLimit-Reset`, `X-Rate-Limit-Reset` or `RateLimit-Reset` when the
matching `…-Remaining` header is `0` (a Unix time or seconds from now).
`timeout:` applies to each attempt; the waits don't count against it.

Each retry writes a line to the output console (`retrying 2/3 in 4s —
HTTP 503`; on stderr from `wrangl`), and the last value stays on screen.
A polled source skips its ticks while a retry is pending.

## `exec`

Runs a command and consumes its stdout. Any CLI that prints JSON becomes
a source (`kubectl get -o json`, `gh api`, scripts); with
`follow: true`, any command that streams lines (`kubectl logs -f`,
`tail -f`).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `command` | list of template | — | required | The argv. The first element is looked up in `$PATH`. Not run through a shell: use `[sh, -c, "…"]` when you want one. |
| `env` | map of template | — | — | Variables added to (or overriding) the inherited environment. |
| `follow` | bool | `false` | not with `window` | Streams: reads stdout line by line as it arrives. |
| `window` | map | — | json; not with `follow` | Runs one command per window of a larger set, with offset, limit, filter and sort templated into the argv. See [`window`](#window). |

```yaml
recent_commits:
  type: exec
  command: [sh, -c, "git log -20 --format='{\"sha\":\"%h\",\"subject\":\"%s\"}' | jq -s ."]
  refresh: 30s
```

## `file`

Reads a file from disk.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `path` | template | — | required | Path to the file. Re-read on each fetch, so with `refresh:` edits show up live. |

```yaml
people:
  type: file
  path: ./examples/fixtures/people.json
  refresh: 5s
```

## `websocket`

Connects to a WebSocket and emits one event per frame. Always streamed.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `url` | template | — | required | `ws://` or `wss://` endpoint. |
| `headers` | map of template | — | — | Sent on the upgrade request. |
| `auth` | map | — | not `oauth2` | Applied to the upgrade request. See [`auth`](#auth). |
| `initial_messages` | list of template | — | — | Text frames sent, in order, right after connecting, for protocols that need a subscribe message first. |

```yaml
trades:
  type: websocket
  url: wss://ws.bitstamp.net
  initial_messages:
    - '{"event":"bts:subscribe","data":{"channel":"live_trades_btcusd"}}'
```

## `static`

Inline data in the YAML. No I/O. For fixtures, lookup tables, offline
demos and building a pipeline before pointing it at a real source.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `data` | any | — | required | The value: a list, an object or a scalar. `root:` slices into it. |

```yaml
regions:
  type: static
  data:
    - {code: us-east-1, name: N. Virginia}
    - {code: eu-west-1, name: Ireland}
```

## `merge`

Fetches several sources and concatenates their results into one list,
optionally tagging each row with where it came from. Streams when every
child streams; otherwise polls. Prefer the [`union`](pipelines.md#union)
operator in new configs; `merge` remains for its leaf fields (`refresh`,
`timeout`).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `sources` | list of string | — | not with `children` | Child entries, by name. |
| `tag_field` | string | — | `sources` | Each row gets a tag under this key whose value is the child's name. |
| `children` | list | — | not with `sources` | Child entries with per-child tags. See [merge children](#merge-children). |
| `meta_key` | string | `_meta` | — | Where tags are written on each row. `""` writes them at the top level of the row instead. |
| `on_error` | string | `fail` | — | `fail` aborts on any child error; `skip` drops failed children and returns the rest. |

**Validation:** exactly one of `sources` and `children`; `tag_field`
only with `sources`; `on_error` is `fail` or `skip`.

### Merge children

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `source` | string | — | required | The child entry's name. |
| `tags` | map of string | — | — | Key/value tags added to every map-shaped row from this child. Existing keys are kept. |

```yaml
pods_all:
  type: merge
  children:
    - {source: pods_prod,    tags: {cluster: prod,    cluster_url: "http://localhost:8001"}}
    - {source: pods_staging, tags: {cluster: staging, cluster_url: "http://localhost:8002"}}
  on_error: skip
  refresh: 5s
```

## Parameters

Typed inputs on a source, referenced as `${params.NAME}` in its
templated fields (`url`, `method`, `body`, `headers`, `path`, `command`,
`env`, `initial_messages`). Callers pass arguments: a push binding's
`bind:`, `on_cursor.bind`, a join lookup's `on:`, or
`wrangl --param NAME=VALUE`. On an operator, values are available in its
expressions as `params.NAME` (see
[pipelines.md](pipelines.md#parameters)).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `type` | string | `string` | — | `string`, `int`, `bool` or `duration`. |
| `required` | bool | `false` | not with `default` | The source can't run until a caller supplies a value. |
| `default` | string | — | not with `required` | Used when no caller supplies one. |
| `description` | string | — | — | One-line summary, shown by `wrangl --describe`. |
| `segment` | bool | `false` | — | The value is one URL path segment: in a `url:` path it's fully escaped, `/` included (`a/b` → `a%2Fb`). Without it `/` is kept, so `owner/repo` spans two segments. An OpenAPI `in: path` parameter. |

The form fields (`label`, `placeholder`, `options`, `mask`, `order`) are
accepted too but only matter where a parameter is rendered as a form
field, which is in [action inputs](actions.md#inputs) and
[boot prompts](app.md#appprompts).

Resolution: the caller's value, else `default`, else an error if
`required`, else empty. A supplied name the source doesn't declare is an
error, so typos surface. A `${params.X}` with no declared parameter is
left literal, so it shows up in the URL rather than vanishing.

```yaml
pod:
  type: http
  parameters:
    namespace: {required: true}
    name:      {required: true}
  url: http://localhost:8001/api/v1/namespaces/${params.namespace}/pods/${params.name}
```

## `paginate`

Walks every page of an http endpoint before returning, concatenating
each page's items (after `root:`) into one list. For sets of up to a few
thousand rows; for bigger ones use [`window`](#window).

`strategy:` says how the API names the next page. Each strategy takes
only its own fields; a field from another is a load error.

| Strategy | The next request | Stops when |
|---|---|---|
| `link` | follows the next URL the response names, exactly as sent | the next URL is empty |
| `cursor` | sends the cursor the response names | the cursor is empty or missing |
| `offset` | sends the count of items so far | the total is reached, or a page is empty |
| `page` | sends the next page number | the total is reached, or a page is empty |

Every strategy also stops when `more_when:` is false, on a short page
with `stop_on_short_page: true`, when a page repeats the one before
(the same next URL or cursor, or the same items for `offset` / `page`),
and at `max_pages`. An empty page never stops a `link` or `cursor`
walk: some APIs send them mid-walk.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `strategy` | string | — | required | `link`, `cursor`, `offset` or `page`. |
| `next_path` | [locator](#locators) | — | `link`; required | Where the next page's URL is: `next`, `links.next`, or `link:next` for the `Link` header (GitHub, GitLab, Okta, Shopify, Sentry). A relative URL resolves against the page that sent it. |
| `cursor_path` | [locator](#locators) | — | `cursor`; required | Where the next cursor is: `meta.next_cursor`, `header:X-Next-Cursor`, or `last:id` for the last item's id (Stripe). |
| `cursor_param` | string | — | `cursor` | The query parameter the cursor goes in. Without it, put `${paginate.cursor}` in a `patch:`. |
| `offset_param` | string | — | `offset` | The query parameter for the number of items fetched so far. |
| `page_param` | string | — | `page` | The query parameter for the page number. |
| `page_start` | int | `1` | `page` | The first page's number: `0` for zero-based APIs such as Algolia. |
| `limit_param` | string | — | not `link` | The query parameter carrying `page_size`. |
| `page_size` | int | `100` | — | Items per page: sent in `limit_param` and `${paginate.limit}`, and what `stop_on_short_page` compares with. |
| `total_path` | [locator](#locators) | — | `offset`, `page` | Where the total item count is; the walk stops once it has that many. `header:X-Total-Count` for json-server. |
| `more_when` | expression | — | — | Evaluated over each raw response; when it's false, the walk stops. `has_more`, `"!isLast"`, `"page + 1 < nbPages"`. |
| `stop_on_short_page` | bool | `false` | — | Stop on a page with fewer than `page_size` items (Discord). |
| `patch` | map | — | not `link`; not with `body` | Merged into every request: into the query string, or into `json:` / `form:` when there's a body. Its `${paginate.cursor}`, `${paginate.offset}`, `${paginate.page}` and `${paginate.limit}` tokens are the walk's position. A value that is exactly one token keeps its type (an Elasticsearch `search_after` stays an array) and is left out while empty, so the first request carries no cursor. |
| `max_pages` | int | `20` | — | Caps the walk. `0` or unset means the default of 20; there is no unlimited setting (one runaway API could exhaust memory), so set a larger number if you need more pages. A walk that stops here with more to come posts one notice: `stopped at max_pages (20) — 2000 of 3500 rows; raise paginate.max_pages or use window:`. |
| `on_page_error` | string | `fail` | — | `fail` returns the error; `skip` returns the pages fetched so far, and posts a notice naming the failed page. |

Unknown keys under `paginate:` are a load error, with the nearest field
suggested. `cursor_param`, `offset_param`, `page_param` and
`limit_param` are written on every request, so the same key can't also
be set in `url:` or `query:`.

```yaml
jobs:                                     # Django REST / AWX: the next URL in the body
  type: http
  url: ${env.AWX_HOST}/api/v2/jobs/
  root: results
  paginate: {strategy: link, next_path: next, max_pages: 50}

issues:                                   # GitHub: the next URL in a Link header
  type: http
  url: https://api.github.com/repos/jsdrews/tui-builder/issues?per_page=100
  paginate: {strategy: link, next_path: "link:next"}

channels:                                 # Slack: a cursor in the query string
  type: http
  url: https://slack.com/api/conversations.list
  root: channels
  paginate:
    strategy: cursor
    cursor_path: response_metadata.next_cursor
    cursor_param: cursor
    limit_param: limit
    page_size: 200

pages:                                    # Notion: a cursor in the JSON body
  type: http
  url: https://api.notion.com/v1/databases/${params.db}/query
  json: {page_size: 100}
  root: results
  paginate:
    strategy: cursor
    cursor_path: next_cursor
    more_when: has_more
    patch: {start_cursor: "${paginate.cursor}"}
```

## `window`

Fetches only the rows a table is showing, and fetches again as the user
scrolls, filters or sorts, so the **server** answers the filter and sort
over the whole set.

| | `paginate:` | `window:` |
|---|---|---|
| Requests before the first frame | one per page | one |
| Rows held | all of them | the window on screen |
| Who filters and sorts | the table, over what it holds | the server, over everything |

Supported on `http` (the request goes in the query string) and `exec`
(the request is templated into the argv).

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `page_size` | int | `100` | — | Rows per request. |
| `prefetch` | int | `0` | — | Extra pages fetched beyond the screen. `1` hides the placeholder flash at page boundaries, for one extra request. |
| `total_path` | [locator](#locators) | — | required, except with `cursor` | Where the total match count is: a dot-path into the raw response (before `root:`), or a header such as `header:X-Total-Count`. Without a total the table never scrolls past its first window, so it's required; a response that lacks it posts one notice. |
| `filters` | map of string | — | — | Maps a column **title** to where a scoped `title:value` filter term goes: a query parameter on http, a `${window.filters.NAME}` token name on exec. Terms on unmapped columns are treated as bare terms. Without `search_param` (or `${window.search}` on exec), bare terms aren't sent: the table says so and its placeholder shows the column syntax. |
| `offset_param` | string | — | http: this or `page_param`; not on exec | Query parameter for the first row wanted (`offset`, `_start`, `skip`). |
| `page_param` | string | — | http: this or `offset_param`; not on exec | Query parameter for a page number, for APIs that page by number (`?page=3&per_page=50`). A window that spans several pages is fetched a page at a time, in order, and trimmed. |
| `page_start` | int | `1` | with `page_param` | The first page's number; `0` for zero-based APIs. |
| `limit_param` | string | — | required on http; not on exec | Query parameter for the page size (`limit`, `per_page`). With `page_param` it always carries `page_size`, which must not exceed the API's maximum: a server that caps it posts a notice. |
| `search_param` | string | — | http only | Query parameter for bare filter terms, joined with spaces. |
| `sort_param` | string | — | http only | Query parameter for the sort field. |
| `sorts` | map of string | — | http only; needs `sort_param` | Maps a column title to the sort field the API expects. Unlisted columns send their title. |
| `sort_fields` | list of string | — | http: needs `sort_param`; exec: `command:` references `${window.sort}`; not Anchored | The sort fields the API accepts, e.g. an `order_by` enum. A bound table's column whose `sorts:` entry, or else whose `value:` is one plain key, is listed becomes sortable without `sortable: true`, and on http sends that field. `sortable: false` opts a column out. On exec it only decides which columns sort; the command still gets the title. |
| `sort_desc_prefix` | string | — | http only; needs `sort_param` | Prepended for a descending sort, e.g. `-` for Django REST (`ordering=-created`). Empty means both directions send the same value. |
| `cursor` | string | — | eventlog and span-table sources | Makes the source **Anchored**: walked from an edge instead of paged by offset. The dot-path to each item's cursor, the value the next request walks from (ES `sort`, a timestamp, an id). See [Anchored windows](#anchored-windows). |
| `older` | map | — | http, with `cursor` | What the request changes to walk towards older items, merged into `body:` or set as query parameters. |
| `newer` | map | — | http, with `cursor` | The same, walking towards newer items. |
| `anchor` | string | `newest` | with `cursor` | Where an Anchored view starts: `newest` (and follows), `oldest` (and walks newer as you scroll down), or a template that resolves to a cursor, `${input.jump_to}`, to jump there. See [jumping](#jumping-to-a-cursor). |
| `find` | map | — | http | Searches past what's loaded: `n` / `N` ask the source where the next match is. See [`find`](#find). |
| `viewport_delay` | duration | tuilib's | — | How long scrolling must pause before rows are requested. `-` asks at once. |
| `growing` | `true` or map | — | eventlog only; not with `refresh` | The set is still gaining items at its newest end. A bound eventlog follows the newest item and the source is polled every `follow_every`; when growth stops, one final read picks up the last items. `true` grows for as long as the screen is open. The map form ties it to another source; see [growing](#growing). |
| `follow_every` | duration | `2s` | needs `growing` | Poll interval while growing. |

On exec, `command:` uses these tokens instead of the `*_param` fields:

| Token | Value |
|---|---|
| `${window.offset}` | First row wanted. Required. |
| `${window.limit}` | Page size. Required. |
| `${window.search}` | Bare filter terms joined with spaces; empty when none. |
| `${window.filters.NAME}` | A scoped term's value, where `NAME` is what `filters:` maps the column to. |
| `${window.sort}` | The sort column's title; the command maps it itself. |
| `${window.sort_dir}` | `asc` or `desc`; empty when unsorted. A sortable column needs `command:` to reference both this and `${window.sort}`. |
| `${window.cursor}` | Anchored only: the edge item's cursor; empty on the first request. |
| `${window.dir}` | Anchored only: `older` or `newer`. |

An argv element whose window tokens all resolve empty is dropped, so
`--author=${window.filters.author}` disappears when no author term is
typed. Substitution is literal, so in a `sh -c` script the quoting
around a token is what keeps it safe; plain argv elements are passed as
single arguments.

Query parameters already on an http `url:` are kept on every request; a
parameter the window also sets is overwritten, so `?q=x` on the URL acts
as a default search the user's filter replaces. `refresh:` and `r`
refetch the window on screen in place.

### Anchored windows

Some APIs can't jump to row N: Elasticsearch past 10,000 hits
(`search_after`), log APIs filtered by timestamp, and any API that pages
with an `?after=<token>`. They're **Anchored**: read by walking from an
edge item to the items before or after it. Setting `cursor:` makes a
window Anchored. An eventlog bound to one opens at the newest item and
walks older as you scroll up, or, with `anchor: oldest`, opens at the
oldest and walks newer.

Each request carries the edge item's cursor, which direction to walk,
and the page size:

- **http:** the `older:` or `newer:` map is merged into `body:` (a JSON
  object), or, for a request with no body, set as query parameters. A
  value that is exactly `${window.cursor}` takes the cursor's JSON value
  (an ES sort array stays an array) and is left out of the first request,
  which has no cursor yet. Inside `body:` text, `${window.limit}` is a
  number and `${window.search}` / `${window.filters.NAME}` are escaped for
  a JSON string. `search_param` and, with no body, `filters:` still name
  query parameters.
- **exec:** the command reads `${window.cursor}` (a string cursor
  unquoted, anything else as JSON), `${window.dir}` (`older` or `newer`)
  and `${window.limit}`, plus the usual filter tokens.

Answer an older walk newest first, as a descending sort does; tui-builder
reverses it. `${window.limit}` is one more than the page size: whether the
extra item comes back is how tui-builder knows that edge has more.

Items are held by cursor plus the component's `key:`, so two items with
the same cursor (log lines stamped the same millisecond) both stay.

```yaml
logs:
  type: http
  method: POST
  url: ${env.ES_URL}/app-logs/_search
  root: hits.hits
  body: '{"query": {"query_string": {"query": "${window.search}*"}}, "size": ${window.limit}}'
  window:
    cursor: sort
    older: {sort: [{"@timestamp": desc}, {_id: desc}], search_after: "${window.cursor}"}
    newer: {sort: [{"@timestamp": asc},  {_id: asc}],  search_after: "${window.cursor}"}
```

An Anchored window has no offsets, total or user sort, so `offset_param`,
`total_path`, `sort_param`, `sorts`, `sort_desc_prefix` and `prefetch`
don't apply. Where it opens is `anchor:`; the eventlog's `start:` is for
paged sources and is rejected here. An eventlog or a table can bind one;
a table over an Anchored source is a span table (see
[components.md](components.md#table)).

#### Jumping to a cursor

`anchor:` can be a template instead of `newest` / `oldest`. It resolves
to a cursor (a timestamp, an id), and the view opens there. When it
reads an [input](components.md#input) (`${input.NAME}`), committing that
input jumps again. A template that resolves empty means `newest`.
Templates take `${input.*}` and `${env.*}`.

The value is sent as the cursor: text a JSON parser can't read is sent
as a JSON string, so a typed `2026-10-11T09:00:00Z` reaches
`search_after` as a string, and `[1760173200000]` stays an array.

The first request walks older from the cursor, which most APIs answer
*excluding* the item at the cursor itself. That item arrives with the
newer side, so the view settles a row from where it jumped. For a
typed time, which rarely matches an item exactly, that's the item you
wanted anyway.

```yaml
jump_to: {type: input, title: Jump to (RFC 3339)}

logs:
  # …as above…
  window:
    cursor: sort
    anchor: "${input.jump_to}"
```

### `find`

Without `find:`, `/` searches only the items loaded, and `n` / `N` stop
at the edge of what's held. With it, they ask the source where the next
match is. `find:` is a patch over the source's request, like `older:` /
`newer:`:

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `params` | map | — | one of `params` / `body` | Query parameters set on the search request. |
| `body` | map | — | one of `params` / `body` | Fields merged into the request's JSON body. |
| `position` | path | — | required on a paged source; not on an Anchored one | The path in the matching item to its 0-based position in the (filtered) set. |

Its tokens are `${window.term}` (what was searched for), `${window.dir}`
(`older` / `newer`: before or after the current item),
`${window.cursor}` (the current item's cursor) and `${window.from}` (its
position, on a paged source), plus the view's filter as on any window
request. `${window.limit}` is `1`.

The reply's first item is the match; none means there's no further
match. An Anchored view re-anchors at the match's cursor; a paged one
scrolls to its position.

```yaml
window:
  cursor: sort
  older: {…}
  newer: {…}
  find:
    body:
      query: {query_string: {query: "${window.term}"}}
      search_after: "${window.cursor}"
      sort: [{"@timestamp": desc}]
```

**Validation:** `find` needs `params` or `body`; `position` is required on
a paged source and an error on an Anchored one; `find` is http-only (an
exec window searches what's loaded).

### Growing

`growing:` as a map names another source and a condition on its value,
re-evaluated every time that source answers. That source needn't be shown
anywhere: the screen fetches it when it opens, and polls it on its own
`refresh:`, so give it one.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `source` | string | — | required | The entry whose value decides, e.g. the job a list of events belongs to. |
| `while` | expr | — | required | True while items are still arriving, over that source's value. |

```yaml
data:
  sources:
    job:
      type: http
      url: ${env.AWX_HOST}/api/v2/jobs/${params.id}/
      refresh: 5s
    job_events:
      type: exec
      command: [awx-events, --job, "${params.id}", --offset, "${window.offset}", --limit, "${window.limit}", --search, "${window.search}"]
      window:
        total_path: count
        growing: {source: job, while: 'status in ["pending", "waiting", "running"]'}
        follow_every: 2s
```

**Validation:**

- `window:` only on `http` and `exec`, with json format, not with
  `follow` or `paginate`.
- Only a `type: table` or an `eventlog` may bind a windowed source, and
  an operator can't consume one (it would see one page and present it as
  everything). `growing:` is eventlog-only, can't be combined with
  `refresh:`, and its `source:` must exist and its `while:` compile;
  `follow_every` needs `growing`.
- A seekable window (no `cursor:`) needs `total_path`.
- http: `limit_param` and exactly one of `offset_param` / `page_param`
  are required; `page_start` needs `page_param` and is not negative. `sorts` and
  `sort_desc_prefix` need `sort_param`.
- exec: the http-only fields are errors. `command:` must reference
  `${window.offset}` and `${window.limit}`; every `filters:` entry must
  be referenced; `command[0]` can't use a window token.
- A `filterable: true` table needs somewhere to send the filter: at
  least one of `search_param` / `filters` on http, or `${window.search}`
  / a declared filter token on exec. An Anchored http window can carry
  it as `${window.search}` in `body:`, `older:` or `newer:`. A table
  without `filterable` needs none, which is how to window an API that
  can't filter.
- A `sortable` column on the bound table needs `sort_param` (http), or
  a `command:` that references both `${window.sort}` and
  `${window.sort_dir}` (exec).
- With `sort_fields`, `sortable: true` on a column the list doesn't
  match is an error: the API won't sort by it. A listed field no column
  shows is fine. Fields can't be empty.
- `page_size` and `prefetch` are not negative.
- With `cursor:`: http needs both `older:` and `newer:`, which are
  http-only; exec must reference `${window.cursor}`, `${window.dir}` and
  `${window.limit}`; the offset, total and sort fields are rejected.
- `anchor:` needs `cursor:`. A paged window starts where the bound
  component's `start:` says.

`wrangl` on a windowed source returns its first page.

```yaml
books:
  type: http
  url: https://openlibrary.org/search.json?q=tolkien&fields=title,author_name
  root: docs
  window:
    prefetch: 1
    offset_param: offset
    limit_param: limit
    total_path: numFound
    search_param: q          # "hobbit"         → ?q=hobbit
    filters: {Author: author} # "author:tolkien" → ?author=tolkien
```

An API that lists what it can sort by, such as an `order_by` enum,
says so once on the source instead of on every column:

```yaml
things:
  type: http
  url: https://api.example.com/things
  root: entries
  window:
    page_param: page
    limit_param: per_page
    total_path: total
    sort_param: order_by
    sort_desc_prefix: "-"
    sort_fields: [name, created_at]   # columns with value: name / created_at sort
```

## `resume`

A stream that drops, or a follow that stops with its container, can be
reconnected with `r`. On its own that starts over from the live tail, so
lines are missed or shown twice. With `resume:`, tui-builder reads each
line's timestamp, and a reconnect asks the source for the lines since the
last one shown, through `${resume.since}` in the request, and skips the
ones it already has. A stream with `resume:` also reconnects by itself
when it ends: after a second, then backing off to 30s while it keeps
ending with nothing new.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `timestamp` | regex | — | required | A regular expression whose first capture group is each line's timestamp, in RFC 3339 (fractional seconds allowed). Lines it doesn't match are shown but not tracked. |
| `granularity` | duration | `1s` | — | The resolution the source's "since" parameter honours. The Kubernetes API truncates to a second; lines within the resumed second are matched up by count. |

`${resume.since}` is the time to ask from, RFC 3339 in UTC, and is empty
on the first connect. It can appear in `command:`, `env:`, `url:`,
`headers:`, `body:` and `initial_messages:`. Each reconnect marks the log
`— reconnected after 12s —`: lines sent while the stream was down are
fetched, but nothing can say whether the source kept them all.

```yaml
pod_logs:
  type: exec
  follow: true
  parameters: {pod: {required: true}}
  command:
    - sh
    - -c
    - 'since="${resume.since}"; kubectl logs -f --timestamps ${since:+--since-time=$since} "${params.pod}"'
  resume:
    timestamp: '^(\S+) '
```

tui-builder fills in `${resume.since}` before the shell runs; the shell's
`${since:+…}` then leaves `--since-time` off the first connect, when it's
empty.

**Validation:** `resume` only on a stream; `timestamp` compiles and has a
capture group; `granularity` is a positive duration; a source that reads
`${resume.since}` must declare `resume:`.

## `cache`

Memoises a source's fetches, keyed on its bound parameter values: many
calls with the same values within the TTL make one upstream request.
For sources fed by `on_cursor` or join lookups, so sweeping a cursor
over rows already visited doesn't refetch them. Streamed events pass
through uncached, and errors aren't cached. Distinct from the
[`type: cache`](pipelines.md#cache) operator, which holds one snapshot.

Join lookups get a cache automatically (60s TTL, 100 entries); declare
`cache:` to change the numbers.

| Field | Type | Default | Valid on/with | What it does |
|---|---|---|---|---|
| `ttl` | duration | — | required | How long an entry stays fresh. |
| `size` | int | `100` | — | Maximum entries; the least recently used is evicted. Negative is unbounded. |

```yaml
user_posts:
  type: http
  parameters: {user_id: {type: int, required: true}}
  url: https://api.example.com/users/${params.user_id}/posts
  cache: {ttl: 5m, size: 200}
```
