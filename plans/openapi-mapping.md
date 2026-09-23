# OpenAPI 3 → tui-builder http source: mapping research

Research for the ticket *How OpenAPI 3 expresses servers, security,
parameters and pagination*, part of the map *HTTP source spec
(pagination, auth, bodies, presets)*.

The question: what an OpenAPI 3.0/3.1 (and now 3.2) document says that a
converter would have to carry into a tui-builder config. The plan is one
OpenAPI document → one **preset** (`data.presets.<api>`) plus one
**source** per operation (`extends: <api>`). The proposed schema this is
checked against is the feature list in `plans/http-source.md`: `query:`,
`auth:` (bearer / basic / api_key / oauth2 client credentials), `json:` /
`form:` bodies, and paginate strategies `link`, `link_header`, `cursor`,
`offset` / `page`.

Sources (all primary):

- [OAS 3.0.3] https://spec.openapis.org/oas/v3.0.3.html
- [OAS 3.1.0] https://spec.openapis.org/oas/v3.1.0.html
- [OAS 3.2.0] https://spec.openapis.org/oas/v3.2.0.html
- [Speakeasy] https://www.speakeasy.com/docs/customize/runtime/pagination
- [Fern] https://buildwithfern.com/learn/sdks/deep-dives/auto-pagination
- [Stainless] https://www.stainless.com/docs/sdks/configure/pagination/
- [AutoRest] https://github.com/Azure/autorest/blob/main/docs/extensions/readme.md (`x-ms-pageable`)

## 1. Servers

- A Server Object has `url` (required), `description`, `variables`. The
  url "supports Server Variables and MAY be relative, to indicate that
  the host location is relative to the location where the OpenAPI
  document is being served" [OAS 3.1.0 §4.8.5]. 3.2 adds an optional
  `name` [OAS 3.2.0 §4.5].
- A Server Variable has `default` (required; "SHALL be sent if an
  alternate value is not supplied"), optional `enum`, `description`
  [OAS 3.0.3 §4.7.6].
- No `servers` (or empty) means a single server with url `/` [OAS 3.0.3
  §4.7.1.1].
- `servers` can be overridden at Path Item and at Operation level; the
  deeper one wins [OAS 3.0.3 §4.7.9, §4.7.10].
- **The path is appended, not resolved.** "The path is appended (no
  relative URL resolution) to the expanded URL from the Server Object's
  url field" [OAS 3.0.3 §4.7.8.1]. So `https://api.x.com/v1` + `/pets` is
  `https://api.x.com/v1/pets`. RFC 3986 resolution would give
  `https://api.x.com/pets` and drop `/v1`.
- Other URL-valued fields (e.g. OAuth `tokenUrl`) MAY be relative and
  resolve against the Server Object url as base [OAS 3.1.0 §4.7
  "Relative References in URLs"; OAS 3.0.3 §4.6].

**Consequence for presets.** "Relative url resolves against the preset's
url" must mean *append with slash normalisation* for the preset/source
join, or every versioned base path (`/v1`, `/api/v3`) breaks. Relative
**next links** in response bodies (Salesforce `nextRecordsUrl`, Fern
`next_path`) are a different case: they should be RFC 3986-resolved
against the request URL. Two rules, and both need names in the docs.

## 2. Security schemes

`components.securitySchemes.<name>.type` is one of `apiKey`, `http`,
`oauth2`, `openIdConnect` (3.0), plus `mutualTLS` (3.1+) [OAS 3.1.0
§4.8.27]:

| type | fields |
|---|---|
| `apiKey` | `name`, `in`: `query` \| `header` \| `cookie` |
| `http` | `scheme` (IANA HTTP auth scheme registry: `basic`, `bearer`, `digest`, …), `bearerFormat` (a hint) |
| `oauth2` | `flows`: `implicit`, `password`, `clientCredentials`, `authorizationCode` (+ `deviceAuthorization` in 3.2). `clientCredentials` requires `tokenUrl` and `scopes`; `refreshUrl` optional. 3.2 adds `oauth2MetadataUrl` (RFC 8414) |
| `openIdConnect` | `openIdConnectUrl`: the discovery document |
| `mutualTLS` | no fields; the client cert is out-of-band |

3.2 also adds `deprecated` on a scheme [OAS 3.2.0 §4.27].

Security Requirements (`security:`) is an array of objects: **OR across
the array, AND within one object**. `{}` in the array makes auth
optional. Operation-level `security` replaces the top-level one, and
`security: []` removes it [OAS 3.0.3 §4.7.30, §4.7.10].

The spec never holds secret values, so a converter always writes
`${env.<API>_<SCHEME>}` placeholders.

## 3. Parameters

