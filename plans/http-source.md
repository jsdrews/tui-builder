# HTTP source: feature gaps

A survey of how widely used HTTP APIs page, authenticate, rate-limit,
and shape their responses, checked against what `type: http` supports
today. The goal is a list of features that covers most real APIs
without hand-written `exec: curl | jq` workarounds.

## What ships today

`url`, `method`, `headers`, `body` (a string template), `format:
json|text`, `root`, `refresh`, `timeout`, `follow` (streams one event per
line), `paginate: {strategy: link, next_path}`, and `window:`
(offset/limit query params, `search_param`, `filters`, `sort_param`,
`sort_desc_prefix`, `total_path`).

## Survey

### Pagination

| API | Style | Request side | Response side (next / stop / total) |
|---|---|---|---|
| Django REST, AWX, Spotify | next URL in body | `?page=` or `?offset=&limit=` | `next` (absolute URL, `null` at end), `count` |
| Confluence, Salesforce | next URL in body, **relative** | — | `_links.next`, `nextRecordsUrl` (relative path) |
| Microsoft Graph, OData | next URL in body | `$top`, `$skip` | `@odata.nextLink`, `@odata.count` |
| GitHub, GitLab, Okta, Sentry, Shopify, Mastodon | **`Link` header** `rel="next"` | `per_page`, `page` / `page_info` | header only; GitLab also sends `X-Total` and `X-Next-Page` |
| Stripe | cursor = id of last item | `starting_after=<last.id>`, `limit` | `has_more: bool` |
| Slack | opaque cursor | `cursor=`, `limit` | `response_metadata.next_cursor` (`""` at end) |
| Kubernetes | opaque token | `continue=`, `limit` | `metadata.continue` |
| Google APIs | opaque token | `pageToken=`, `pageSize`/`maxResults` | `nextPageToken` |
| AWS (JSON APIs) | opaque token, **in POST body** | `{"NextToken": ...}` | `NextToken` |
| Notion | opaque cursor, **in POST body** | `{"start_cursor": ...}` | `next_cursor`, `has_more` |
| HubSpot | opaque cursor | `after=` | `paging.next.after` |
| Airtable | opaque token | `offset=` | `offset` (missing at end) |
| X/Twitter v2 | opaque token | `pagination_token=` | `meta.next_token` |
| Zendesk (cursor) | next URL + flag | `page[size]`, `page[after]` | `links.next`, `meta.has_more` |
| Jira | offset | `startAt`, `maxResults` | `total`, `isLast` |
| PagerDuty | offset | `offset`, `limit` | `more: bool`, `total` (opt-in) |
| Elasticsearch / OpenSearch | offset or `search_after` in body | `from`, `size` / `search_after: [sort values]` | `hits.total.value` |
| Algolia, GitHub search | page number | `page`, `hitsPerPage` / `per_page` | `nbPages`, `total_count` |
| json-server, many CRUD backends | offset or page | `_start`/`_limit`, `_page` | **`X-Total-Count` header** |
| Discord, Reddit | keyset | `before`/`after=<id>` | nothing; stop on a short or empty page |
| GraphQL (GitHub v4, Shopify, Linear) | Relay cursor **in variables** | `after: $cursor` in the JSON body | `pageInfo.endCursor`, `pageInfo.hasNextPage` |

The request side comes in three kinds: *follow a URL* (from the body
or from a header), *set a token/cursor* (in the query string or in the
body), and *count* (offset or page number). The response side needs
three signals: where the next value is, whether there is another page,
and the total. Each can come from the body or from a header.

### Beyond pagination

