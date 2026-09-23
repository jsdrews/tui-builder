# HTTP source survey: checked against vendor docs

Research note for *Verify the API survey against current docs*, part of
*Map: HTTP source spec (pagination, auth, bodies, presets)*. The survey
tables in `plans/http-source.md` were written from memory. This file
re-checks each row against the vendor's own docs (fetched 2026-09-22)
and gives corrected tables with a citation per row. `plans/http-source.md`
itself is left alone.

Legend: **Unchecked** means the claim is plausible but no first-party
source was read for it this pass. Salesforce's docs return 403 to
automated fetches, so that row rests on Salesforce's own search-result
snippets, and the relative-path shape of `nextRecordsUrl` was not seen
directly.

## Corrections

What the original tables got wrong or left out, in rough order of how
much each one affects the schema.

1. **Sentry always sends a `next` link.** The `Link` header carries
   `rel="previous"` (not `prev`) and `rel="next"`, each with
   `results="true|false"`. The next link is present even on the last
   page, so a plain "follow rel=next until it's gone" loop never ends.
   `link_header` needs a per-link attribute check such as `results`.
2. **A short or empty page does not mean the end.** Microsoft Graph
   ("a page of results might contain zero or more results"), Google
   Drive (pages "may be partial or empty"), DynamoDB (`Limit` counts
   items evaluated, not items returned) and Slack ("don't rely on
   result count") all stop only when the token or link is gone. Stop
   on a short page only when the user asks for it, and never by default
   for the cursor or link strategies.
3. **json-server v1 changed shape.** It now takes `_page` and `_per_page`
   (`_limit` is deprecated) and returns a body envelope
   `{first, prev, next, last, pages, items, data}` with **no
   `X-Total-Count` header**. It also clamps an out-of-range `_page` to the
   last page, so an "empty page = stop" rule loops forever. `X-Total-Count`
   and a `Link` header were v0.17 behaviour. The walker needs a loop
   guard (stop on a repeated cursor or URL, plus `max_pages`).
4. **Reddit is cursor-in-body, not bare keyset.** Listings return
   `data.after` and `data.before` fullnames (`null` at the end), and the
   next page is requested with `after=<fullname>`. Only Discord belongs
   in the "nothing in the response, stop on a short page" row.
5. **Jira issue search moved to tokens.** `GET /rest/api/3/search` is
   deprecated. `/rest/api/3/search/jql` takes `nextPageToken` and
   `maxResults` and returns `nextPageToken` and `isLast`, with **no
   `total`**. `startAt`/`maxResults`/`total`/`isLast` (plus a `nextPage`
   URL) still apply to the other paged ("PageBean") endpoints.
6. **AWS has no single pagination shape.** CloudWatch Logs uses
   `nextToken` (camelCase) in a JSON POST body. DynamoDB uses an
   *object* cursor: `ExclusiveStartKey` goes in, `LastEvaluatedKey`
   comes out. Lambda uses `Marker` in the query string and `NextMarker`
   in the response. S3 returns XML with `continuation-token`,
   `NextContinuationToken` and `IsTruncated`. Every one of them needs
   SigV4, which the plan already puts out of scope. Don't count AWS as
   covered by the cursor-in-body strategy.
7. **The rate-limit row was wrong about several vendors.**
   - **GitHub:** hitting the primary limit gives `403` or `429` with
     `x-ratelimit-remaining: 0`, and the client should wait until
     `x-ratelimit-reset` (epoch seconds). `retry-after` is sent only
     for secondary limits.
   - **Stripe:** documents **no `Retry-After`**. A `429` carries
     `Stripe-Rate-Limited-Reason`, and a `429` without that header is a
     `lock_timeout`, not a rate limit.
   - **Airtable** (fixed 30 s wait), **Okta** and **X**
     (`x-rate-limit-reset`) and **HubSpot** send no `Retry-After` either.

   The retry design needs a "reset header" fallback on top of
   `Retry-After`.
8. **Reset values come in several formats.** Epoch seconds: GitHub,
   Okta, X, Sentry, GitLab `RateLimit-Reset`, Discord
   `X-RateLimit-Reset`. Seconds from now: Reddit `X-Ratelimit-Reset`,
   Discord `X-RateLimit-Reset-After` (a float). An HTTP date: GitLab
   `RateLimit-ResetTime`. Mastodon doesn't say. `Retry-After` itself
   can be fractional (Shopify sends `Retry-After: 2.0`), Discord also
   puts a float `retry_after` in the body, and Notion asks clients to
   retry `529` as well as `429`.
9. **Header names aren't uniform.** Okta and X use `X-Rate-Limit-*`
   (with a hyphen in "Rate-Limit"). GitHub uses lowercase
   `x-ratelimit-*`. GitLab uses unprefixed `RateLimit-*`. Sentry uses
   `X-Sentry-Rate-Limit-*` and HubSpot uses `X-HubSpot-RateLimit-*`,
   and HubSpot sends none of them on search endpoints.
10. **Relative next URLs aren't limited to Salesforce and Confluence.**
    OData 4.01 allows a relative `nextLink`, resolved against the
    context URL, and drops the prefix (`@nextLink`, `@count`) unless the
    response is OData 4.0. Confluence v2 returns a relative
    `_links.next` **and** an absolute `Link` header.
11. **Totals are often missing, capped or opt-in.**
    - **GitLab** omits `X-Total`, `X-Total-Pages` and `rel="last"` above
      10,000 rows.
    - **Elasticsearch** `hits.total` has `relation: "gte"` beyond the
      tracking cap (10,000 by default).
    - **Opt-in totals:** PagerDuty `total` is `null` unless
      `total=true`. Stripe search `total_count` appears only with
      `expand[]=total_count`, and is accurate only to 10,000. Graph
      `@odata.count` needs `$count=true`, and directory resources send
      it on the first page only.
    - **Hard result caps:** GitHub search stops at 1,000 results,
      HubSpot search at 10,000 (then a `400`), Zendesk offset paging at
      100 pages or 10k rows, and Algolia's `paginationLimitedTo`
      defaults to 1,000.

    Treat a total as optional, and display it as a lower bound when it's
    marked approximate.
12. **Page numbers don't always start at 1.** Algolia's `page` is
    zero-based, and GitHub, GitLab, DRF and json-server start at 1. The
    `page` strategy needs a `page_start`.
13. **Stripe has three list shapes.** v1 lists use `starting_after`,
    `has_more` and no total. v1 search takes `page=<next_page>` (an
    opaque cursor, not a number). The v2 namespace uses `next_page_url`
    (`null` at the end).
14. **GraphQL can signal errors on a 200.** GitHub GraphQL returns HTTP
    `200` with an error when you exceed the primary rate limit (secondary
    limits can come back as `200` or `403`). That supports the "GraphQL
    errors on HTTP 200" row, and it means a rate-limit retry has to look
    at the body too.
15. **Kubernetes `continue` tokens expire** after about 5 minutes and
    return `410 Gone`. A slow walk has to restart the list rather than
    retry that page.
16. **Smaller fixes:**
    - GitHub also pages with `before`/`after` and `since`.
    - GitLab has keyset paging (`pagination=keyset&order_by&sort`).
    - Shopify REST is legacy as of 2024-10-01. Its `page_info` can't be
      combined with other params except `limit` and `fields`, and its
      `Link` uses `rel="previous"`.
    - HubSpot list responses also carry an absolute `paging.next.link`.
      HubSpot search is a POST with `after` in the body.
    - Okta's System Log polling always returns a `next` link.
    - Spotify's playlist-tracks endpoint is deprecated in favour of
      playlist *items*.
    - Google's `maxResults` survives only on older Discovery-based APIs.
      AIP-158 standardises `pageSize`/`pageToken`/`nextPageToken`.
    - GitHub's 304 exemption applies only when the request carries an
      `Authorization` header.

## Pagination (corrected)

| API | Style | Request side | Response side (next / stop / total) | Source |
|---|---|---|---|---|
| Django REST | next URL in body | `PageNumberPagination`: `?page=` (`page_size` only if the view enables it); `LimitOffsetPagination`: `?limit=&offset=`; `CursorPagination`: `?cursor=` | `next` / `previous` absolute URLs, `null` at end; `count` (not on cursor pagination) | [DRF pagination](https://www.django-rest-framework.org/api-guide/pagination/) |
| AWX | next URL in body | `?page=`, `page_size` | **Unchecked** (DRF-based; may return a relative `next`) | none |
| Spotify | next URL in body | `limit` (1–50), `offset` | `next` absolute URL or `null`, `total`, `items` | [Get Playlist Items](https://developer.spotify.com/documentation/web-api/reference/get-playlists-tracks) |
| Confluence Cloud v2 | next URL, **relative in body** + `Link` header | `cursor`, `limit` | `_links.next` relative (`/wiki/api/v2/…?cursor=`), `_links.base`; also `Link: <…>; rel="next"`. Stop when both are absent | [Confluence v2 intro](https://developer.atlassian.com/cloud/confluence/rest/v2/intro/#using) |
| Salesforce | next URL, **relative in body** | none on follow-ups; page size via `Sforce-Query-Options: batchSize=` header (default 2000) | `nextRecordsUrl` (relative query-locator path), `done: bool`, `totalSize` | [Query resource](https://developer.salesforce.com/docs/platform/api-rest/guide/resources-query.html) (snippets only, 403) |
| Microsoft Graph | next URL in body | `$top`; `$skip` or `$skipToken` on some APIs; `$count=true` (+ `ConsistencyLevel: eventual` on directory objects) | `@odata.nextLink` absolute; don't parse it; **pages may be empty**; `@odata.count` (first page only for directory objects) | [Graph paging](https://learn.microsoft.com/en-us/graph/paging) |
| OData 4.01 (generic) | next URL in body | `$top`, `$skip`, `$count=true`, `Prefer: maxpagesize=` | `@nextLink` / `@count` (with `odata.` prefix only in 4.0); nextLink **may be relative** to the context URL | [OData JSON 4.01](https://docs.oasis-open.org/odata/odata-json-format/v4.01/odata-json-format-v4.01.html) |
| GitHub REST | **`Link` header** | `per_page` (≤100), `page`; some endpoints use `before`/`after` or `since` | `rel` next/prev/first/last, absolute URLs; no total (search: `total_count` in body) | [GitHub pagination](https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api) |
| GitLab | **`Link` header** + `X-*` headers | offset: `page`, `per_page` (≤100); keyset: `pagination=keyset`, `order_by`, `sort` | `Link`; `X-Next-Page`, `X-Page`, `X-Per-Page`, `X-Prev-Page`, `X-Total`, `X-Total-Pages`; **total headers and `rel="last"` omitted above 10,000 rows** | [GitLab REST pagination](https://docs.gitlab.com/api/rest/#pagination) |
| Okta | **`Link` header** | `limit`, `after` (opaque; don't build it by hand) | `rel="self"`, `rel="next"` absolute; stop when there is no `next` (System Log polling always sends `next`) | [Okta pagination](https://developer.okta.com/docs/api/#pagination) |
| Sentry | **`Link` header** + `results` attribute | `cursor=0:100:0` (from the link) | `rel="previous"` and `rel="next"` **always present**; stop when next has `results="false"` | [Sentry pagination](https://docs.sentry.io/api/pagination/) |
| Shopify REST (legacy) | **`Link` header** | `limit` (≤250), `page_info` (no other params except `limit`, `fields`) | `rel="next"`, `rel="previous"`; links are short-lived | [Shopify REST pagination](https://shopify.dev/docs/api/admin-rest/usage/pagination) |
| Mastodon | **`Link` header** | `limit`, `max_id`, `since_id`, `min_id` (treat IDs as opaque strings) | `rel="next"`, `rel="prev"`, absolute URLs | [Mastodon guidelines](https://docs.joinmastodon.org/api/guidelines/#pagination) |
| Stripe v1 list | cursor = id of last item | `limit` (1–100), `starting_after=<last.id>` or `ending_before` | `has_more: bool`, `data`; no total | [Stripe pagination](https://docs.stripe.com/api/pagination) |
| Stripe v1 search | opaque cursor | `page=<next_page>`, `limit` | `next_page`, `has_more`; `total_count` only with `expand` (accurate to 10k) | [Stripe search pagination](https://docs.stripe.com/api/pagination/search) |
| Stripe v2 | next URL in body | `page` token | `next_page_url` (`null` at end), `previous_page_url` | [Stripe API v2 overview](https://docs.stripe.com/api-v2-overview) |
| Slack | opaque cursor | `cursor`, `limit` (≤1000, 100–200 recommended) | `response_metadata.next_cursor`; empty, null or missing = end; don't use result count | [Slack pagination](https://docs.slack.dev/apis/web-api/pagination) |
| Kubernetes | opaque token | `continue`, `limit` | `metadata.continue` (`""` at end), optional `metadata.remainingItemCount`; token expires (~5 min) → `410 Gone` | [k8s API concepts](https://kubernetes.io/docs/reference/using-api/api-concepts/#retrieving-large-results-sets-in-chunks) |
| Google APIs | opaque token | `pageToken`, `pageSize` (`maxResults` on older APIs) | `nextPageToken` absent or empty at end (the *only* end signal); optional `totalSize`; pages may be short or empty | [AIP-158](https://google.aip.dev/158), [Drive files.list](https://developers.google.com/drive/api/reference/rest/v3/files/list) |
| AWS | varies by service; SigV4 required | Logs: `nextToken` in JSON body. DynamoDB: `ExclusiveStartKey` (object). Lambda: `Marker` query param. S3: `continuation-token` | Logs: `nextToken`. DynamoDB: `LastEvaluatedKey` (absent = end). Lambda: `NextMarker`. S3 (XML): `NextContinuationToken`, `IsTruncated` | [DescribeLogGroups](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogGroups.html), [Scan](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html), [ListFunctions](https://docs.aws.amazon.com/lambda/latest/api/API_ListFunctions.html), [ListObjectsV2](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html) |
| Notion | opaque cursor; **query for GET, body for POST** | `start_cursor`, `page_size` (≤100) | `next_cursor`, `has_more`, `results` | [Notion pagination](https://developers.notion.com/reference/intro#pagination) |
| HubSpot | opaque cursor | list: `after`, `limit` (≤100) in query; search: `after`, `limit` (≤200) in the **POST body** | `paging.next.after`, `paging.next.link`; search `total`, 10k cap (`400` beyond) | [Contacts list](https://developers.hubspot.com/docs/api-reference/crm-contacts-v3/basic/get-crm-v3-objects-contacts), [CRM search](https://developers.hubspot.com/docs/api-reference/search/guide) |
| Airtable | opaque token | `offset`, `pageSize` (≤100); POST `/listRecords` puts them in the body | `offset` (missing at end) | [List records](https://airtable.com/developers/web/api/list-records) |
| X/Twitter v2 | opaque token | `pagination_token`, `max_results` | `meta.next_token` (omitted at end), `meta.previous_token`, `meta.result_count` | [X pagination](https://docs.x.com/x-api/fundamentals/pagination) |
| Zendesk (cursor) | next URL + flag | `page[size]` (≤100), `page[after]` | `links.next` (absolute URL), `meta.after_cursor`, `meta.has_more` | [Zendesk pagination](https://developer.zendesk.com/api-reference/introduction/pagination/) |
| Zendesk (offset) | next URL in body | `page`, `per_page` | `next_page` URL, `count`; capped at 100 pages or 10k rows (`400`) | [Zendesk pagination](https://developer.zendesk.com/api-reference/introduction/pagination/) |
| Jira (PageBean endpoints) | offset | `startAt`, `maxResults` | `total`, `isLast`, `nextPage` URL, `values` | [Jira v3 OpenAPI](https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json) |
| Jira issue search | opaque token | `/search/jql`: `nextPageToken`, `maxResults` (old `/search` deprecated) | `nextPageToken`, `isLast`; **no total** | [Jira v3 OpenAPI](https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json), [Issue search](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-search/) |
| PagerDuty | offset (classic) or cursor | classic: `offset` (≤10,000), `limit`, `total=true`; cursor endpoints (audit records, automation actions): `cursor`, `limit` | classic: `more: bool`, `total` (`null` unless requested); cursor: `next_cursor` (`null` at end) | [PagerDuty OpenAPI](https://github.com/PagerDuty/api-schema), [Pagination](https://developer.pagerduty.com/docs/rest-api-v2/pagination/) |
| Elasticsearch / OpenSearch | offset, or `search_after` in body | `from`, `size` (from+size ≤ `index.max_result_window`, 10k); `search_after: [sort values]` + `sort` (+ PIT) | `hits.total.value` + `hits.total.relation` (`eq`/`gte`); `track_total_hits` default 10k, `false` omits it | [ES paginate](https://www.elastic.co/docs/reference/elasticsearch/rest-apis/paginate-search-results), [Search API](https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-search). OpenSearch unchecked |
| Algolia | page number, **zero-based** | `page`, `hitsPerPage` (≤1000) | `nbPages`, `nbHits`, `page`; `paginationLimitedTo` 1000 | [page](https://www.algolia.com/doc/api-reference/api-parameters/page/), [hitsPerPage](https://www.algolia.com/doc/api-reference/api-parameters/hitsPerPage/) |
| GitHub search | page number + `Link` | `page`, `per_page` (≤100) | `total_count`, `incomplete_results` in body; 1,000-result cap | [GitHub search](https://docs.github.com/en/rest/search/search) |
| json-server v1 | page number, **body envelope** | `_page`, `_per_page` (`_limit` deprecated) | `{first, prev, next, last, pages, items, data}`; `next: null` at end; out-of-range page clamps to last. No `X-Total-Count` | [README](https://github.com/typicode/json-server/blob/main/README.md), [paginate.ts](https://github.com/typicode/json-server/blob/main/src/paginate.ts) |
| json-server v0.17 | offset or page | `_page`/`_limit`; `_start`/`_end`/`_limit` | `Link` header (first/prev/next/last) for `_page`; **`X-Total-Count`** for slices | [v0.17.4 README](https://github.com/typicode/json-server/blob/v0.17.4/README.md) |
| Discord | keyset | `before`/`after`/`around=<id>` (mutually exclusive), `limit` 1–100 (default 50) | bare array; stop on a short or empty page | [Get Channel Messages](https://docs.discord.com/developers/resources/message#get-channel-messages) |
| Reddit | cursor in body | `after`/`before=<fullname>`, `limit`, `count` | `data.after` / `data.before` (`null` at end) | [reddit.com/dev/api](https://www.reddit.com/dev/api/) |
| GraphQL (GitHub v4, Shopify) | Relay cursor **in variables** | `first`/`last` (GitHub 1–100, Shopify ≤250), `after: $cursor` | `pageInfo.endCursor`, `pageInfo.hasNextPage`. Linear unchecked | [GitHub GraphQL pagination](https://docs.github.com/en/graphql/guides/using-pagination-in-the-graphql-api), [Shopify GraphQL pagination](https://shopify.dev/docs/api/usage/pagination-graphql) |

## Beyond pagination (corrected)

Only rows with a vendor-specific claim are re-checked. Generic rows
(query map, JSON body, TLS, unix socket, proxy, size cap, shared config)
make no claim about a vendor and are unchanged.

| Concern | Where it shows up (corrected) | Source |
|---|---|---|
| **`User-Agent` required** | GitHub: "Requests with no `User-Agent` header will be rejected", and an invalid one gets `403`. Reddit: default UAs are "drastically limited"; format `<platform>:<app ID>:<version> (by /u/<user>)`. Wikipedia unchecked | [GitHub getting started](https://docs.github.com/en/rest/using-the-rest-api/getting-started-with-the-rest-api), [Reddit API wiki](https://github.com/reddit-archive/reddit/wiki/API) |
| **429 + `Retry-After`** | Sends `Retry-After`: Slack (`429`), Discord (`429`, plus body `retry_after` float), Shopify REST (`429`, fractional `Retry-After: 2.0`), Microsoft Graph (`429`), GitLab (`429`), Zendesk (`429`), Notion (`429` **and `529`**, integer seconds), Spotify ("normally"). **No `Retry-After`**: GitHub primary limit (`403` or `429`, wait for `x-ratelimit-reset`; `retry-after` only on secondary limits), Stripe (`429` + `Stripe-Rate-Limited-Reason`; `429` `lock_timeout` is not a rate limit), Airtable (fixed 30 s), Okta, X, HubSpot. AWS: some services send `x-amz-retry-after` (ms); Lambda puts `retryAfterSeconds` in the body | [GitHub](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [Stripe](https://docs.stripe.com/rate-limits), [Slack](https://docs.slack.dev/apis/web-api/rate-limits), [Discord](https://docs.discord.com/developers/topics/rate-limits), [Shopify](https://shopify.dev/docs/api/admin-rest/usage/rate-limits), [Graph](https://learn.microsoft.com/en-us/graph/throttling), [GitLab](https://docs.gitlab.com/administration/settings/user_and_ip_rate_limits/), [Zendesk](https://developer.zendesk.com/api-reference/introduction/rate-limits/), [Notion](https://developers.notion.com/reference/request-limits), [Spotify](https://developer.spotify.com/documentation/web-api/concepts/rate-limits), [Airtable](https://airtable.com/developers/web/api/rate-limits), [AWS retries](https://docs.aws.amazon.com/sdkref/latest/guide/feature-retry-behavior.html) |
| **Rate-limit headers (for display or pre-emptive waits)** | GitHub `x-ratelimit-limit/-remaining/-used/-reset/-resource` (reset = epoch). GitLab `RateLimit-Limit/-Remaining/-Reset/-Observed/-Name` (+ `RateLimit-ResetTime` HTTP date on 429). Okta `X-Rate-Limit-Limit/-Remaining/-Reset` (epoch). X `x-rate-limit-*` (epoch). Discord `X-RateLimit-Limit/-Remaining/-Reset/-Reset-After/-Bucket/-Scope/-Global`. Sentry `X-Sentry-Rate-Limit-*` (+ concurrent). HubSpot `X-HubSpot-RateLimit-*` (none on search). Mastodon `X-RateLimit-*` (reset format not stated). Reddit `X-Ratelimit-Used/-Remaining/-Reset` (seconds until). Shopify `X-Shopify-Shop-Api-Call-Limit: 32/40` | as above, plus [Okta](https://developer.okta.com/docs/reference/rl-best-practices/), [X](https://docs.x.com/x-api/fundamentals/rate-limits), [Sentry](https://docs.sentry.io/api/ratelimits/), [HubSpot](https://developers.hubspot.com/docs/developer-tooling/platform/usage-guidelines), [Mastodon](https://docs.joinmastodon.org/api/rate-limits/), [Reddit](https://github.com/reddit-archive/reddit/wiki/API) |
| **Conditional GET** (`ETag` → `If-None-Match`, 304) | GitHub: a 304 doesn't count against the primary limit **only when sent with an `Authorization` header**. k8s and CDNs unchecked | [GitHub best practices](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api) |
| **Error message extraction** | Graph: `error.message` (plus `error.code`). HubSpot 429: `errorType`, `policyName`. X: `errors[].message`. Discord 429: `message`. GitHub `message`, Stripe `error.message`, Google `error.message` and DRF `detail` unchecked this pass | [Graph](https://learn.microsoft.com/en-us/graph/throttling), [X](https://docs.x.com/x-api/fundamentals/rate-limits), [HubSpot](https://developers.hubspot.com/docs/developer-tooling/platform/usage-guidelines) |
| **GraphQL errors on HTTP 200** | Confirmed for GitHub: exceeding the primary rate limit returns HTTP `200` with an error and `x-ratelimit-remaining: 0` (secondary: `200` or `403`). Shopify `THROTTLED` unchecked | [GitHub GraphQL limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api) |
| **NDJSON parsing in `follow`** | k8s watch: newline-delimited `{type, object}` events (`ADDED`/`MODIFIED`/`DELETED`/`BOOKMARK`/`ERROR`); expired resourceVersion → `410 Gone` | [k8s API concepts](https://kubernetes.io/docs/reference/using-api/api-concepts/) |
| **Reconnect on stream drop** | k8s: watch and `continue` tokens expire → `410 Gone`, so the client must re-list rather than resume | [k8s API concepts](https://kubernetes.io/docs/reference/using-api/api-concepts/#retrieving-large-results-sets-in-chunks) |
| **Response headers as data** | Totals: GitLab `X-Total` (absent above 10k rows), json-server **v0 only** `X-Total-Count`. Links: `Link` (GitHub, GitLab, Okta, Sentry, Shopify, Mastodon, Confluence) | see pagination rows |
| **Auth shorthands, OAuth2 client credentials, form bodies, SSE sources** | Unchecked this pass; nothing in them bears on pagination | none |

## What this means for the schema

- `link_header` needs to match on a `rel` value (`next`) and optionally
  on an extra link attribute (Sentry's `results="true"`).
- Every strategy stops on its own signal: a missing or empty token, a
  missing link, `has_more: false`, `isLast`, or `done`. A short page is
  a separate, opt-in `stop_on`. Put a `max_pages` cap and a
  repeated-cursor guard on every strategy (json-server clamps pages;
  Sentry always links).
- Cursors aren't always strings: DynamoDB's `LastEvaluatedKey` is an
  object. AWS is out of scope anyway because of SigV4, so strings are
  enough for now.
- `page` needs `page_start: 0|1` (Algolia is zero-based).
- `total_path` should accept `header:` sources and tolerate a missing
  total. Elasticsearch's `relation: gte`, GitLab above 10k rows,
  PagerDuty and Stripe (opt-in), and Graph (first page only) all make
  the total optional or a lower bound.
- `retry` has to cope with every format in Corrections 7–9: fractional
  `Retry-After`, reset headers as epoch seconds, seconds from now or an
  HTTP date, and `Retry-After` as seconds or an HTTP date. Retrying
  `529` should be configurable. A GitHub `403` with
  `x-ratelimit-remaining: 0` is a rate limit, not an auth failure.
- Resolve `next_path` against the request URL in every case. OData
  4.01, Confluence and Salesforce all return relative URLs.
