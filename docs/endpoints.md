# Endpoints

| Path | Purpose |
| --- | --- |
| `/extract` | Build and return a feed. This is the one readers subscribe to. |
| `/check` | Render an HTML page describing what a feed URL would produce, without fetching the source (so it cannot leak the server's IP to an arbitrary site). |
| `/preview` | Build the feed and render its items as HTML, so you can see what you are about to subscribe to. |
| `/healthz` | Liveness check. |
| `/feeds` | Management page: every feed that has been built, grouped by the page it was built from. Behind the token when one is set. |
| `/feeds/opml` | Export every listed feed as OPML 2.0 for import into a reader. Behind the same token as `/feeds`. |
| `/login` | Sign-in form for the management page. Trades the token for a session cookie, so a browser never has to meet a Basic prompt. |
| `/metrics` | Prometheus metrics endpoint (when enabled via `-render-url` or programmatically). |
| `/feedme.png`, `/favicon.ico`, `/favicon.png` | The brand image and favicons. They are embedded in the binary, so the runtime image needs no static directory. |
| `/` | A four-mode form — Automatic, Simple, Advanced, Merge — with a Preview button that builds the feed and shows its items. |

## Response Headers

A successful `/extract` responds with the feed, an `ETag`, a `Last-Modified`, a
`Cache-Control`, `X-Feedme-Items`, and — where relevant — `X-Feedme-Detection`,
`X-Feedme-Confidence`, `X-Feedme-Fulltext`, `X-Feedme-Feeds`, and
`X-Feedme-Feedcache` (the whole rendered feed came from storage and no build
ran). Every `/extract` response also carries
`X-Robots-Tag: noindex, nofollow`, because a generated feed is a view of someone
else's content and should not be indexed.

## Error Responses

Errors are plain text with a status code:

| Status | Cause |
| --- | --- |
| `400` | A parameter is missing or malformed; the message names the field. |
| `403` | robots.txt denied the fetch, a private address was blocked, or the site returned a bot challenge. |
| `404` | The source page does not exist. |
| `502` | The source is unreachable, returned a 5xx, a 202 challenge, or an oversized body. |
| `503` | The source rate-limited us (429). |
