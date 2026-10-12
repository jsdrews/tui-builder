# HTTP API survey

A survey of how widely used HTTP APIs page, authenticate, rate-limit,
and shape their responses, checked against what `type: http` supported
when it was written. It's the research behind the locked spec in
[`../http-source.md`](../http-source.md). The spec is what was decided;
this file is what the decisions were made from.

Two research tickets refined it:

- [Verify the API survey against current docs](https://github.com/jsdrews/tui-builder/issues/18)
  corrected several rows (Sentry's `results="false"`, json-server v1,
  Jira's `nextPageToken`, AWS, and `Retry-After` coverage). Its full
  corrections, with citations, are in
  [`http-survey-check.md`](http-survey-check.md).
- [How OpenAPI 3 expresses servers, security, parameters and pagination](https://github.com/jsdrews/tui-builder/issues/17)
  mapped OpenAPI onto the schema, in
  [`openapi-mapping.md`](openapi-mapping.md).

The tables below are as originally surveyed; where the verification
ticket disagrees, it wins.

## What shipped when surveyed

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