| Concern | Where it shows up |
|---|---|
| **`User-Agent` required** | GitHub rejects requests that don't send one; Reddit and Wikipedia throttle generic UAs |
| **Query params as a map** | Everywhere. Building the URL by hand breaks on spaces or `&` in `${selection.*}` values and can't omit empty params |
| **JSON body from YAML** | GraphQL, Elasticsearch, Notion, AWS. Templating a string body breaks when a value contains a quote |
| **Form-encoded body** | OAuth token endpoints, Twilio, older Stripe-style POSTs |
| **Auth shorthands** | Bearer (most), Basic (Jira Cloud, Jenkins, Artifactory), API key in query (`?api_key=`, OpenWeather, NASA), API key header (`X-API-Key`, `PRIVATE-TOKEN` on GitLab) |
| **OAuth2 client credentials** | Spotify, Salesforce, Azure, many internal APIs: POST for a token, cache it until `expires_in`, refresh on 401 |
| **429 + `Retry-After`** | GitHub (also `X-RateLimit-Remaining`/`-Reset`), Stripe, Slack, Discord, Shopify |
| **Retries with backoff** on 5xx and network errors | Every API has occasional failures; this matters most during a pagination walk |
| **Conditional GET** (`ETag` → `If-None-Match`, 304) | GitHub doesn't count 304s against the rate limit, which makes `refresh: 10s` affordable. Also k8s and CDNs |
| **Treat some statuses as data** | `404` means empty on lookups (`join` detail fetches); `204 No Content` needs to parse as empty rather than fail as JSON |
| **Error message extraction** | `message` (GitHub), `error.message` (Stripe, Google), `errors[0].message` (GraphQL), `detail` (DRF). Showing 200 bytes of raw body is noisy |
| **GraphQL errors on HTTP 200** | Every GraphQL API: a non-empty `errors` array alongside `data: null` should become an error |
| **Proper SSE parsing** | OpenAI-style APIs, GitHub Actions logs, Mastodon streaming, Wikimedia EventStreams: `data:` lines, multi-line events, `event:`/`id:`, `Last-Event-ID` on reconnect. `follow` currently passes the raw `data: {...}` lines through |
| **NDJSON parsing in `follow`** | k8s `?watch=true`, Docker events, Elasticsearch `_bulk`. Each line should become a parsed object, not a string |
| **Reconnect on stream drop** | k8s watches time out by design; SSE specifies reconnect with `retry:` |
| **TLS options** | k8s API servers and self-hosted services: custom CA, client cert/key, `insecure_skip_verify` for dev |
| **Unix socket transport** | Docker Engine API (`/var/run/docker.sock`), Podman, containerd |
| **Proxy** | Corporate networks. Go honors `HTTPS_PROXY` already, so this only needs documenting |
| **Other response formats** | CSV (data portals, Google Sheets export), XML/RSS/Atom (feeds, S3 listings, legacy SOAP) |
| **Object → list** | Firebase RTDB, some config APIs, and Prometheus label maps return `{id: {...}, id2: {...}}`; tables need rows |
| **Response headers as data** | Totals (`X-Total-Count`), rate-limit display, `Location` after a POST |
| **Shared client config** | Ten sources against one API repeat `base_url`, auth headers, UA, and TLS each time |
| **Response size cap** | `io.ReadAll` has no limit, so one bad endpoint can use unbounded memory |

## Proposed features

Ordered by value divided by cost. Items within a tier are independent of
each other.

### Tier 1: small, unblocks common APIs

1. **`query:` map.** `map[string]string`, templated, URL-encoded, and
   empty values dropped. `window:` and `paginate:` write into the same
   `url.Values`.
2. **Default `User-Agent`** (`wrangl/<version>`), overridable by
   `headers:`.
3. **`paginate.strategy: link_header`**: follow `Link: <…>;
   rel="next"`. Covers GitHub, GitLab, Okta, Shopify, and Sentry.
   (Sentry also sends `results="false"` on the last `next` link, which
   has to be checked.)
4. **Relative next URLs**: resolve `next_path` values against the
   request URL. This is a one-line fix and unblocks Salesforce and
   Confluence.
5. **`paginate.strategy: cursor`**: `cursor_path` (response) plus
   `cursor_param` (query). Optional `has_more_path`; otherwise an empty
   or missing cursor ends the walk. Covers Slack, k8s, Google, HubSpot,
   Airtable, and X.
6. **`paginate.strategy: offset` / `page`**: `offset_param` or
   `page_param`, `limit_param`, `page_size`. Stops on `total_path`, on
   a short page, or on an empty page. Covers Jira, PagerDuty,
   json-server, Algolia, and Discord (with `stop_on: short_page`).
