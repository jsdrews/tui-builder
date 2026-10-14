# HTTP source: the locked spec

What `type: http` grows into, so that most real APIs work without an
`exec: curl | jq` workaround. It's locked: every decision was made on
the map
[Map: HTTP source spec (pagination, auth, bodies, presets)](https://github.com/jsdrews/tui-builder/issues/7),
and each section links the ticket that holds its detail and reasoning.
Seven PRs, in the [order at the bottom](#implementation-order).

One constraint shaped every decision: **each field maps cleanly from
OpenAPI 3**, because the next effort is a skill that turns an OpenAPI
document into a tui-builder config. Document-level constructs
(servers, server variables, security schemes) land on a **preset**;
operation-level ones (path, parameters, request body, pagination) land
on a **source** that extends it.

The research behind it lives in `plans/research/`:

- [`http-survey.md`](research/http-survey.md): how about 25 widely used
  APIs page, authenticate, rate-limit and shape their responses.
- [`http-survey-check.md`](research/http-survey-check.md): that survey
  checked against current vendor docs
  ([Verify the API survey against current docs](https://github.com/jsdrews/tui-builder/issues/18)).
- [`openapi-mapping.md`](research/openapi-mapping.md): how OpenAPI 3
  maps onto this schema
  ([How OpenAPI 3 expresses servers, security, parameters and pagination](https://github.com/jsdrews/tui-builder/issues/17)).

Terms follow [`CONTEXT.md`](../CONTEXT.md): source, leaf, preset,
parameter, argument, window, token, and the two this spec added,
**locator** and **patch**.

## What ships today

`url`, `method`, `headers`, `body` (a string template), `format:
json|text`, `root`, `refresh`, `timeout` (per request, default 10s),
`follow`, `resume:`, `paginate: {strategy: link, next_path, max_pages,
on_page_error}`, and `window:`. Unknown YAML keys are silently ignored,
URL tokens are substituted unescaped, there's no default
`User-Agent`, and nothing is retried or redacted.

## Coverage

What the finished spec covers, by API style. Every row is reachable with
YAML alone, except where noted.

| API | Paging | Other features it needs |
|---|---|---|
| Django REST, AWX, Spotify | `link`, `next_path: next` | `oauth2` (Spotify) |
| Confluence, Salesforce, OData | `link`, relative next URLs | `oauth2` (Salesforce, Azure) |
| GitHub, GitLab, Okta, Shopify, Mastodon | `link`, `next_path: link:next` | `bearer`, `retry:` with reset headers, conditional GET |
| Sentry | `link:next`, `results="false"` built in | `bearer` |
| Slack, Kubernetes, Google, HubSpot, Airtable, X | `cursor` | `bearer` / `api_key` |
| Notion, Elasticsearch `search_after` | `cursor` with `patch:` into `json:` | `json:` |
| Jira, PagerDuty | `offset`, `more_when` | `basic` (Jira Cloud) |
| Algolia, GitHub search | `page` (`page_start: 0` for Algolia), `more_when` | `api_key` header |
| json-server v1 | `page`, repeat guard | — |
| Zendesk | `link` + `more_when: meta.has_more` | `basic` |
| GraphQL (GitHub v4, Shopify, Linear) | `cursor` with `patch:` into `variables` | `graphql:` |
| Stripe | `cursor`, `cursor_path: last:id`, `more_when: has_more` | `bearer`, `form:` |
| Discord | `cursor`, `cursor_path: last:id`, `stop_on_short_page` | `bearer` |
| AWS | `cursor` with `patch:` | **not covered**: needs SigV4 signing |

## 1. Presets

From [Presets: the merge rules for extending a partial source](https://github.com/jsdrews/tui-builder/issues/9).
**Landed in [#81](https://github.com/jsdrews/tui-builder/pull/81).** Built as written, plus: a
`url:` that starts with a token counts as absolute, `false` / `0` /
`null` can't override a preset's value (only `unset:` can), and the
undeclared-parameter check applies to every leaf, not only ones that
extend a preset.

A **preset** is a named, partial leaf under `data.presets`. A source
names one with `extends:`. Presets aren't HTTP-only: any leaf kind can
have one.

```yaml
data:
  presets:
    github:
      type: http
      url: https://api.github.com
      headers: {Accept: application/vnd.github+json}
      auth: {bearer: ${env.GITHUB_TOKEN}}
      retry: {max: 3, backoff: 500ms}
  sources:
    issues:
      extends: github
      url: /repos/${params.repo}/issues
      query: {state: open}
      parameters:
        repo: {type: string, required: true}
```

**Merging** (the source over its preset):

| Field kind | Examples | Rule |
|---|---|---|
| Single values | `method`, `timeout`, `format`, `root`, `refresh`, `body` | the source's replaces the preset's |
| String maps | `headers`, `query`, `env` | merged key by key; the source wins, and `""` drops an inherited key |
| Lists | `command`, `initial_messages` | the source's replaces the whole list |
| Structured blocks | `auth`, `paginate`, `window`, `resume`, `cache`, `retry` | the source's replaces the whole block; no deep merge |

**Unsetting:** `unset: [auth, paginate]` drops inherited fields. OpenAPI's
`security: []` maps onto `unset: [auth]`.

**URL join:**
- An absolute source `url:` (`https://…`) replaces the preset's.
- A relative one is **appended** to the preset's path with exactly one
  `/` between (`/v1` + `/users` = `/v1/users`). This follows OpenAPI,
  not RFC 3986.
- With no source `url:`, the preset's is used as it is.
- Query strings written in both URLs merge; the source wins on a shared
  key. They're the lowest query layer (see [§2](#2-the-query-string)).

**`type:`:** inherited from the preset. If both set it, they must match.

**Chains:** a preset may `extends:` one other preset. A cycle is a load
error. `extends:` names exactly one preset, never a list.

**Contents:** any leaf field except `pipe:`. Validation runs on the
merged source, so a preset may be incomplete.

**Parameters:** the merged source's parameters are the union of both,
matched by name, and a source that redeclares a name replaces the
preset's whole declaration. Every `${params.X}`, the preset's URL
included, resolves against that merged set, so an OpenAPI server
variable (`{region}`) on the preset is filled by the source's caller. A
token naming an undeclared parameter is a load error, checked after the
merge.

**In wrangl** (from [Validation errors and --describe output](https://github.com/jsdrews/tui-builder/issues/77)):
`--list` shows presets in their own section; `--describe <preset>`
shows `Kind: preset / http`, its fields and `Extended by: issues,
pulls`; running a preset fails with `github is a preset; run a source
that extends it`.

## 2. The query string

From [Who owns the query string](https://github.com/jsdrews/tui-builder/issues/8).
Ships in PR 2.

**Writers, lowest to highest** (later wins on a shared key):

1. Parameters written literally in `url:` (the preset's and the
   source's, merged)
2. `query:`, including what a preset contributes
3. Per-request logic: window parameters (`offset_param`, `limit_param`,
   `search_param`, `sort_param`, `filters:` destinations), window and
   `find` patches, and `paginate:`'s parameters and `patch:`
4. Auth: a query `api_key` (see [§7](#7-auth))

**Owned keys:** a key that layer 3 or 4 writes on *every* request can't
also be set in `url:` or `query:`. That's a load error naming the field
that owns the key; silently dropping the author's value would be a
design bug. A key written only some of the time (`search_param`,
`sort_param`, `filters`, `find.params`) may be set: the author's value
is the default it replaces while the user searches, sorts or filters
(`examples/http_window.yaml` relies on this with `?q=tolkien`).

**`query:` values:** a string, or a list of strings that becomes
repeated keys (`?tag=a&tag=b`, OpenAPI's default `form` + explode).
Object-style parameters (deepObject) are written as literal keys:
`filter[status]: open`. Each value is templated, then URL-encoded. A
value that resolves empty drops its key, and an empty list item is
dropped.

**Following a next URL:** `paginate:` uses it exactly as the server
sent it, without re-applying `query:`, because the server encodes the
page's state in it. Only auth's query key is re-applied. A relative
next URL resolves against the request URL (RFC 3986), unlike the
preset join.

**Tokens in the URL are escaped by where they sit** (OpenAPI `in:
path`). Today they're substituted raw, so an id with a space or `?`
breaks the request.
- In the path, a token's value is path-escaped, except that `/` is
  kept: `${params.full_name}` = `owner/repo` still spans two segments,
  as existing configs rely on.
- In a query string written in `url:`, it's query-escaped.
- A token before the path (`${env.ARGOCD_URL}/api/…`) is a base URL
  and is left as it is.

## 3. Request bodies and default headers

From [Request bodies: body vs json vs form](https://github.com/jsdrews/tui-builder/issues/12).
Ships in PR 2.

At most one of these is set (more is a load error):

| Field | Value | `Content-Type` |
|---|---|---|
| `json:` | any YAML value, encoded as JSON | `application/json` |
| `form:` | a map, URL-encoded | `application/x-www-form-urlencoded` |
| `body:` | a raw templated string, as today | whatever `headers:` sets |
| `graphql:` | see [§9](#9-graphql) | `application/json` |

An explicit `Content-Type` in `headers:` always wins.

**`json:` templating:** strings are templated *before* encoding, so a
quote in a value can't break the JSON. A string that is exactly one
token (`"${params.limit}"`) takes a typed value: a number for an `int`
parameter, a boolean for `bool`, a cursor's own JSON value. A token
inside a longer string is substituted as text. An exact-token value
that resolves empty drops its key.

**`form:` values** follow `query:`: a string or a list (repeated keys),
templated then encoded, empty values dropped.

**Method:** with `method:` unset, a request with a body is POST and one
without is GET. An explicit `method:` wins.

**`User-Agent`:** every request sends `tui-builder/<version>`, or
`wrangl/<version>` from wrangl, unless `headers:` sets one. GitHub
rejects requests without one, and Reddit and Wikipedia throttle
generic ones.

**Patches** (window `older:` / `newer:`, `find.body`, `paginate.patch`):
deep-merged into `json:`, merged as top-level fields into `form:`.
With `body:`, window patches still require it to parse as a JSON
object, as today.

## 4. Locators

From [Reading values from headers vs the body](https://github.com/jsdrews/tui-builder/issues/10).
Ships in PR 3.

A **locator** says where a response-level value is read from:

| Form | Reads |
|---|---|
| `a.b.0.c` | a dot-path into the raw response body (before `root:`) |
| `header:X-Total-Count` | a response header; the name matches case-insensitively; first value if repeated |
| `link:next` | the URL of the `Link` header entry with that `rel`; relative URLs resolve against the request URL |
| `last:id` | a dot-path into the **last item** after `root:` (Stripe's `starting_after`, Discord's `before` / `after`) |

**Where they're allowed:** response-level fields (`total_path`,
`next_path`, `cursor_path`, `error_path`) take any locator. Item-level
fields (`window.cursor`, `find.position`, `mark_key`, a column's
`value:`) take body paths only; a `header:`, `link:` or `last:` there
is a load error.

**Absence:** a missing header, body field, `Link` entry or last item
(an empty page) means "not there" (no total, no next page). A total that doesn't parse as an
integer counts as missing, not an error. A `Link` entry with
`results="false"` (Sentry) counts as absent.

There's no separate `link_header` strategy: `strategy: link` with
`next_path: link:next` covers GitHub, GitLab, Okta, Shopify and Sentry.

## 5. Errors and statuses

From [Error and status handling](https://github.com/jsdrews/tui-builder/issues/11).
Ships in PR 3.

**Statuses as data:** `empty_on: [404, 410]` answers those statuses
with the empty result (`null`) instead of an error. A component shows
nothing; a join lookup finds no match.

**204 and empty bodies** are always the empty result: `null` for JSON,
`""` for `format: text`. There's no setting.

**The message:** `error_path:` is a locator. When it's unset, a non-2xx
response tries, in order: `message`, `error.message`, `error` (if a
string), `error_description`, `detail`, `title`, `errors.0.message`,
then the first 200 bytes of the body.

**Errors on a 2xx:** only an explicit `error_path:` turns a 2xx into an
error, when it resolves non-empty. The built-in guesses never apply to
a 2xx. `graphql:` sets `error_path: errors.0.message` by default.

**What you see:**
- Statusbar and `wrangl`: `pods: HTTP 404 Not Found — <message>`.
- The output console's detail: the method, the URL with secrets masked
  (see [§7](#7-auth)), and the raw body snippet.
- `wrangl` otherwise unchanged: stderr and exit 1; streams emit an
  `{"error": …}` line.

**Partial data:** if `root:` still finds data alongside the error, the
data is kept and the error reported too, as a merge with `on_error:
skip` does today.

## 6. Pagination

From [Paginate strategies and stop conditions](https://github.com/jsdrews/tui-builder/issues/13)
and [GraphQL shorthand and body-carried cursors](https://github.com/jsdrews/tui-builder/issues/16).
Ships in PR 4.

`strategy:` is required, and each strategy accepts only its own fields;
a field from another strategy is a load error.

| Strategy | Request side | Response side | Stops when |
|---|---|---|---|
| `link` | follows the next URL as sent | `next_path` (locator) | the next URL is empty |
| `cursor` | `cursor_param` (query) or `patch:` | `cursor_path` (locator) | the cursor is empty or missing |
| `offset` | `offset_param`, `limit_param` or `patch:` | `total_path` (optional) | the total is reached, or a page is empty |
| `page` | `page_param`, `limit_param` or `patch:` | `total_path` (optional) | the total is reached, or a page is empty |

`strategy: page` also takes **`page_start:`**, the number of the first
page: `1` by default, `0` for zero-based APIs such as Algolia.

Shared by every strategy:

- **`page_size`** sets the limit.
- **`more_when:`** an expression over the raw response, in the language
  of `busy_when` / `growing.while`: `has_more` (Zendesk), `"!isLast"`
  (Jira), `"page + 1 < nbPages"` (Algolia). False stops the walk.
- **`stop_on_short_page: true`** opts in to stopping on a page shorter
  than `page_size` (Discord). An empty page never stops a `link` or `cursor`
  walk (Graph, Google, DynamoDB, Slack).
- **`max_pages`** defaults to 20.
- **The repeat guard** stops the walk, with a console warning, when the
  next URL or cursor equals the previous page's, or an `offset` / `page`
  walk returns identical items (json-server v1 clamps out-of-range
  pages).
- **`on_page_error: fail | skip`** is unchanged, and applies once a
  page's retries ([§8](#8-retries-rate-limits-and-conditional-get)) run
  out.

Names are shared with `window:` where the meaning is the same
(`offset_param`, `limit_param`, `page_size`, `total_path`), so a source
can move between walking every page up front and a window.

**`patch:`** writes paging values anywhere in the request, reusing the
window's patch:

- It applies to every request, the first included.
- With no body it goes into the query string; otherwise it deep-merges
  into `json:` (or `graphql:`'s built body) or merges into `form:`.
- Its tokens are `${paginate.cursor}`, `${paginate.offset}`,
  `${paginate.page}` and `${paginate.limit}`. Exact-token strings keep
  their type, so `search_after` stays an array, and an empty value
  drops its key, so the first request carries no cursor.
- `cursor_param`, `offset_param` and `page_param` are query-only
  shorthand for it.
- Load errors: a shorthand and `patch:` writing the same key; `patch:`
  with raw `body:`; `patch:` on `strategy: link`.

```yaml
notion_pages:
  extends: notion
  url: /databases/${params.db}/query
  json: {page_size: 100}
  root: results
  paginate:
    strategy: cursor
    cursor_path: next_cursor
    more_when: has_more
    patch: {start_cursor: "${paginate.cursor}"}
```

## 7. Auth

From [Auth: schemes, placement and redaction](https://github.com/jsdrews/tui-builder/issues/14).
Ships in PR 5.

`auth:` holds exactly one scheme; none or two is a load error.

```yaml
auth: {bearer: ${env.GH_TOKEN}}
auth: {basic: {user: ${env.U}, pass: ${env.P}}}
auth: {api_key: {in: header, name: X-Api-Key, value: ${env.K}}}   # in: header | query | cookie
auth: {oauth2: {token_url: /oauth/token, client_id: ${env.ID}, client_secret: ${env.SECRET}, scopes: [read]}}
```

| OpenAPI security scheme | `auth:` |
|---|---|
| `http` bearer / basic | `bearer` / `basic` |
| `apiKey` (header, query, cookie) | `api_key` |
| `oauth2` clientCredentials | `oauth2` |
| any other OAuth flow, OIDC | `bearer: ${env.TOKEN}` |
| `security: []` | `unset: [auth]` |

**Placement:**
- Auth owns `Authorization` (bearer, basic, oauth2), the key's `name`
  (header `api_key`), or `Cookie` (cookie `api_key`). Setting that
  header in `headers:` too is a load error. A source may set it only
  after `unset: [auth]`.
- A query `api_key` is the outermost query layer: it's set last on every
  request, followed next URLs included. Setting that key in `url:` or
  `query:` is a load error.

**OAuth2 client credentials:**
- `token_url` may be relative to the URL base.
- Client credentials go in the form body, or with `client_auth: basic`
  in an `Authorization` header.
- The token is cached in memory per process, keyed by token URL, client
  and scopes, and shared across sources.
- It refreshes 30s before `expires_in`, or never if that's absent.
- On a 401 it refetches the token once and retries the request once.
  This is separate from `retry:`, which never retries a 401.
- A failed token request fails the source: `source: auth: token request
  failed — HTTP 401 …`.

**Websocket:** `bearer`, `basic`, and header or query `api_key` apply
to the handshake. `oauth2` on a websocket is a load error.

**Missing secrets:** an empty value fails the source when it runs, not
at load, so one missing token doesn't block the whole config: `source:
auth: bearer is empty (${env.GH_TOKEN} is unset)`.

**Redaction:** every resolved `auth:` value is a secret, and so is any
`headers:` value named `Authorization`, `Proxy-Authorization` or
`Cookie`, or matching `*token*`, `*key*` or `*secret*`
(case-insensitive). Secrets show as `•••` in error messages, the
console detail (including auth's query value in URLs), logs and
response snippets. `--describe` prints templates as written and masks
literal values.

## 8. Retries, rate limits and conditional GET

From [Retry, rate limits and conditional GET](https://github.com/jsdrews/tui-builder/issues/15).
Ships in PR 6.

```yaml
retry:
  max: 3            # attempts, including the first
  backoff: 500ms    # base of the exponential curve
  max_backoff: 30s  # default
  max_wait: 60s     # default; a longer server-requested wait fails now
  on: [429, 503]    # optional; replaces the default status list
```

**Opt-in:** no `retry:`, no retries. A preset turns it on for a whole
API.

**What's retried:** network errors, timeouts, and 408, 429, 500, 502,
503 and 504. `on:` replaces the status list; network errors and
timeouts always stay in, and 401 never is. Every method is retried,
POST included, because a source is a read.

**When:** exponential backoff with full jitter, capped at `max_backoff`.
The server's timing wins over the curve:

1. `Retry-After`, in seconds (fractional allowed) or as an HTTP-date.
2. `X-RateLimit-Reset`, `X-Rate-Limit-Reset` or `RateLimit-Reset`, when
   the matching `*Remaining` header is `0`. A value over 10⁹ is epoch
   seconds; anything smaller is a delay.

A 403 with `Remaining: 0` counts as a 429 (GitHub). The header names
are built in. A wait longer than `max_wait` fails at once with `source:
rate limited until 14:32:05 (HTTP 429)`.

**Budgets:** `timeout:` stays per attempt, and waits don't count toward
it. Each request is retried on its own: every page of a walk, and every
window edge or seek fetch.

**Polling:** a retrying fetch is still out, so `refresh:` ticks are
skipped. Each retry writes a console line (`retrying 2/3 in 4s — HTTP
503`), and the last value stays on screen.

**Conditional GET:** automatic for single-request http sources, with
`conditional: false` to opt out. `ETag` and `Last-Modified` are stored
against the exact resolved URL and body. A 304 emits nothing, so the
last value stands with its focused row and scroll, and any error
clears. It's off for paginate walks and windows. `r` still sends the
conditional headers.

**Size cap:** `max_body:` (default `50MB`) applies per page. Over it,
the source fails with `source: response larger than 50MB (max_body)`.

OpenAPI doesn't describe retries or rate limits, so none of this has to
map.

## 9. GraphQL

From [GraphQL shorthand and body-carried cursors](https://github.com/jsdrews/tui-builder/issues/16).
Ships in PR 7.

`graphql: {query, variables}` builds `json: {query, variables}`,
defaults to POST and to `error_path: errors.0.message`.

- It's mutually exclusive with `json:`, `form:` and `body:`.
- `variables` is templated like `json:`. `query` is literal and never
  templated, so values reach it only through variables.
- Window patches and `paginate.patch` deep-merge into the built body.
- Every path, `root` included, reads the raw response: there's no
  `data`-relative shortcut.

Relay pagination needs nothing GraphQL-specific:

```yaml
repos:
  extends: github_graphql
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

## 10. Validation and `--describe`

From [Validation errors and --describe output](https://github.com/jsdrews/tui-builder/issues/77).
Each PR ships its own rows.

**Load-time errors**, in the loader's `path: message` style:

| Rule | Example message | PR |
|---|---|---|
| Preset: unknown `extends:`, a cycle, a `type:` mismatch, `pipe:` inside it, `unset:` naming an unknown field | `presets.a: extends cycle a → b → a` | 1 |
| Unknown key in a strictly decoded block | `sources.x.paginate: unknown field "cursor_pram" (did you mean cursor_param?)` | 1 |
| A token naming an undeclared parameter (after merging; new, nothing checked this before) | `data.sources.x: ${params.region} names no parameter declared under parameters:` | 1 |
| Owned query key also in `url:` / `query:` | `sources.issues.query: "page" is set by paginate (page_param)` | 2 |
| More than one of `json:` / `form:` / `body:` / `graphql:` | `sources.x: json and body are mutually exclusive` | 2, 7 |
| `header:` / `link:` / `last:` locator on an item-level field | `sources.x.window.cursor: header: locators only work on response-level fields` | 3 |
| `paginate.strategy` missing, or a field from another strategy | `sources.x.paginate: cursor_param belongs to strategy cursor, not offset` | 4 |
| `patch:` and a shorthand writing the same key; `patch:` with `body:`; `patch:` on `link` | `sources.x.paginate.patch: "after" is also set by cursor_param` | 4 |
| `auth:` with zero or two schemes; `oauth2` on a websocket | `sources.x.auth: set exactly one of bearer, basic, api_key, oauth2` | 5 |
| Owned header also in `headers:` | `sources.issues.headers: "Authorization" is set by auth.bearer` | 5 |
| Durations (`backoff`, `max_backoff`, `max_wait`) and sizes (`max_body`) that don't parse | as today's `timeout` | 6 |

**Strict decoding:** `auth`, `retry`, `paginate`, `graphql` and every
preset decode strictly, with a did-you-mean suggestion. The rest of
the config keeps ignoring unknown keys.

**Load or run time:** structure, conflicts, parsing, preset merges and
tokens are checked at load. Owned-key checks are case-insensitive for
headers and exact for query keys; keys are never templated, so all of
them can run at load. Anything that needs a resolved value (an empty
secret, a locator that doesn't resolve, a token request) is checked
when the source runs. An empty cursor ends a walk rather than failing.

**`--describe`** on a source that extends a preset shows the merged
result:

```
issues
  Kind:       source / http
  Extends:    github → github_org
  Lifecycle:  one-shot
  URL:        https://api.github.com/repos/${params.repo}/issues
  Headers:    Accept: application/vnd.github+json (from github)
  Auth:       bearer ${env.GITHUB_TOKEN} (from github_org)
  Paginate:   link (max 20 pages)
  Retry:      3 × 500ms (from github)
  Parameters:
    …
```

Inherited fields get `(from <preset>)`. `Method`, `Query`, `Headers`,
`Body: json|form|raw|graphql`, `Auth`, `Paginate`, `Retry` and `Window`
lines appear only when set.

## Implementation order

From [Split the spec into implementation PRs](https://github.com/jsdrews/tui-builder/issues/78).

| # | PR | Sections | Needs |
|---|---|---|---|
| 1 | **Presets**: `extends:`, merging, `unset:`, the strict-decode and did-you-mean helper, presets in `--describe` / `--list` | 1, 10 | — |
| 2 | **Query and bodies**: list-valued `query:`, owned keys, URL token escaping, `json:` / `form:`, the method default, the default `User-Agent`, patch merging | 2, 3 | 1 |
| 3 | **Locators and errors**: `header:` / `link:` / `last:`, `empty_on`, `error_path` and its guesses, the error format, partial data | 4, 5 | 1 |
| 4 | **Paginate**: `cursor` / `offset` / `page`, `page_start`, `more_when`, guards, `patch:` | 6 | 2, 3 |
| 5 | **Auth and redaction**: the four schemes, owned keys, the websocket handshake, masking | 7 | 2 |
| 6 | **Resilience**: `retry:`, rate-limit headers, conditional GET, `max_body` | 8 | 1 |
| 7 | **GraphQL shorthand** | 9 | 2, 3, 4 |

After presets, PRs 2, 3 and 6 can run in parallel.

**Each PR ships** its rows of the load-error table and strict decoding
for its block, `httptest`-based tests, updates to
`docs/reference/sources.md` and `docs/guide/data.md`, and `--describe`
lines for its fields. **Examples:** presets rework `http_github.yaml`
and `http_github_auth.yaml` to extend a `github` preset; auth moves
`http_github_auth.yaml` to `auth: {bearer: ${env.GITHUB_TOKEN}}`;
paginate adds a `cursor` example; GraphQL adds the Relay example. New
behaviour is proven in tests, not in examples that need the network.

## Out of scope

- **Streaming framing** (NDJSON / SSE parsing, `Last-Event-ID`). An
  independent track. Reconnecting from the last line shown shipped as
  `resume:`.
- **Transport and formats**: `tls:`, Unix sockets, CSV / XML. An
  independent track.
- **Window follow-ups**: `page_param` on a window, a separate
  sort-order parameter, a window total from a header.
- **Requiring two auth schemes at once.** Put the second in `headers:`
  or `query:` with `${env.X}`.
- **OAuth flows other than client credentials**, and OIDC discovery.
  Use `bearer: ${env.TOKEN}`.
- **AWS SigV4 signing**, digest auth, cookie or session login flows,
  SOAP, multipart bodies. Use an `exec` source.
- **Strict decoding for the whole config.** A standalone change.
- **wrangl rejecting data-only configs.** A standalone bug.
- **The OpenAPI → tui-builder config skill.** The next effort; it
  consumes this spec.