`in`: `path` (must be `required: true`), `query`, `header`, `cookie`
[OAS 3.0.3 §4.7.12]. 3.2 adds `querystring`: the whole query string is
one value, described with `content` (usually form-urlencoded or JSON). It
can't be combined with `query` params [OAS 3.2.0 §4.12.1]. Parameters are
unique by `(name, in)`, and operation-level ones override path-level ones.
A header param named `Accept`, `Content-Type` or `Authorization` is
ignored (those come from `content`/`security`).

Serialization is set by `style` and `explode` [OAS 3.0.3 §4.7.12.3–4]:

| style | in | default explode | `color=["blue","black"]` | `{R:100,G:200}` |
|---|---|---|---|---|
| `form` (query/cookie default) | query, cookie | **true** | `color=blue&color=black` (explode=false: `color=blue,black`) | `R=100&G=200` (explode=false: `color=R,100,G,200`) |
| `simple` (path/header default) | path, header | false | `blue,black` | `R,100,G,200` (explode: `R=100,G=200`) |
| `spaceDelimited` | query | false | `color=blue%20black` | — |
| `pipeDelimited` | query | false | `color=blue\|black` | — |
| `deepObject` | query | true | — | `color[R]=100&color[G]=200` |
| `label` | path | false | `.blue.black` | — |
| `matrix` | path | false | `;color=blue,black` | — |

`allowReserved` (query only) skips percent-encoding of reserved
characters. A parameter uses either `schema` or `content`, never both.

**Consequence.** The *default* for an array query param is repeated
keys (`?id=1&id=2`). A `query: map[string]string` can't express that.

## 4. Request bodies

`requestBody.content` is a map of media type → Media Type Object
(`schema`, `example(s)`, `encoding`). `required` defaults to false
[OAS 3.0.3 §4.7.13–14]. The Encoding Object applies to
`application/x-www-form-urlencoded` (`style`/`explode`/`allowReserved`,
same rules as query params) and to `multipart/*` (`contentType`,
per-part `headers`). Multipart part defaults: primitive → `text/plain`,
object → `application/json`, binary → `application/octet-stream`
[OAS 3.0.3 §4.7.14.5, §4.7.15]. 3.1 allows a body on GET/HEAD/DELETE but
says its semantics are undefined [OAS 3.1.0 §4.8.10]. 3.2 adds
*sequential* media types (`application/jsonl`, `application/x-ndjson`,
`text/event-stream`) with `itemSchema` applied per streamed item
[OAS 3.2.0 §4.14.3, §4.14.4].

## 5. Finding the list in a response

There is no "items" marker in core OpenAPI. The converter has to infer
from `responses["200" | "2XX" | "default"].content["application/json"].schema`:

1. Top-level `type: array` → no `root:`.
2. Object with one array property → `root: <that property>`. Common
   names are `data`, `items`, `results`, `value` (OData/Azure),
   `records`, `hits.hits`.
3. `allOf` envelopes (`allOf: [Envelope, {properties: {data: {type:
   array}}}]`) need `$ref` resolution and an allOf merge before step 2.
4. More than one array, or `oneOf`/`anyOf` at the top: ambiguous. Pick
   one and flag it, or ask.

A pagination extension, when present, names the list outright and wins
over the heuristic: Speakeasy `outputs.results: $.data`, Fern `results:
$response.data`, Stainless `x-stainless-pagination-property: {purpose:
items}`, AutoRest `itemName` (default `value`).

Declared response **headers** are also useful: `Link` points to
`link_header`, and `X-Total-Count`/`X-Total` point to a header-sourced
total (plan item 22).

## 6. Pagination

Core OpenAPI has **no pagination construct**. The nearest are:

- Link Objects: a response can name another operation and fill its
  parameters with runtime expressions such as `$response.body#/next_cursor`
  [OAS 3.0.3 §4.7.20]. That could describe "next page" but is rarely
  used for it.
- 3.2 "Modeling Link Headers": describe `Link` via `application/linkset`
  on a Header Object, with a collection-pagination example
  (`first`/`prev`/`next`/`last`) [OAS 3.2.0 §4.21.2]. This is new and
  not yet common.

In practice pagination comes from vendor extensions. Where there's none,
it comes from parameter and property names.

| Extension | Kinds | Request side | Response side |
|---|---|---|---|
| `x-speakeasy-pagination` (operation) | `offsetLimit`, `cursor`, `url` | `inputs[]: {name, in: parameters\|requestBody, type: page\|offset\|limit\|cursor}` | `outputs: {results, numPages, nextCursor, nextUrl}` as JSONPath (e.g. `$.data[-1].created_at`). No header support |
| `x-fern-pagination` (operation) | cursor, offset, `next_uri`, `next_path` | `cursor: $request.cursor`, `offset: $request.page` | `next_cursor: $response.next`, `results: $response.data`, `next_path` (relative) |
| Stainless (`stainless.yml` + `x-stainless-pagination-property`) | `cursor`, `cursor_id`, `cursor_url`, `offset`, `page_number` | `param_location: query\|body` | `items`, `next_cursor_field`, `cursor_url_field`, `cursor_item_id`, `offset_total_count_field`, `total_page_count_field`; `from_header:` (incl. `Link`) |
| `x-ms-pageable` (AutoRest) | next link | GET to the link, or `operationName` for a different op | `nextLinkName` (null = not paged, just unwrap), `itemName` (default `value`) |