7. **`accept_status: [404]`** (or `empty_on: [404]`) and treating
   `204`/an empty body as `null`.
8. **`error_path:`**: a dot-path to the error message in a non-2xx
   body, with fallback to the current snippet. Built-in guesses for
   `message`, `error.message`, `detail`, and `errors[0].message` make
   the default output better even when `error_path` isn't set.

### Tier 2: auth and resilience

9. **`auth:` block**: `{bearer: ${env.X}}`, `{basic: {user, pass}}`,
   `{api_key: {in: query|header, name, value}}`. This is mostly sugar
   over headers, but it keeps secrets out of `headers:` so we can
   redact them in `--describe` and in error messages.
10. **`retry:`**: `{max: 3, backoff: 500ms}` on 5xx, network errors, and
    429. Honors `Retry-After` (seconds or HTTP-date). Applies to each
    page in a walk, so a transient blip no longer kills page 14 of 20.
11. **Conditional GET for polled sources**: remember the `ETag` and
    `Last-Modified`, send `If-None-Match`, and on a 304 re-emit the
    last value. This needs no config.
12. **`oauth2:` client credentials**: `{token_url, client_id,
    client_secret, scopes}`. Cache the token until `expires_in`, and
    re-fetch and retry once on a 401.
13. **Response size cap**: a default (say 50 MB) with an override.

### Tier 3: body-carried pagination and GraphQL

14. **`json:` body**: a YAML value encoded as JSON, where templated
    strings are substituted *before* encoding so quotes are safe. Sets
    `Content-Type`. `form:` does the same for urlencoded bodies.
15. **Cursor in the body**: `paginate.cursor_in: body` with
    `cursor_field: start_cursor` (a dot-path into `json:`). Covers
    Notion, AWS, and Elasticsearch `search_after`.
16. **`graphql:` sugar**: `{query, variables}` builds the POST body;
    `errors` on a 200 response becomes an error; `root` defaults to
    under `data`. Relay pagination (`pageInfo.endCursor` into
    `variables.after`) then falls out of item 15.

### Tier 4: streaming

17. **`follow` framing**: `framing: lines|ndjson|sse`. `ndjson` parses
    each line; `sse` handles `data:`, multi-line events, `event:`, and
    `id:`, and drops comments and keepalives.
18. **Reconnect**: `reconnect: {backoff}` plus SSE `Last-Event-ID`.
    This also covers k8s watch timeouts.

### Tier 5: transport and formats

19. **`tls:`**: `{ca_file, cert_file, key_file, insecure_skip_verify}`.
20. **`unix_socket:`** transport for the Docker and Podman APIs.
21. **`format: csv | xml | ndjson`** for one-shot fetches.
22. **`headers_as:`** exposes response headers to `total_path`,
    `cursor_path`, and similar, e.g. `total_path: header:X-Total-Count`.
    This also serves item 6 for json-server and GitLab.
23. **Shared connection block**: a named `data.connections.github:
    {base_url, auth, headers, tls, retry}` that sources reference with
    `connection: github`. This is the largest ergonomic win for real
    configs, and it also gives auth and TLS one home.

### Window follow-ups

- **`page_param`** as an alternative to `offset_param`, computed as
  `page = offset / limit + 1`. GitHub, GitLab, and Algolia only accept
  page numbers.
- **Separate sort-direction param** (`order_param: order`, with
  `asc`/`desc` values) for GitHub, json-server, and many CRUD APIs
  that don't use a `-field` prefix.
- **Total from a header** (see item 22).
- Cursor APIs can't jump to a random offset, so windowing over them
  would need forward-only scrolling with a cursor cache. **Out of
  scope for now.** Use `paginate:` for those APIs.

## Deliberately out of scope

AWS SigV4 signing, digest auth, cookie or session login flows, SOAP,
HTTP/3 knobs, and response caching beyond `cache:`, which already
exists. Use an `exec` source for these, or add them once a real config
needs them.

## Suggested order

Items 1–8 in one PR. They are all local to `http.go` plus config
validation. Then add 22 and 23 before 9 and 12, so auth has a
connection block to live in rather than being duplicated on every
source. Streaming (17–18) and GraphQL (14–16) are independent tracks.
