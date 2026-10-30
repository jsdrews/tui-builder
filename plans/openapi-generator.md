# OpenAPI generator: `tui-builder gen`: the locked spec

`tui-builder gen <spec>` turns an OpenAPI 3.0 / 3.1 document (a file, a
URL or stdin; JSON or YAML) into a tui-builder config that loads and runs
against the API without hand edits. A separate overrides file, merged in
on every run, says what the spec can't: hidden filters, refused sorts,
other columns, titles and groups, secrets' `from:`.

It's locked: every decision was made on the map
[Map: OpenAPI → tui-builder config generator](https://github.com/jsdrews/tui-builder/issues/113).
Each section links the ticket that holds its detail and reasoning; where
a later ticket refined an earlier one, this plan states the result.
Seven PRs, in the [order at the bottom](#implementation-order).

Terms follow [`CONTEXT.md`](../CONTEXT.md) (Source, Preset, Window,
Seekable, Anchored, Action, Binding, Action group, Menu, Comparison). No
new terms: "list operation", "detail operation", "singleton" and
"top-level screen" are the generator's working words, not names in the
config.

**The running example** is the fixture in
[`internal/fixture/things`](../internal/fixture/things): a FastAPI-style
spec (`things.Spec`, 8 operations, tags `things` and `owners`, bearer
auth, `page` / `per_page` / `order_by`, `{total, page, per_page,
entries}` envelopes) and a stub server (`things.New()`, token
`fixture-token`) that also answers undocumented `__icontains` filters
and refuses `order_by=status` with a 400. `go run
./internal/fixture/things/thingsd` serves it on `localhost:8765`. From
[Write the FastAPI-style fixture spec and stub server](https://github.com/jsdrews/tui-builder/issues/116).

## 1. Parsing

From [Which Go library parses OpenAPI](https://github.com/jsdrews/tui-builder/issues/114);
findings in `plans/research/openapi-library.md` on the local branch
`research/openapi-library`.

- **kin-openapi** (`github.com/getkin/kin-openapi/openapi3`, v0.152.0,
  MIT). It loaded GitHub's REST spec (3.0 and 3.1) and Jira's v3 from a
  file and a URL, on Go 1.25, with the fewest extra modules.
- **Its limits, and what the generator does about them:**
  - paths and properties come back as Go maps → order them by
    `IncludeOrigin` line numbers, so output follows the spec;
  - strict `Validate` rejects real specs → never call it;
  - recursive schemas are pointer cycles → the schema walker keeps a
    visited set;
  - a broken `$ref` errors without file/line → wrap the error with the
    ref string;
  - URL fetch has no timeout or headers → set `ReadFromURIFunc` with
    `--timeout` and `--header`.

## 2. The command

From [The generator's name and command line](https://github.com/jsdrews/tui-builder/issues/117).
Builds on the Cobra root from the scaffolding plan's
[subcommands](scaffolding.md#1-subcommands), which reserved `gen`.

- **Input:** one positional, a file path, an `http(s)://` URL, or `-`
  for stdin; JSON or YAML detected from the content. URLs are fetched
  with a 30s timeout (`--timeout`); `--header 'Name: value'`
  (repeatable) for specs behind auth. Header values never reach the
  config.
- **Output:** stdout by default; `-o <path>` writes a file and refuses
  an existing one without `--force`. Always one file: presets,
  sources, screens, actions. The result goes through `config.Load`
  (with stand-ins for required env vars) before anything is written; a
  failure is a generator bug: exit 1, nothing written, the load error
  shown.
- **Selection:** `--tag` / `--exclude-tag` and `--operation` /
  `--exclude-operation` (operationIds, globs like `list_*`),
  repeatable; includes narrow first, then excludes remove.
  `--read-only` generates no actions. `--include-deprecated` keeps
  `deprecated: true` operations. Default: everything.
- **Report:** a stderr summary (counts of screens, sources, actions)
  then one line per skipped operation with its reason (`skipped POST
  /files: multipart body`); `--quiet` silences it. The same list is a
  comment block at the top of the config, under a header naming the
  spec, the exact command, and the overrides file to edit instead of
  the output.
- **Exit codes:** 0 written (skips aren't failure); 1 error (can't fetch
  or parse, nothing generatable, existing output without `--force`,
  failed `Load`, an invalid overrides file); 2 usage.
- **Other flags,** from later sections: `--name`, `--base-url`,
  `--prompt-server`, `--layout tabs|menu`, `--overrides`,
  `--prune-overrides`, `--init-overrides`.
- **Deterministic:** the same spec, flags and overrides give
  byte-identical output. Fixed key order; no timestamps.
- **Docs:** a `gen` section in `docs/cli/tui-builder.md`, kept complete
  by the docs-flags test.

## 3. Preset, servers and auth

From [Servers, security and credentials](https://github.com/jsdrews/tui-builder/issues/122),
on top of the document-level table in
`plans/research/openapi-mapping-v2.md` (branch
`research/openapi-mapping-v2`), from
[Recheck the OpenAPI mapping against tui-builder as built](https://github.com/jsdrews/tui-builder/issues/115).

- **`app`:** `title` from `info.title`, `version` from `info.version`.
- **One preset** for the whole API, named from `info.title` in
  snake_case (`things_api`); every source and action `extends:` it. The
  env prefix is the name upper-cased (`THINGS_API_*`). `--name`
  overrides both. A path or operation with its own `servers` gets an
  absolute `url:` on the same preset's sources; never a second preset.
- **Base URL:** `url: ${env.<API>_URL}`, with an `app.env` entry
  (`required: false`) whose `default` is `servers[0]` with its
  variables filled from their defaults. Server variables are never
  `${params.*}`: actions can't extend a preset that uses params.
  - Several servers: an `app.prompts` select (each URL, the first as
    default) only with `--prompt-server`; otherwise the first, the rest
    listed in a comment.
  - A variable with an `enum`: its default in the URL, the allowed
    values in a comment.
  - A relative server URL is resolved against the spec's URL; from a
    file or stdin the generator stops and asks for `--base-url`. No
    `servers`: `--base-url`, or the spec URL's origin.
- **Schemes:**

  | Scheme | `auth:` |
  |---|---|
  | `http` bearer | `{bearer: "${env.X_TOKEN}"}` |
  | `http` basic | `{basic: {user: "${env.X_USER}", pass: "${env.X_PASSWORD}"}}` |
  | `apiKey` (header, query, cookie) | `{api_key: {in, name, value: "${env.X_API_KEY}"}}` |
  | `oauth2` clientCredentials | `{oauth2: {token_url, client_id, client_secret, scopes}}`, secrets from env |
  | `openIdConnect` | discovery fetched at generation time (URL specs only) for `token_url` |
  | `oauth2` authorization code, implicit, password, device | `{bearer: "${env.X_TOKEN}"}` plus a comment: how the API signs in, get a token another way and set `from:` in overrides |
  | `mutualTLS`, unknown | skipped with a reported reason; no auth, and a summary warning that requests will likely 401 |

- **Requirements:** OR (alternatives) → the first, the rest in a comment
  (overrides' `gen.security` picks another). AND → the first in `auth:`,
  the rest as `headers:` / `query:` from `${env.*}`. An operation's
  `security: []` → `unset: [auth]`; a different scheme on an operation →
  its own `auth:`.
- **Secrets in `app.env`:** one entry each, `required: true`,
  `description` from the scheme's or "Bearer token for <title>", then
  commented `from: {command: …}` and `from: {file: …}` examples. Never
  a value.
- **Preset defaults:** `retry: {}` when any operation documents a 429 or
  `Retry-After` / `X-RateLimit-*` headers. Nothing for conditional GET
  (on by default); no `error_path` (the built-in guesses read FastAPI's
  `detail`).
- **The fixture:** `things_api: {url: "${env.THINGS_API_URL}", auth:
  {bearer: "${env.THINGS_API_TOKEN}"}}`; `THINGS_API_URL` defaults to
  `http://localhost:8765`; `THINGS_API_TOKEN` is required. Run with
  `THINGS_API_TOKEN=fixture-token tui-builder things.yaml`.

## 4. Operations to screens

From [Which operations become screens](https://github.com/jsdrews/tui-builder/issues/118).

- **List operation:** a GET whose 200 (or first 2xx) JSON response is
  - an array → no `root:`;
  - an object with exactly one array property, after merging `allOf`
    and resolving `$ref` (FastAPI's `Page[T]`) → `root:` that property;
  - an object with several arrays → the one named `entries`, `items`,
    `data`, `results`, `records` or `values`; else skipped ("several
    arrays — set `root` in overrides").

  An array of scalars is a one-column table. No response schema →
  skipped.
- **Detail operation:** `GET /things/{x}` pairs with `GET /things` when
  its path is the list's plus one parameter segment and it returns an
  object. `{x}` matches a row property by name (`thing_id`, `id`,
  `<singular>_id`), else `id`; no match → the detail is a screen nobody
  reaches, reported "can't tell which column fills `{x}`". It's a
  pushed screen with an `auto: true` inspector; the list binds `enter`
  → push with `bind: {x: "${selection.<IdTitle>}"}`, keeping the id
  column (hidden if need be).
- **Sub-resource lists** (`/owners/{owner_id}/things`): pushed from the
  parent list's row, bound the same way, on the next free key from the
  child's name (`t`, …), also in the action menu. Never pushed from the
  detail screen (see [Follow-ups](#follow-ups)). When the parent list
  isn't in the output, see [Navigation](#5-navigation).
- **Singleton** (`GET /me`, `/health`: an object, no path params): a
  screen with an `auto: true` inspector.
- **A required query parameter, no parent:** an `app.prompts` entry
  from its schema (type; enum → `options:`), read as `${env.*}`.
  Optional query parameters are the windows' business (section 6).
- **A required path parameter with no list at all:** skipped, "`{org}`
  has no list to pick it from — add one in overrides".
- **Skipped, one reason line each:** non-JSON responses
  (`text/event-stream`, `application/octet-stream`, images,
  `text/csv`), file downloads, deprecated operations (unless
  `--include-deprecated`), a required header or cookie parameter,
  callbacks and webhooks. Operations deselected by flags get no line.
- **Names:** one screen per list, singleton and detail, named from the
  path (`/things` → `things`, `/things/{thing_id}` → `thing`,
  `/owners/{owner_id}/things` → `owner_things`); a clash adds the method
  or operationId.

## 5. Navigation

From [Navigation: menu, screens and tags](https://github.com/jsdrews/tui-builder/issues/123).
A **top-level screen** is a list or singleton screen; details and
sub-lists are reached only by push. Always `tui.screens:`, since details
are pushed.

- **Layout by count:**
  - 1 top-level screen → it's `initial`; no home screen.
  - 2–5 → a home screen with `tabs:`, one tab per top-level screen in
    spec order, each holding that screen's layout and its `n` / `e` /
    `d` bindings. Tags are ignored.
  - 6+ → a home screen with a `type: menu`; each top-level screen its
    own screen.
  - `--layout tabs|menu` overrides the threshold. The fixture (Things,
    Owners) gets tabs.
- **Menu items:** one per top-level screen. `label`: the resource name
  ("Things"), or the `summary` for a singleton. `description`: method
  and path (`GET /things`). `group`: the first tag, groups in the spec's
  `tags:` order; untagged → no group (the menu shows `other`). Several
  tags → once, under the first. Tag descriptions as a YAML comment above
  their group's items.
- **Titles:** home: `info.title`. Lists and tab labels: the resource
  name. Details: "Thing ${selection.Name}" when the list has a
  name-like column, else "Thing ${selection.Id}" (the bound id).
  Sub-lists: "${selection.Name}'s things".
- **Huge specs:** no cap; the menu's `/` filter and `[` / `]` group
  jumps handle it. Past 40 top-level screens, the summary suggests
  `--tag` with a count per tag; past 200 operations, the same plus a
  note that everything was generated. Nothing is dropped silently.
- **Orphan sub-lists** (the parent list excluded or absent): top-level,
  with the path parameter as a required `app.prompts` entry
  (`<API>_<PARAM>`) read as `${env.<API>_<PARAM>}` in the URL.
- **Action groups:** `group:` left to its defaults (push → `read`,
  writes → `write`); `app.action_groups: [read, write]` only when some
  screen has both.

## 6. Lists to windows

From [Lists to windows: paging, sort and filter from the spec](https://github.com/jsdrews/tui-builder/issues/119).

- **Which form:**
  - **seekable `window:`**: page-number or offset params *and* a
    total (a body property, or a declared `X-Total-Count` header →
    `total_path: "header:X-Total-Count"`);
  - **`paginate:` walk**: a cursor, a next link (body or `Link`
    header), or page/offset with no total; `max_pages: 20`, the summary
    noting "walks up to 20 pages; raise `max_pages` in overrides"; no
    total → `stop_on_short_page: true`;
  - **one request**: no paging params.
  - **Anchored windows are never generated** (a spec can't say both
    directions exist); overrides only.
- **Paging params,** by name, on integer query params:
  - page: `page`, `page_number`, `pageNumber`, `p`; `page_start` from
    its `minimum`, default 1;
  - size: `per_page`, `page_size`, `pageSize`, `limit`, `size`,
    `count`, `top`, `$top`; `page_size` = its `maximum` if ≤ 100, else
    100; no maximum → its `default`, else 100;
  - offset: `offset`, `skip`, `start`, `_start`, `$skip`;
  - total: a top-level integer `total`, `count`, `total_count`,
    `totalCount`, `total_results`, `numFound`, or `meta.total` /
    `meta.count`.

  Two candidates for one role → a walk, with a note.
- **Sort:** `sort_param` from `order_by`, `ordering`, `sort`, `sort_by`,
  `orderBy`, `sortBy`; `sort_fields` from its `enum` (through
  `anyOf`-with-null and `$ref`). `-`-prefixed entries → `sort_desc_prefix:
  "-"`, the pairs collapsing to one field. A value matching a column only
  after snake/camel normalisation → a `sorts:` entry; matching none →
  dropped. No enum → no `sort_fields`, no sortable columns. A lying enum
  is caught at runtime by refused-sort recovery.
- **Search and filters:** `search_param` from a string `q`, `query`,
  `search`, `filter`, `term`. `filters: {Title: param}` for a query
  param named like a shown column's property. Range pairs
  (`created_after`/`created_before`, `min_size`/`max_size`,
  `size_gte`/`size_lte`) → `{gte, lte}` entries. Enum columns get
  `distinct:`. `filterable: true` only with at least one search or
  filter destination.
- **`filter_ops`** is never generated from the spec; the overrides'
  `window:` hint adds it. With it, the generator writes `filter_except`
  from the schema: every non-text column (integer, number, boolean,
  enum, id, date-time). Never `filter_except` alone (a load error).
- **The fixture's `/things`:** `window: {page_param: page, limit_param:
  per_page, page_size: 100, total_path: total, sort_param: order_by,
  sort_fields: [name, created_at, size, status], sort_desc_prefix: "-",
  filters: {Status: status, Owner id: owner_id}}`, `distinct` on
  Status, filterable. `/owners`: the same paging, sort on name, no
  filters, not filterable.

## 7. Columns and the detail inspector

From [Which response properties become columns](https://github.com/jsdrews/tui-builder/issues/120),
which follows the scaffolding plan's
[ranking and style](scaffolding.md#4-inferring-a-source-and-component)
with the schema as evidence instead of a sample.

- **Properties:** from the item schema after `$ref` and `allOf`.
  Scalars; nested objects flattened to dot-paths two levels deep
  (`labels.team`); arrays of scalars as one comma-joined column.
  Skipped: arrays of objects, maps (`additionalProperties`), `oneOf` /
  `anyOf` across object shapes, `writeOnly`. `anyOf`-with-null unwraps;
  `readOnly` is kept.
- **Ranking:** identity (the property the detail binds, else `name`,
  `title`, `id`) → status (any `enum`, or a status-like name) → time
  (`date-time` / `date`) → numbers → short strings → long text
  (`description`, `body`, `notes`, `message`, `comment`, or `maxLength`
  > 80). Schema order within a rank. **Top 8**, one slot guaranteed to
  the top long-text column (slot 8, displacing the last short string);
  the rest as commented-out lines.
- **The bound column** (usually `id`) is always written: shown in the
  top 8, else `hidden: true`, never commented out. With `name` as the
  identity and a numeric `id`, `name` is bold and `id` shows only if it
  made the cut.
- **Titles:** the property name in words (`owner_id` → "Owner id",
  `createdAt` → "Created at"), not FastAPI's `title`; nested paths use
  the last segment, clashes prefixed by the parent ("Labels team").
  Unique, since filters, sorts and bindings key on titles.
- **Sort and align:** `integer` / `number` → `sort: number`, `align:
  right`. On a window, columns matching `sort_fields` sort; on a walk or
  one request, every column is `sortable: true` (local sort).
- **Style:** bold identity. The status vocabulary matched against the
  enum's declared values → `theme:success` / `theme:warning` /
  `theme:error` colour rules, unknown values plain; booleans coloured
  only when named as a check; failure-count names → `>0` warning.
  `flex: 1` on the long-text column (else the identity), `max_width:
  20` on status. Timestamps raw.
- **Detail screen:** an `auto: true` inspector with `color_rules` and a
  `path:` per enum property from the same vocabulary.
- **`mark_key`:** only off-window (walks, single requests): the
  identity's id property when `required`.
- **The fixture's things table:** Name (bold), Status (active →
  success, pending → warning, failed → error, archived plain;
  `distinct`), Created at, Id, Size, Owner id (right-aligned), Tags,
  Description (`flex: 1`); Labels team and Labels env commented out.

## 8. Writes to actions

From [Write operations to actions](https://github.com/jsdrews/tui-builder/issues/121).

- **Which, and where:** POST / PUT / PATCH / DELETE with a JSON body, a
  form body or none. **Collection actions** (the list's path: `POST
  /things`) on the list screen, no `from:`. **Item actions** (the
  detail path, or it plus a segment: `PATCH /things/{id}`, `POST
  /things/{id}/restart`) on the list screen with `from: <table>`. A
  path matching no list (`POST /login`) is skipped, "no list to attach
  to". None on detail screens (see [Follow-ups](#follow-ups)).
- **Inputs from the body:** `required`; enum → `options:`; constraints →
  `validate:` + `invalid:`; `format: password` / `writeOnly` → `mask:`;
  `description` → `description:`; `example` → `placeholder:`.
  - Spec defaults are never `default:` (an input with one is never
    asked): `placeholder: "default: pending"`.
  - A required nested object or array field skips the operation
    ("`labels` is an object — add it in overrides"); an optional one is
    left out with a note.
  - Optional booleans → `options: ["", "true", "false"]`; numbers →
    string inputs. Both rely on the server coercing strings (pydantic
    does) and carry a YAML comment.
- **Parameters:** path params → inputs with `segment: true`, bound from
  the selection by the detail-push id rule (no matching column → the
  action is skipped); query params → inputs sent through `query:`; a
  required header param → skipped.
- **Edits:** PATCH / PUT bind only path params; every other field is
  asked blank and optional, so a PATCH sends only what was typed. PUT
  keeps the schema's required fields required. Labels "Edit" (PATCH),
  "Replace" (PUT).
- **Confirm:** every DELETE gets `confirm: "Delete
  ${selection.<Identity>}?"`; nothing else by default.
- **Names, labels, keys:** `create_thing`, `update_thing`,
  `replace_thing`, `delete_thing`; others from `summary` in snake_case
  (`restart_thing`). Labels from `summary`. Keys: create `n`, edit `e`,
  delete `d`, then the next free letter.
- **After it runs:** success is `code < 400`; no `error_message`; create
  → `message: "created ${body.<identity>}"`. Every write's message ends
  "(r to refresh)".
- **Bulk:** on a markable table, DELETE and body-less item POSTs get
  `multi: true`. Never on windows.
- **Not generated:** multipart / file uploads (skipped with a reason);
  `202 Accepted` claims (overrides only); anything at all under
  `--read-only`.
- **The fixture's things list:** `n` Create Thing (name required with
  `validate` max 80, owner_id required int, status `options` with
  placeholder "default: pending", size, description; optional `tags`
  left out with a note), `e` Edit (all optional), `d` Delete (confirm).

## 9. Overrides

From [Overrides and additions the spec can't express](https://github.com/jsdrews/tui-builder/issues/124).

- **A separate file, never hand edits.** `--overrides <file>` is merged
  in on every run; the output is always regenerated whole.
- **Two layers in one YAML file:**
  - `gen:`: generator hints keyed by spec identity (`"GET /things"`,
    an operationId, schema property names), so they survive
    regeneration. They change what's generated.
  - Every other top-level key: a tui-builder config fragment (`app:`,
    `presets:`, `sources:`, `actions:`, `tui:`) merged onto the output,
    keyed by generated names. The escape hatch for what the generator
    can't know.
- **`gen:` vocabulary:**

  ```yaml
  gen:
    name: things_api            # every global flag but --header has a key
    layout: tabs
    base_url: http://localhost:8765
    security: HTTPBearer        # pick a non-first alternative
    order: ["GET /owners", "GET /things"]   # menu / tab order
    operations:
      "GET /things":
        title: Things           # menu label, tab, screen title
        group: inventory        # menu group
        hide: true              # off the menu / tabs; still generated if pushed
        skip: true              # not generated at all
        root: entries
        max_pages: 50
        columns: [name, status, size, owner_id]   # replaces the top-8 pick, in order
        window:                 # merged into the generated window
          search_param: name__icontains
          filter_ops: {contains: "${column.value}__icontains"}
        sort_refuse: [status]   # dropped from sort_fields, both directions
        anchored: {cursor: id}  # opts into an anchored window
        confirm: false          # on a write: drop the DELETE confirm, or add one
  ```

  Unknown keys, and operations not in the spec, are errors with a "did
  you mean". Claims and secrets' `from:` go in the config fragment.
- **Merging the fragment:** JSON merge patch (RFC 7386): maps merge
  recursively, scalars replace, `null` deletes, lists replace. Except
  lists with an identity, which merge by it: `app.env` by `name`,
  `app.prompts` by `key`, `columns` by `title`, action `inputs` by
  `name`, screen `actions` by `action`, menu `items` by `screen`, `tabs`
  by `label`. A matched item merges, a new one appends, `{…, $drop:
  true}` removes.
- **Precedence:** flags > `gen:` > generated defaults; the fragment is
  applied last, then `Load`. An error caused by an override names the
  overrides file and the key path. Errors: a `gen:` hint and the
  fragment setting the same field; `skip` with other hints; a `$drop`
  matching nothing. A fragment key naming something not generated is an
  addition, and fine.
- **Flags vs file:** flags only for global, per-run choices; anything
  per operation lives only in the file. No generator "styles"
  (`filter_style: django`): the `window:` hint says it in runtime
  terms. A regeneration is `tui-builder gen <spec> --overrides o.yaml -o
  app.yaml --force`.
- **Spec drift:** hints for an operation gone from the spec are errors
  (`--prune-overrides` makes them warnings); a column hint naming a
  removed property is an error; new operations get defaults and are
  listed as new in the report.
- **`--init-overrides`:** writes a starter file, every generated
  operation key commented out with its current title, group and
  columns. For the fixture, that's where the `__icontains` filters and
  `sort_refuse: [status]` get filled in.
- **From earlier sections, overrides can also:** set a real `from:` on a
  secret; supply `auth:` for a skipped scheme; set `root:` where several
  arrays tie; add a list for a skipped path parameter; add `filter_ops`
  (with `filter_except` derived); raise `max_pages`; add claims and
  confirms; set a list's columns.

## 10. Testing

From [Testing generated configs](https://github.com/jsdrews/tui-builder/issues/139).

- **Goldens:** `internal/gen/testdata/<case>/` holds `spec.yaml` (or a
  fixture path), an optional `overrides.yaml`, `args`, `want.yaml` and
  `want.report`. `go test ./internal/gen -update` rewrites them. For the
  fixture: defaults; `--read-only`; `--tag owners` (the orphan
  sub-list); `--layout menu`; with overrides (`__icontains` filters,
  `sort_refuse`, a `from:` secret, a `$drop`).
- **Every output loads:** each golden test also runs `config.Load`,
  with stand-ins for required env vars as `TestEveryExampleLoads` does.
- **End to end:** one test in `internal/screen` generates the things
  config with `--base-url` at `things.New()` as an `httptest` server and
  `THINGS_API_TOKEN=fixture-token`, and drives it through
  `windowHarness`: the list and its total, paging past 100 rows, a sort
  on size, the refused status sort (a 400 notice without overrides, not
  sortable with them), an `__icontains` filter via overrides, `enter`
  to the detail, switching tabs, create / edit / delete hitting the
  right routes with the confirm, and a 401 without the token. Each PR
  extends it.
- **Rule-level unit tests:** table tests on inline spec snippets for
  each rule: list detection, id pairing, paging-style detection, column
  ranking, action inputs, the security mapping, the menu/tabs threshold,
  keyed merges and `$drop`, each override error.
- **Public specs,** vendored (licence permitting) under
  `internal/gen/testdata/public/`: Petstore 3.0, GitHub's REST bundle,
  and a mid-size spec with OAuth2 and apiKey such as Stripe's. Checked
  by assertions, not goldens: generates, loads, stays within a time
  budget (GitHub < 5 s), report counts in expected ranges, `--tag`
  narrows. Skipped under `-short`. If GitHub's bundle is too heavy to
  vendor, a `go generate` script fetches it into a git-ignored
  directory and the test skips without it.
- **Determinism:** one test generates twice and compares bytes.

## What's already in place

- **The runtime:** everything the generator writes exists today:
  presets with `extends:` / `unset:`; `auth:` (bearer, basic, api_key in
  header / query / cookie, oauth2 client credentials); `retry:`;
  `app.env` with `required`, `default`, `description`, `from:`;
  `app.prompts`; windows with `page_param`, `page_start`, `total_path`
  (headers too), `sort_fields`, `sort_desc_prefix`, `filters`,
  `filter_ops` / `filter_except`, refused-sort recovery; `paginate:`;
  `auto` inspectors; `type: menu`; `tabs:`; action groups; theme tokens
  `success` / `warning` / `error`, hex palettes downsampled in colour
  rules. Built by
  [Map: tuilib table bugs and tui-builder gaps from generated configs](https://github.com/jsdrews/tui-builder/issues/105)
  and the scaffolding work.
- **No runtime changes are needed ahead of the generator.** Every
  runtime gap the map found is worked around in what's generated (see
  [Follow-ups](#follow-ups)).
- **The fixture** (`internal/fixture/things`), with its tests.
- **Not yet:** kin-openapi as a dependency; the Cobra root and
  subcommands (scaffolding PR 1); the shared column ranking (scaffolding
  PR 4).

## Implementation order

Each PR is usable on its own, leaves `go test ./...` green, and brings
its fixture goldens, unit tests and e2e steps with it. The generator
lives in `internal/gen`. **Depends on** the scaffolding plan's PR 1
(subcommands) before PR 1 here. The column ranking and style is shared
with the scaffolding plan's PR 4: whichever lands first puts it in a
package both use, and the other reuses it.

1. **Parser, skeleton and preset** (sections 1–3): `gen` on the Cobra
   root; kin-openapi loading from a file, URL or stdin with
   `--header` / `--timeout`; ordering by origin lines; the schema walker
   with a visited set; selection flags; the YAML emitter with comments,
   the header and the skip report; determinism; `-o` / `--force`; the
   `Load` check; exit codes; `docs/cli` and the docs-flags test. The
   document level: `app`, the preset, servers, every scheme, `app.env`,
   `retry`, `--name`, `--base-url`, `--prompt-server`. Singleton
   screens with `auto` inspectors, so the output has something to show
   (a spec with none exits 1 until PR 2). The golden harness with
   `-update`.
   *As built:* PR 1 also writes the basic layout (one screen; two to
   five as tabs; six or more as a menu grouped by tag, in the spec's
   `tags:` order), since several singletons need one; PR 3 adds
   `--layout`, the title rules, orphan sub-lists and the huge-spec hints.
   Until overrides land (PR 7) the header says to change the flags and
   regenerate. Only the final skip reasons are reported (non-JSON,
   header or cookie parameters, deprecated, webhooks); lists, details and
   writes are left silently for the PRs that map them. Webhooks aren't
   listed when `--tag` / `--operation` narrow. When a requirement names
   several schemes, every env var is named for its scheme
   (`KEYED_APP_ID_API_KEY`). `--quiet` keeps warnings. Flags that shape
   the output are registered by `internal/gen` itself, so the goldens
   parse the same flags as the CLI.
2. **Lists, details and sub-lists** (section 4): list detection and
   `root:`, detail pairing and the `enter` push, sub-lists pushed from
   the parent row, startup prompts for required query params, skip
   reasons, screen names. One-request sources and plain columns (every
   scalar property, schema order) until PRs 4 and 5. The e2e test
   starts here: list, detail.
   *As built:* a list of strings is one column with `value: "."` (the row
   itself, which `config.Load` accepts where `""` isn't; pinned by a
   datasource test and documented). A detail's parameter also matches the
   row's property named like the parameter less `<singular>_`
   (`report_slug` → `slug`), and a list of strings fills it with the row.
   A second detail of one list goes in the action menu only. Sub-list
   keys are action-menu shortcuts, which is how the runtime treats every
   key but `enter`; they skip the shell's, the menu's and a table's keys
   and `n` / `e` / `d`. An object under an item (`/reports/{slug}/raw`)
   is skipped as neither a list nor a detail. A detail or sub-list whose
   list isn't generated (absent, or excluded by `--tag`) is skipped until
   PR 3's orphan rule. Properties sort by where their key is written
   (a `$ref`'s own origin), ties by name, so output is the same every run.
3. **Navigation** (section 5): the 1 / tabs / menu layout and
   `--layout`; menu items and groups from tags; titles; orphan
   sub-lists; huge-spec hints; `action_groups`. The public-spec
   assertions (Petstore, GitHub, the mid-size one) land here, since this
   is where scale shows.
   *As built:* labels two top-level screens share (GitHub has three
   "Issues") become each screen's name in words, then its method and
   path, since tab labels must be unique. A spec with no tags gets an
   `--operation` hint instead of `--tag`. A list whose rows have no plain
   field is skipped with a reason rather than failing the run (Stripe has
   one). `app.action_groups` waits for PR 6: nothing writes yet, so no
   screen has both groups. The large public specs are pinned to a commit
   and fetched by `go generate ./internal/gen` into a git-ignored
   `testdata/public/.cache/`; Petstore is vendored. On this machine
   GitHub's (1,254 operations) generates in about 2.3 s and Stripe's in
   about 1.4 s.
4. **Windows** (section 6): window, walk or one request; paging, sort,
   search, filter and range recognition; `distinct`; `filterable`.
   E2e: paging, sort, the refused sort.
   *As built:* walks also cover a cursor sent back in `cursor`,
   `page_token`, `pageToken` or `after`, Stripe's `starting_after` +
   `has_more` (`cursor_path: last:id`), a `links.next` body field, and
   page or offset with no page-size parameter. `max_pages: 20` is written
   on every walk so it can be found. Notes many lists share collapse to
   one line ("20 lists (…): the response has no total …"). On a window,
   columns carry no `sortable:` at all (`sort_fields` decides) and enum
   columns get `distinct:`. The end-to-end test pages the fixture past
   100 rows (`G` asks for page 2), sorts by name on the server, sees the
   refused `status` sort revert with "can't sort by Status", sorts by
   size next, and filters `status:failed` as `?status=failed`. GitHub's
   spec gives 30 windows and 90 walks.
5. **Columns and style** (section 7): ranking with the long-text slot,
   the 8 cap and commented-out rest, the bound column, titles,
   sort/align, colour rules, widths, the detail inspector's rules,
   `mark_key`.
   *As built:* uses `internal/columns` (Pick, Styles, StatusRules)
   with schema evidence, as `add exec` uses it with a sample. The
   identity prefers a name-like field over the id a detail binds, which
   is what the fixture expectation implies (Name bold, Id among the
   numbers); with none, the bound id (`slug`) is it. Booleans rank as
   states. Nested fields are titled by their last segment unless two
   clash, so the fixture's labels are "Team" and "Env". `mark_key`
   comes with `markable: true`, which the runtime requires. The
   commented-out rest is written through the same placeholder lines as
   `app.env`'s `from:` examples. The fixture's things table is exactly
   the plan's list, checked by a test, and the end-to-end sort now steps
   name → status (refused) → created at → size.
6. **Actions** (section 8): collection and item actions, inputs and
   their workarounds, edits, confirm, names and keys, messages, `multi`,
   skips, `--read-only`. E2e: create / edit / delete.
   *As built:* integer fields are `type: int` inputs (the runtime has
   int; a JSON body token keeps the input's type, so `owner_id` goes out
   as `2`); only non-integer numbers are typed as text. A select can't
   show a placeholder, so an enum's or bool's spec default goes in its
   label ("Status (default: pending)"). Optional enums get a blank first
   option so nothing is sent unless picked. Inputs carry `order:` so the
   form follows the schema (forms otherwise sort alphabetically). Writes
   below an item (`POST /owners/{id}/things`) are item actions on the
   parent's row; collection writes on an orphan list read its startup
   parameter. A preset whose title-derived name equals a source's
   (`Jobs`, `/jobs`) gets an `_api` suffix; presets and sources share a
   namespace. The end-to-end test creates, edits (sending only
   `status`) and deletes (after the confirm) against the stub; the
   harness now skips cursor blinks, which had made each form cost 70 s.
7. **Overrides** (section 9): the file, `gen:` hints, the merge patch
   with keyed lists and `$drop`, precedence and conflict errors, drift
   and `--prune-overrides`, `--init-overrides`. `filter_except` derived
   when `filter_ops` is set. E2e: the `__icontains` filter, the
   refused sort made unsortable.

   *As built (PR 7):* the config fragment uses the config's own shape
   (`app`, `data`, `actions`, `tui`, with presets and sources under
   `data:`), not top-level `presets:` / `sources:`. Global `gen:` keys
   mirror the flags by name (`tag`, `exclude_tag`, `operation`,
   `exclude_operation`, `read_only`, `include_deprecated`,
   `prompt_server`, `timeout`); a flag on the command line wins.
   `--init-overrides FILE` writes only the starter (with `-o` it's a
   usage error). `anchored:` takes the window's anchored fields
   (`cursor`, `older`, `newer`, `anchor`), drops the paging, total and
   sort fields, and makes the table a span table keyed by the row's id.
   A `columns:` hint keeps the rest commented out, in rank order, and
   the bound id still goes in hidden. Hints that don't fit what an
   operation became are errors; hints for an operation nothing was
   generated from are warnings. The end-to-end test runs the fixture
   with the overrides golden's file: `name:thing-01` goes out as
   `?name__icontains=thing-01`, and the sort cycle skips `status`.
   New operations: a footer comment at the end of the generated file
   lists every operation in the spec (all of it, so a different `--tag`
   doesn't make operations look new), wrapped as comma-separated lines;
   at the end rather than in the header, so a big spec's list doesn't
   push the config down. When `-o`
   replaces a config, gen reads that list first and reports `new in the
   spec:` and `gone from the spec:` lines; a file with no list (not one
   gen wrote) reports nothing. GitHub's spec makes the list about 1,100
   lines.

## Follow-ups

Left for after v1. Each is a tui-builder runtime change; when one ships,
the generator drops its workaround.

- **Carrying a pushed screen's arguments into its bindings.** A pushed
  screen substitutes the carried selection into its title and sources
  only (`SubstituteScreen`, `internal/build/template.go`), so detail
  screens get no actions and can't push sub-lists.
- **Refresh after a write**, so messages can drop "(r to refresh)".
- **An edit form pre-filled from the row.**
- **A tri-state optional boolean input**, replacing `options: ["",
  "true", "false"]`.
- **A number input type**, replacing string inputs for numbers.
- **From the mapping research, still unexpressible:** opaque-cursor
  lists as windows, per-row transforms on windowed sources, a separate
  sort-direction param, seekable POST-body windows, array inputs,
  marking on seekable windows, menus carrying a selection, several auth
  schemes at once, mTLS, token refresh for `from:` values
  (`plans/research/openapi-mapping-v2.md` §3, R1–R15).
- **Not planned:** Swagger 2.0 (convert it first), a GraphQL generator,
  non-http sources from a spec, a `--strict` mode, live checks against
  the API at generation time.