Without an extension, look for parameter names (`cursor`, `page_token`,
`pageToken`, `starting_after`, `after`, `continue`, `offset`/`limit`,
`page`/`per_page`) together with response properties (`next`,
`next_cursor`, `nextPageToken`, `has_more`, `@odata.nextLink`) and a
declared `Link` header.

## 7. Mapping table

| OpenAPI construct | Proposed tui-builder field | Notes |
|---|---|---|
| `servers[0].url` | preset `url:` | Several servers: pick one and add the rest as a comment, or expose them as an `enum` param |
| relative `servers[].url` | preset `url:` (absolute) | Resolve against the spec's retrieval URL at conversion time |
| server `variables` | preset `parameters:` with `default`/`enum`; `${params.x}` in url | Presets must be able to carry `parameters:` |
| path/operation `servers` override | source absolute `url:` (overrides preset) | Or a second preset |
| `paths` key `/pets/{id}` | source `url: pets/${params.id}` (appended to the preset url) | Append, not RFC 3986; see §1 |
| `in: path` | `${params.x}` in url | Value must be path-escaped. `label`/`matrix` are rare, so use a literal template |
| `in: query`, scalar | `query: {name: ${params.x}}` | Optional + empty → dropped (matches plan item 1) |
| `in: query`, array, `form` explode=true (default) | **gap**: repeated keys | See G1 |
| `in: query`, array, explode=false / space / pipe | `query:` value joined `,` / ` ` / `\|` | Needs a join in templates, or list values with a `style:` |
| `in: query`, object, `deepObject` | literal keys `query: {"filter[status]": …}` | Converter flattens known properties |
| `in: query`, object, `form` explode | one `query:` key per property | Converter flattens |
| `in: header` | `headers:` | |
| `in: cookie` | `headers: {Cookie: "a=${params.a}"}` | Works, but clumsy |
| `in: querystring` (3.2) | **gap**, rare | |
| `required` / `schema.default` / `enum` / `description` | `parameters:` entry | Required with no default → "needs params" binding |
| `apiKey` in header | `auth: {api_key: {in: header, name, value}}` | |
| `apiKey` in query | `auth: {api_key: {in: query, name, value}}` | |
| `apiKey` in cookie | **gap**: `api_key.in: cookie` | G3 |
| `http` `bearer` | `auth: {bearer: ${env.X}}` | `bearerFormat` is only a hint |
| `http` `basic` | `auth: {basic: {user, pass}}` | |
| `http` other schemes (digest, …) | out of scope | Matches plan |
| `oauth2.clientCredentials` | `auth: {oauth2: {token_url, client_id, client_secret, scopes}}` | `tokenUrl` may be relative: resolve against the server url |
| `oauth2` authorizationCode / implicit / password / device | `bearer: ${env.X}` fallback | Interactive flows are out of scope |
| `openIdConnect` / `oauth2MetadataUrl` (3.2) | **gap**: discovery | G5 |
| `mutualTLS` (3.1+) | `tls: {cert_file, key_file}` (plan item 19) | Must be preset-able |
| top-level `security` | preset `auth:` | |
| operation `security` override | source `auth:` | |
| `security: []` / `{}` (public op) | **gap**: clear inherited auth | G4 |
| AND of schemes (`{appId: [], appKey: []}`) | **gap**: single `auth:` | G2; falls back to `headers:` |
| `requestBody` `application/json` (+ `*+json`) | `json:` | |
| `requestBody` `application/x-www-form-urlencoded` | `form:` | Nested encodings are rare |
| `requestBody` `multipart/form-data` | **gap** | G6 |
| other body types (`text/plain`, octet-stream) | `body:` string + `Content-Type` header | |
| operation `method` (+ 3.2 `additionalOperations`, `QUERY`) | `method:` | Allow any token, not only known verbs |
| 2xx response schema array / envelope | `root:` | §5 heuristic, or the extension's results path |
| response `content` `application/json` | `format: json` | |
| response `text/event-stream`, `application/jsonl`/`x-ndjson` | `follow: true` + `framing: sse\|ndjson` (plan item 17) | |
| response `text/csv`, XML | `format: csv\|xml` (plan item 21) | |
| response header `X-Total-Count` | `total_path: header:X-Total-Count` (plan item 22) | |
| response header `Link` / 3.2 linkset | `paginate: {strategy: link_header}` | |
| Speakeasy `url` / Fern `next_uri` / Stainless `cursor_url` / `x-ms-pageable.nextLinkName` | `paginate: {strategy: link, next_path}` | JSONPath/`$response.` → dot-path |
| Fern `next_path` | `strategy: link` with relative resolution | RFC 3986 against the request URL (plan item 4) |
| Stainless `cursor_url` `from_header: Link` | `strategy: link_header` | |
| cursor, input `in: parameters` | `strategy: cursor`, `cursor_param`, `cursor_path` | |
| cursor, input `in: requestBody` / Stainless `param_location: body` | `cursor_in: body`, `cursor_field` (plan item 15) | |
| Speakeasy `offsetLimit` offset / Stainless `offset` | `strategy: offset`, `offset_param`, `limit_param`, `total_path` | |
| Speakeasy `offsetLimit` page / Stainless `page_number` | `strategy: page`, `page_param` | |
| Speakeasy `numPages` / Stainless `total_page_count_field` | **gap**: stop on page count | G7 |
| Stainless `cursor_id` / Speakeasy `nextCursor: $.data[-1].id` | **gap**: cursor from last item | G7 (Stripe) |
| `has_more` flags | `has_more_path` (plan item 5) | Also needed for offset/page |
| `x-ms-pageable.operationName` | **gap**: next page via a different operation | G7, rare |
| `x-ms-pageable` `nextLinkName: null` | `root:` only, no paginate | |

