# `tui-builder add http <url>`: a config from one endpoint

`add exec` runs a command once, samples its JSON, and prints a source
and a component that shows it. `add http` does the same for one HTTP
endpoint: it makes one GET, samples the response, and prints an http
source (with its auth, paging and parameters) and a component.

Proposed in conversation (no map); **not decided yet**. The open
questions are in [§10](#10-open-questions). Terms follow
[`CONTEXT.md`](../CONTEXT.md). It builds on
[`scaffolding.md`](scaffolding.md) (`add exec`, `--into`) and borrows
from [`openapi-generator.md`](openapi-generator.md) (`gen`).

## Why not `gen`

`gen` stays as it is. The two commands do different jobs:

| | `gen <spec>` | `add http <url>` |
|---|---|---|
| Input | a description of an API | what one real request returns |
| Output | a whole app: preset, a screen per operation, actions, a home | one source and one component, or a one-screen config |
| Who owns the file | the generator: regenerate it, put changes in `--overrides` | you: hand-edit it, `--into` keeps your comments |

`add http` is for an API with no spec, or one endpoint you want in a
config you already have.

## 1. The command

```sh
tui-builder add http [flags] <url>
tui-builder add http https://api.github.com/repos/cli/cli/issues
tui-builder add http --header 'Authorization: Bearer ${env.GH_TOKEN}' --into app.yaml -o app.yaml https://…/issues
```

- One positional, the URL, with its query string. An `http` or
  `https` scheme is required; anything else is a usage error.
- **Output** follows the rule every config-making command has: it prints
  the config, `-o FILE` writes it, `--force` overwrites, and `-o` naming
  the `--into` file edits it in place. The summary and warnings go to
  stderr.
- **Flags shared with `add exec`, meaning the same:** `--into`,
  `--place`, `--name`, `--title`, `--columns`, `--max-columns`,
  `--no-style`, `--component`, `--root`, `--refresh`, `--timeout`,
  `--param`, `--no-inputs`, `--no-run`, `--follow` / `--no-follow`,
  `--window`, `--total-path`, `--cursor-path`.
- **Its own:** `--header 'Name: value'` (repeatable), `--user USER[:PASS]`
  (basic auth), `--no-paging` (sample one page and write no `paginate:`
  or `window:`), and `--max-pages N` for a walk.
- The default `--name` comes from the URL's last path segment that isn't
  an id (`/repos/cli/cli/issues` → `issues`; `/v1/things/42` → `things`).

## 2. The request

- **One GET** with `Accept: application/json`, `User-Agent:
  tui-builder/<version>`, the `--header`s, and the auth from §3. It
  follows redirects and is bounded by `--timeout` (default 10s, as the
  http source's) and `config.DefaultMaxBody`, as `add exec`'s sample is.
- **`${env.X}` in the URL or a header** is expanded for the request and
  written to the config as written, so a secret never reaches the file.
- **A non-2xx answer** fails with the status and the first line of the
  body, and nothing goes to stdout. A 401 or 403 adds a hint to pass
  `--header` or `--user`.
- **Not JSON:**
  - a `text/event-stream` or NDJSON content type, or `--follow`, is a
    stream (§6);
  - `text/*` is a text source with a logview, as `add exec` does for
    text;
  - anything else is an error naming the content type.
- **`--no-run`** makes no request. It writes the source with an
  `auto: true` inspector, as `add exec --no-run` does.
- **Only GET in this plan.** A POST search, GraphQL and websockets are
  out of scope (§9).

## 3. Auth and secrets

**The rule:** no credential value is ever written to the config. Each
one becomes `${env.NAME}`, with an `app.env` entry declaring it
`required` with a description. Stderr says what to export. `NAME` is
the source name upper-cased plus a suffix: `ISSUES_TOKEN`, `ISSUES_USER`,
`ISSUES_PASS`, `ISSUES_API_KEY`.

| On the command line | Written as |
|---|---|
| `--header 'Authorization: Bearer abc'` | `auth: {bearer: "${env.ISSUES_TOKEN}"}` |
| `--header 'Authorization: Basic …'` or `--user u:p` | `auth: {basic: {user: "${env.ISSUES_USER}", pass: "${env.ISSUES_PASS}"}}` |
| `--header 'X-Api-Key: abc'` (a header name containing `key`, `token` or `secret`) | `auth: {api_key: {in: header, name: X-Api-Key, value: "${env.ISSUES_API_KEY}"}}` |
| `?api_key=abc` in the URL (`api_key`, `apikey`, `key`, `token`, `access_token`) | `auth: {api_key: {in: query, …}}`, and the value is removed from `url:` |
| a header or query value already `${env.X}` | kept as written, with `X` declared in `app.env` |
| any other `--header` | `headers:`, as written |

- **With `--into`,** if a preset in the config has the same origin
  (scheme, host and port) and a `url` prefix that matches, the source
  `extends:` it and writes only the rest of the path. It inherits the
  preset's auth, so no `--header` is needed. One matching preset is
  used; with more than one, the longest `url` wins.
- **Without a preset,** the source is written with the full `url:`.
  Making a preset is out of scope for this plan.

## 4. From the response to a source and a component

This is the same work as `add exec`, done on the response body. It
moves into a shared core first (see [§8, PR 1](#8-prs)).

- **`root:`, the columns, their ranking, status colours and the
  component** are inferred as for `add exec`, with `--root`,
  `--columns` and `--component` taking precedence.
- **A single object** gets an `auto: true` inspector.
- **`--param NAME=VALUE`** marks a query value or a path segment equal to
  `VALUE` as the parameter `NAME`, with an input field as for `add exec`.
  A path segment becomes `${params.NAME}` in `url:`. A query value moves
  into `query:` as `NAME: ${params.NAME}`.
- **The other query parameters** move from the URL into `query:`, so
  they're easy to edit. Paging parameters are the exception: they go to
  the `window:` or `paginate:` that uses them (§5).

## 5. Paging, read from the live response

`gen` finds paging in the spec. `add http` has only one response, so it
looks at the URL's query, the response headers and the body. The
name lists `gen` uses (`pageNames`, `sizeNames`, `offsetNames`,
`totalNames`, `nextNames`, `cursorNames`, `cursorIn`, `searchNames`,
`sortNames` in `internal/gen/paging.go`) move to a package both can
import. The first rule that matches wins:

1. **A seekable window.** The body has a total (`totalNames`, or
   `--total-path`), and the URL has a page or offset parameter and a
   size parameter. With `page=`, `per_page=` and `total`, that writes:

   ```yaml
   window:
     page_param: page        # or offset_param
     limit_param: per_page
     total_path: total
   ```

   A missing page or offset parameter with a size and a total still
   counts: it writes `page_param: page` with a warning to check it.
   The component is a table (or an eventlog with `--component
   eventlog`).
2. **A Link-header walk.** The response has `Link: <…>; rel="next"`
   (GitHub, GitLab): `paginate: {strategy: link, next_path:
   "link:next"}`.
3. **A next-URL walk.** The body has a next URL (`nextNames`):
   `paginate: {strategy: link, next_path: <path>}`.
4. **A cursor walk.** The body has a next cursor (`cursorNames`), and
   the URL has a matching parameter (`cursorIn`): `paginate:
   {strategy: cursor, cursor_path: <path>, cursor_param: <param>}`.
5. **Nothing found:** one request, with no paging.

- A walk writes no `max_pages:`, so the source's cap of 20 pages applies.
  `--max-pages N` writes it. Stderr says the walk is capped.
- **A search parameter** already in the URL (`searchNames`) becomes
  `search_param` on a window, which makes the table filterable. A sort
  parameter (`sortNames`) becomes `sort_param`. Otherwise the table
  filters and sorts locally.
- **`--window ROLE=PARAM`** names roles the inference can't see: `page`,
  `offset`, `limit`, `search` or `sort`, each mapped to a query
  parameter (`--window page=p --window limit=n`). It differs from `add
  exec`'s `--window TOKEN=VALUE`, which marks an argument's value; the
  difference is in the help text. `--window` turns rule 1 on and skips
  the others.
- **`--no-paging`** skips all of this.
- **No second request is made** to confirm a window (see
  [§10](#10-open-questions)).

## 6. Streams

- A `text/event-stream` or NDJSON response, a response still open after
  the sample time, or `--follow` is sampled the way `add exec` samples a
  stream. It writes `follow: true`, and a keyed table or a logview, by
  the same rules.
- `--follow` together with a window or paging is a usage error, as on
  `add exec`.

## 7. What's shared, and where

| Piece | Today | After |
|---|---|---|
| Sample → root, columns, ranking, component | `internal/scaffold` (`exec.go`, `infer.go`, `rank.go`) | the same package, behind a function both `add exec` and `add http` call with the sampled bytes |
| `--into` splice | `scaffold.SpliceInto` | unchanged; gains an `extends:` hook for §3's preset match |
| Paging name lists | `internal/gen/paging.go` | a small package both `gen` and `scaffold` import (`internal/apinames`, name to be decided) |
| The request | — | new, in `scaffold`, built on `internal/datasource`'s http client so the headers and redirects match what runs |
| The CLI | `cmd/tui-builder/add_exec.go` | `add_http.go` beside it, reusing the shared flags through a helper |

## 8. PRs

1. **Factor the shared core out of `add exec`.** Move the work from
   sample bytes to source and component behind one function, with no
   change in behaviour: the `add exec` goldens stay byte-for-byte the
   same.
2. **`add http`, one page.** The request, §3's auth and secrets, `query:`,
   `--param`, `--into` with the preset `extends:` match, and `--no-run`.
   Goldens against `internal/fixture/things` (bearer token, a list and a
   detail) and an `httptest` server per auth scheme.
3. **Paging.** Move the name lists, then §5's rules. Each rule gets a
   fixture: things for a seekable window, a Link-header server, a
   next-URL server and a cursor server. An e2e test loads each golden
   and reads two pages through `wrangl`'s data layer.
4. **Streams.** §6, against an SSE and an NDJSON `httptest` server.
5. **Docs.** A `tui-builder add http` section in
   `docs/cli/tui-builder.md` (`TestDocsListEveryFlag` requires it), a
   line in the README and the quick start, and the manual-testing
   guide.

## 9. Out of scope

- **A bridge to a spec**, such as `add http --spec openapi.yaml
  --operation listThings --into app.yaml`, which adds one operation's
  screen from a spec to a config you already have. It's a natural next
  step, since it reuses `gen`'s per-operation code, and gets its own
  plan.
- **Making a preset** from the first source on an origin.
- POST searches, request bodies, GraphQL, websockets and oauth2: each
  needs more than one sample can show.
- Actions (writes). `gen` makes those from a spec; one GET can't see
  them.

## 10. Open questions

1. **Confirming a window with a second request.** Fetching page 2 (or
   `offset + limit`) would show whether the page parameter really moves
   the page, at the cost of a second request to someone's API.
   *Proposed:* no second request; warn when the window was guessed
   rather than seen, and let `--window` settle it.
2. **The name of `--window`'s role form.** Should it reuse `--window`, or
   use a separate flag (`--page-param`, `--limit-param`, …), as the
   source's own fields are named? *Proposed:* reuse `--window
   ROLE=PARAM`, so both `add` commands have one flag for paging.
3. **Secrets in a header value that isn't recognised.** A custom header
   with a credential whose name doesn't say so (`X-Session: …`) would
   be written as-is. *Proposed:* warn on any header value longer than 20
   characters with no spaces, and point at `${env.X}`.
4. **`add http --into` with no matching preset, but other sources on the
   same origin.** Should it offer to make a preset, or only note it?
   *Proposed:* a note on stderr only, since making a preset is out of
   scope.