## 8. Gaps: what the proposed schema can't express yet

- **G0. Preset URL join rule.** OpenAPI appends the path to the server
  url. The preset/source join has to append too (normalising `/`), and
  must not use RFC 3986 resolution, or versioned base paths break.
  Relative next links do use RFC 3986, against the request URL. This is
  a decision to write down, not a new field.
- **G1. Multi-valued query params.** `form`+explode (the OpenAPI
  default for arrays) needs repeated keys. Let `query:` values be a
  list (`ids: [a, b]` → `ids=a&ids=b`), with an optional join style
  (`comma` | `space` | `pipe`) for explode=false. Also check that a
  templated list param expands to a list.
- **G2. Several auth schemes at once** (Security Requirement AND, e.g.
  Datadog `DD-API-KEY` + `DD-APPLICATION-KEY`). Either `auth:` accepts a
  list, or the converter puts the second key in `headers:`. The second
  option loses redaction.
- **G3. `api_key.in: cookie`.** Cheap to add. Without it, cookie params
  and keys go through a hand-built `Cookie` header.
- **G4. Unsetting inherited fields.** A public operation (`security:
  []`) under an authed preset needs `auth: none` (or `auth: null` meaning
  "clear"). The `extends` merge rules need an explicit way to unset.
  The same applies to `paginate:` and `headers:` keys.
- **G5. OIDC / RFC 8414 discovery.** `openIdConnect` gives only a
  discovery URL. Either `oauth2.discovery_url` (fetch `token_endpoint`
  at runtime), or the converter fetches it once and writes `token_url`.
  The second fits "converter does the work" better.
- **G6. `multipart/form-data` bodies.** Rare for a read-mostly TUI
  (uploads). Leave it out of scope and have the converter skip or flag
  such operations.
- **G7. Pagination variants the extensions cover and the plan doesn't:**
  - cursor taken from the **last item** (`starting_after=<last.id>`,
    Stripe), e.g. `cursor_path: "[-1].id"` relative to the list, or a
    `cursor_from: last_item` + `cursor_item_field: id`;
  - stop on **total page count** (`total_pages_path`);
  - `has_more_path` for offset/page, not only cursor;
  - cursor read from a **response header** (Stainless `from_header`),
    which falls out of plan item 22 if `cursor_path` accepts
    `header:<Name>`;
  - next page via a **different operation/method** (`x-ms-pageable.operationName`),
    rare enough to leave out.
- **G8. Path-parameter escaping.** `${params.id}` in a url path must be
  path-escaped, or an id with `/` or space breaks the request. Needs a
  rule in the template engine: escape by URL position, or a filter.
- **G9. Path syntax translation.** The extensions use JSONPath
  (`$.data`, `$.data[-1].id`) and runtime expressions
  (`$response.body#/next`, `$request.cursor`). The converter must
  lower them to our dot-paths. Negative indices and JSON Pointer need a
  defined equivalent, or conversion fails loudly.
- **G10. `querystring` params (3.2).** Too rare to design for now.

Nothing found conflicts with type-agnostic presets. Every
document-level thing (server, server variables, security, mTLS) lands on
the preset, and every operation-level thing (path, params, body, root,
paginate, auth override) lands on the source. That split is what the
`extends` design assumes.
