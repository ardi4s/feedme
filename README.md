# feedme

### RSS feed creator

Turn any web page into an RSS 2.0, Atom 1.0, or JSON Feed 1.1 feed, including
sites that publish no feed of their own — then read it in any feed reader.

Every option of a feed lives in its feed URL, so a feed is a URL you can edit,
share, and keep. There is nothing to register first: the server builds a feed
the first time it is requested.

It also merges: give it existing feeds and it will combine, filter, and — with
`fulltext=1` — upgrade them to full article text.

![The front page: paste a listing page, or merge existing feeds](docs/brand/home.png)

![The management page: every feed the server has built, grouped by site](docs/brand/hero.png)

```
http://localhost:8080/extract?url=https://example.com/news&id_or_class=news-item&fulltext=1
http://localhost:8080/extract?feeds[]=https://a.example/rss&feeds[]=https://b.example/feed.xml&fulltext=1
```

## Build and run

```sh
make all            # vet, test, and build ./bin/feedme
./bin/feedme serve  # listen on :8080
```

Go 1.25 or newer. Each [release](https://github.com/ardi4s/feedme/releases)
carries this build as a tarball for linux/amd64 and linux/arm64 — the binary,
the site configs and the licence, with `checksums.txt` beside it. The
[container image](https://github.com/ardi4s/feedme/pkgs/container/feedme) is the
other way in, and [Docker](#docker) covers that.

`feedme serve` flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `:8080` | Listen address. |
| `-config` | | Path to a YAML config file. |
| `-db` | user config dir | SQLite path for the HTTP and feed caches. |
| `-site-dir` | `configs/sites` | Directory of per-host site overrides. |
| `-user-agent` | built-in | User-Agent sent to source sites. |
| `-timeout` | `20s` | Per-request timeout. |
| `-fulltext-parallel` | `4` | How many article bodies to fetch at once. |
| `-render-url` | | Base URL of a headless-browser service (browserless-compatible). Enables `render_js`. |
| `-no-robots` | `false` | Ignore robots.txt (use only where you have permission). |
| `-allow-private` | `false` | Permit requests to private IP ranges, for LAN testing. |
| `-admin-token` | `$FEEDME_ADMIN_TOKEN` | Password for the `/feeds` management page. Empty leaves it open. |
| `-log-level` | `info` | `debug`, `info`, `warn`, or `error`. |
| `-quiet` | `false` | Log warnings and errors only. |

`feedme probe <url>` fetches a page once and reports what extraction found. It
is the tool to reach for when a feed comes back empty. Run `feedme <url>` as a
shortcut, and `feedme help` for the command list.

### Docker

```sh
mkdir -p data
docker compose up -d
```

`docker compose` pulls the image from GHCR — built for linux/amd64 and
linux/arm64, with no build step — and the database is a bind mount at
`./data/feedme.db`, so the container runs as your host user. `FEEDME_UID` and
`FEEDME_GID` default to `1000:1000`; setting them from `id -u`/`id -g` is what
lets the container write the mount:

```sh
FEEDME_UID=$(id -u) FEEDME_GID=$(id -g) docker compose up -d
```

The compose file names one release (`ghcr.io/ardi4s/feedme:0.1.0`), so a
`docker compose pull` cannot move a deployment to a new version on its own.
`:0.1`, `:v0.1.0` and `:latest` are published too, for following a release line
rather than one release. `docker-compose.override.yml` is where a choice that is
true of one machine belongs, and the repository ignores it:

```yaml
# docker-compose.override.yml — never committed.
services:
  feedme:
    build: .            # build from the checkout instead of pulling
    image: feedme:local # a local name, so a build does not overwrite the pull
    user: "1001:1001"   # this machine's uid, so ./data is writable
    networks: [shared]
networks:
  shared:
    external: true
```

A headless browser is available as an opt-in profile; see
[Headless browser](#headless-browser).

Set `FEEDME_ADMIN_TOKEN` to put the management page behind a password. Compose
passes the variable through when it is set:

```sh
FEEDME_ADMIN_TOKEN=$(openssl rand -hex 24) docker compose up -d
```

## Endpoints

| Path | Purpose |
| --- | --- |
| `/extract` | Build and return a feed. This is the one readers subscribe to. |
| `/check` | Render an HTML page describing what a feed URL would produce, without fetching the source (so it cannot leak the server's IP to an arbitrary site). |
| `/preview` | Build the feed and render its items as HTML, so you can see what you are about to subscribe to. |
| `/healthz` | Liveness check. |
| `/feeds` | Management page: every feed that has been built, grouped by the page it was built from. Behind the token when one is set. |
| `/login` | Sign-in form for the management page. Trades the token for a session cookie, so a browser never has to meet a Basic prompt. |
| `/feedme.png`, `/favicon.ico`, `/favicon.png` | The brand image and favicons. They are embedded in the binary, so the runtime image needs no static directory. |
| `/` | A four-mode form — Automatic, Simple, Advanced, Merge — with a Preview button that builds the feed and shows its items. |

A successful `/extract` responds with the feed, an `ETag`, a `Last-Modified`, a
`Cache-Control`, `X-Feedme-Items`, and — where relevant — `X-Feedme-Detection`,
`X-Feedme-Confidence`, `X-Feedme-Fulltext`, `X-Feedme-Feeds`, and
`X-Feedme-Feedcache` (the whole rendered feed came from storage and no build
ran). `X-Feedme-Cache` would mark a build answered from the HTTP cache instead
of the network; the pipeline reads every source fresh, so it does not appear
today. Every `/extract` response also carries
`X-Robots-Tag: noindex, nofollow`, because a generated feed is a view of someone
else's content and should not be indexed.

Errors are plain text with a status code:

| Status | Cause |
| --- | --- |
| `400` | A parameter is missing or malformed; the message names the field. |
| `403` | robots.txt denied the fetch, a private address was blocked, or the site returned a bot challenge. |
| `404` | The source page does not exist. |
| `502` | The source is unreachable, returned a 5xx, a 202 challenge, or an oversized body. |
| `503` | The source rate-limited us (429). |

## Feed readers

A feedme feed is a URL you paste into a reader: nothing on the source site
points at it, so add the `/extract` URL itself rather than the site's home page.
Any reader that accepts a pasted feed URL will do. Six open-source ones, listed
without ranking:

| Reader | Platform | License |
| --- | --- | --- |
| [yarr](https://github.com/nkanaev/yarr) | Windows, macOS, Linux — desktop app or self-hosted web | MIT |
| [Fluent Reader](https://github.com/yang991178/fluent-reader) | Windows, macOS, Linux — desktop app | BSD-3-Clause |
| [FreshRSS](https://github.com/FreshRSS/FreshRSS) | Self-hosted server — web UI, or any device through its API | AGPL-3.0 |
| [Read You](https://github.com/ReadYouApp/ReadYou) | Android | GPL-3.0 |
| [Feeder](https://github.com/spacecowboy/feeder) | Android | GPL-3.0 |
| [Twine](https://github.com/msasikanth/twine) | Android, iOS | GPL-3.0 |

## Parameters

Every parameter is optional except `url`. Repeated parameters may be given as
`key=a&key=b`, `key[]=a&key[]=b`, or the indexed `key[0]=a&key[1]=b`. All three
spellings are accepted, so a feed URL written by another tool usually works
unchanged. A blank value is the same as leaving the parameter out, except for
booleans, where a present-but-empty value is `false`.

### Source

| Parameter | Default | Meaning |
| --- | --- | --- |
| `url` | | The listing page to build a feed from. Required unless `feeds` is given. Must be `http` or `https`. |
| `feeds` | | One or more existing RSS, Atom, or JSON Feed URLs to read and merge. Repeatable (bare or `feeds[]`). |
| `id_or_class` | | A bare element id or class name, e.g. `news-item`. A short way to say `item`. Cannot be combined with `item`. |
| `keep_qs_params` | `1` | Query string for item links: `1` keeps everything (minus campaign noise), `0` drops it, or a comma-separated list of parameter names to keep. |

### Extraction

Selectors are CSS by default; prefix with `xpath:` to use XPath. When no `item`
selector is given (or none match and auto-detection is allowed), the service
detects the repeating item container itself and reports the choice in
`X-Feedme-Detection`.

| Parameter | Meaning |
| --- | --- |
| `item` | Selector for each item container. Repeatable; the selectors are unioned, so one feed can cover several card blocks. |
| `item_url` | Selector for the item's link, resolved against the page URL. |
| `item_title` | Selector for the title (falls back to the link text, then the page title). |
| `item_date` | Selector for the date (`time[datetime]`, machine-readable formats, and common text dates are understood). |
| `item_desc` | Selector for the summary. |
| `item_image` | Selector for the item image. |
| `allow_cross_host` | `0` | Keep links to a different site. Off by default; same-site links, including subdomains such as `news.example.com`, are always kept. |
| `force_host` | | Rewrite every item link and image onto this bare host, keeping the scheme, path, and query. |

### Links that point at another host

By default only same-site links become items, where "site" is the registrable
domain — `news.example.com` and `www.example.com` count as one site, so a portal
whose articles live on its own subdomains works without configuration.
`allow_cross_host=1` keeps links to a different site, and
`force_host=<host>` rewrites every item link and image onto a bare host while
keeping the scheme, path, and query:

```
/extract?url=https://example.com/&force_host=www.example.com&fulltext=1
```

The rewrite happens before a link is checked and before detection groups links,
so it unifies links that would otherwise split into one pattern per host.

### Filtering

| Parameter | Meaning |
| --- | --- |
| `remove` | Selector for subtrees to delete outright (navigation, ads, share bars). |
| `strip_css` | Blanks the **text** of matching elements while keeping the elements themselves, so a wrapper can stay without its wording (a "Read more" strapline, an ad label). |
| `url_contains` | Keep only items whose URL contains this. Repeatable. |
| `strip_if_url` | Drop items whose URL contains this. Repeatable. |
| `uri_matches` | Keep only items whose URL matches this regular expression. |
| `text_contains` | Keep only items whose text contains this. Repeatable. |
| `strip_if_text` | Drop items whose text contains this. Repeatable. |
| `domain` | Keep only items whose URL host is this domain. Repeatable. |
| `date_after` | Drop items older than this date. |
| `date_before` | Drop items newer than this date. |

### Output

| Parameter | Default | Meaning |
| --- | --- | --- |
| `format` | `rss` | `rss`, `atom`, or `json`. |
| `max` | `50` | Maximum items, capped at `200`. With `fulltext=1` it is also capped by `fulltext_max`. |
| `title` | page title | Feed title. |
| `description` | | Feed description. |
| `lang` | | Feed language, e.g. `en` or `id`. |
| `guid` | `link` | Item identity: `link` (the URL), `stable` (a hash of title and date, so it survives a URL change), or `title`. |
| `ttl` | `0` | Caching hint in minutes. `0` uses the 15-minute default. |
| `summary_chars` | `0` | Truncate the summary to this many characters. `0` means no limit. |
| `fulltext_chars` | `0` | Truncate the article body to this many characters. `0` means no limit. |
| `refresh` | `0` | Bypass the feed cache and rebuild from the source, for this request only. |
| `fresh` | `0` | Like `refresh`, but part of the feed's URL: every request rebuilds from the source, the rendered feed is never stored, and readers are told `Cache-Control: no-store`. The build is still recorded in the build history, which is what lists feeds on `/feeds`. |

### Fetching and rendering

| Parameter | Default | Meaning |
| --- | --- | --- |
| `fulltext` | `0` | Fetch each article and put the full body in the feed. One fetch per item, so the effective item count is `min(max, fulltext_max)`. A partial result is normal and is reported in `X-Feedme-Fulltext`. |
| `fulltext_max` | `20` | How many items a full-text feed may fetch article bodies for, up to `200`. Raise it when you really want more than twenty full articles. |
| `html_cleanup` | `1` | Run the aggressive prune pass over full-text bodies. Set `0` to keep the article's markup closer to the original. |
| `user_agent` | server default | User-Agent for this request. |
| `referer` | | Referer for this request. |
| `render_js` | `0` | Load the page in a headless browser when the plain HTML yields no item list. Requires `render_url`. Without it the request fails with `501`. |

When full text is requested, an item keeps its teaser unless the extractor found
real article text. A body that is identical across several items — a price
widget, an ad slot, a paywall notice — is treated as boilerplate and dropped, so
the feed never replaces the teaser with the same wrong block. Those items are
counted in `X-Feedme-Fulltext` as failures.

## Merging existing feeds

Give `feeds` one or more RSS, Atom, or JSON Feed URLs and their entries are read
into the same pipeline as scraped ones:

```
/extract?feeds[]=https://a.example/rss&feeds[]=https://b.example/feed.xml&fulltext=1
```

- Entries from every source are merged, **deduplicated by link**, and sorted by
  date. The first source that reads names the feed when `title` is not set.
- Every normal option still applies: filters, `max`, `fulltext`, `guid`, `ttl`,
  `fresh`, and the output `format`.
- `url` and `feeds` can be combined to mix a scraped listing with existing
  feeds.
- With `fulltext=1`, each merged entry's article is fetched and used as the
  body, which is how a summary-only feed becomes a full-text one.
- A source that cannot be read is skipped and counted; the merge only fails when
  **every** source fails. The `X-Feedme-Feeds` header reports
  `N fetched, M failed`.

No `url` is required when `feeds` is given.

## Google News, Google Alerts, and click-through links

An aggregator publishes links of its own so that it can count the click. Those
links are opaque, and a feed built from one would carry teasers instead of
articles, so `fulltext=1` opens them first.

Two kinds, handled differently on purpose:

- **Links that name their destination in the query string** — a Google
  click-through `google.com/url?…&url=…` (Google Alerts) or `…&q=…` (a plain
  search result), and Bing News' `bing.com/news/apiclick.aspx?…&url=…` — are
  read, not fetched. They cost no request, so the item is published with the
  publisher's own link, and filters like `filter=example.com` and `domain=` see
  the publisher rather than the aggregator. A wrapper that answers with a
  redirect instead of naming its target costs one request, which is what a
  redirect is, and lands in the same place.
- **Google News links** cannot be read at all: since July 2024 the path is an
  opaque token and the page it opens is a JavaScript shell that redirects in the
  browser. The publisher URL is obtained from Google with two extra requests,
  and it is used for **the article body only** — the entry keeps its Google News
  link, which already works for a reader and stays stable.

```
/extract?url=https://news.google.com/rss/search?q=agentic+ai&hl=en-US&gl=US&ceid=US:en&fulltext=1
```

A feed address works in `url` as well as in `feeds[]`, and gives the same feed
either way. That is the field a reader copies an address into, and one that is
already a feed has nothing for the listing path to find. The document is read as
what it is, so this costs no request that a listing page would not. A page that
merely mentions a feed URL is still read as a page.

Google News and Google Alerts both forbid automated clients from the very path
that serves their feed (`news.google.com` disallows everything, `google.com`
disallows `/alerts/`), so reading them needs `respect_robots: false` on the host.
A config for each ships in `configs/sites/`, because the path is a published
endpoint meant for readers rather than something crawled, and the alternative is
every operator rediscovering the same 403:

```yaml
# configs/sites/news.google.com.yml
respect_robots: false
# and configs/sites/www.google.com.yml for an Alerts feed
```

Both are scoped to one host. `google.com` itself and any other Google host keep
the check, and `-no-robots` is still what turns it off everywhere — which is why
these two hosts are a config rather than a flag. `configs/sites/README.md`
describes `respect_robots` and the other keys.

Two things are worth knowing before relying on it:

- **Costs three requests per item** for Google News (the signature page, the
  decode, then the article) instead of one. `fulltext_max` is the knob, and
  `per_host_interval` spaces the requests to the same host.
- **`domain=` filtering does not apply to Google News items**, because their
  published link is still `news.google.com`. The publisher is only known at body
  fetch time, and the entry is not rewritten. `filter=` and `url_contains=` work
  on the Google News URL; use `url=` with the publisher's own feed when you need
  to select by site.

The decoder talks to an undocumented Google endpoint that has already changed
shape more than once, so it is written to fail quietly: a link that cannot be
opened is counted in `X-Feedme-Fulltext` as a failure, its item keeps the
teaser, and the feed is otherwise complete. `feedme probe <google news link>`
reports what happened, including the publisher URL when it was obtained.

A Google Alerts feed is an Atom feed, and works the same way as any other:
`feeds[]=https://www.google.com/alerts/feeds/<id>/<id>&fulltext=1`. Its items are
click-through links of the first kind, so they come out pointing straight at the
publisher and `domain=` selects by site. The three items that stay without a body
in any run are the publishers that refuse the request — a 403 from a paywalled
site, or a page that is built client-side — and they keep their teaser, which is
what a full-text feed does with a page it cannot read.

## Caching

Two caches sit between a reader and the source site, both in SQLite:

- The **feed cache** stores the fully rendered document (`feed_cache`, plus the
  `feed_items` of the current rendering). A cache hit answers the request without
  running the pipeline at all. The items are swept when the cached body goes, so
  an expired feed never leaves an orphaned item list behind.
- The **build history** (`feed_builds`) records every build, successful or not,
  with the detector it used and any error. Its newest row per feed is the
  registry `/feeds` lists feeds from, so a feed stays findable after its cached
  body has expired. Only **Forget** removes it.
- The **HTTP cache** stores fetched responses (`http_cache`) so a repeated fetch
  can be avoided. The pipeline reads listings, merged feeds, and article bodies
  **fresh every time**: a feed is only rebuilt once the feed cache has expired,
  and at that point a cached page would only publish a stale feed without saving
  a request. Today the HTTP cache is consulted for `robots.txt`, whose own
  in-memory cache shares the same `cache_ttl`.

The feed's identity is its canonical URL, which is also written into the
document as `rel="self"`. Two spellings of the same feed (`?url=X` and
`?url=X&format=rss`) share one entry; different options (`?url=X` and
`?url=X&max=1`) are different feeds.

A rendered feed is reused for `ttl` minutes, or 15 minutes by default. The same
window is advertised to readers through `Cache-Control`. An `ETag` and a
`Last-Modified` time let a reader revalidate with a `304` that costs nothing;
`If-None-Match` is the stronger validator, so it wins when a client sends both.

`refresh=1` is the way out of a stale feed without editing the URL, and it also
answers a request that cached a 5xx. It applies to one request. `fresh=1` asks
for the same thing on every request: because it is part of the URL, a
`fresh=1` feed is a different feed from the same URL without it, is never written
to the feed cache, and answers with `Cache-Control: no-store`. It is still
recorded in the build history like any other build.

Both caches are pruned hourly, and once at startup. The build history is pruned
in the same pass, keeping the newest twenty builds of each feed and always the
newest, so no feed drops off `/feeds`.

## Site configs

Most pages need nothing beyond the feed URL. When a host's markup needs more
help than selectors give it, drop a YAML file in `configs/sites/`. The filename
scopes it (`kompas.com.yaml` covers `kompas.com` and its subdomains); a `host:`
key overrides that, and `*.example.com` is a wildcard. A config with no host
anywhere is rejected.

No configs are bundled. A config is read only for the host it names, and only
fills in fields the request left empty: a selector given in the feed URL always
wins. The keys are `title`, `body`, `strip`, `item`, `url`, `date`, `summary`,
`strip_id_or_class`, `tidy`, `prune`, `autodetect_on_failure`,
`single_page_link`, `next_page_link`, `categories`, `test_urls`, plus
`force_host`, `allow_cross_host`, and `respect_robots`. See
[`configs/sites/README.md`](configs/sites/README.md).

Selectors given in the URL win over the site config, which only fills in fields
the request left empty. A `remove` list is the exception: the site config's
`strip` and the request's `remove` are combined, never narrowed.

## Configuration file

`feedme serve -config config.yaml` reads a YAML file over the defaults:

```yaml
db_path: /var/lib/feedme/feedme.db
site_dir: /etc/feedme/sites
user_agent: "feedme/0.1 (+https://example.com/bot)"
timeout: 20s
max_body_bytes: 5242880
global_concurrency: 8
per_host_interval: 1s
respect_robots: true
allow_private: false
cache_ttl: 24h             # freshness window for cacheable fetches (robots.txt)
max_elements: 100000
render_url: http://chromium:3000
render_timeout: 30s
```

## Headless browser

Some listings ship an empty shell over HTTP and fill it with JavaScript.
`render_js=1` handles those, but only when a browser is configured:

```sh
feedme serve -render-url http://localhost:3000
```

The server does not embed a browser. It speaks the browserless `POST /content`
contract over HTTP, so the browser runs as its own process — its memory use, its
crashes, and its attack surface stay out of the feed server.

The browser is optional and off by default. Nothing uses it unless a feed URL
carries `render_js=1`, and the ordinary HTML path never contacts it, so a
deployment without a browser still serves every listing that returns its items
in HTML. In the compose file the browser sits behind the `browser` profile:

```sh
# Server only (the default): no browser, no render_js.
docker compose up -d

# Server plus browser. They share the stack's own network, so the server
# reaches the browser by service name.
FEEDME_RENDER_URL=http://chromium:3000 docker compose --profile browser up -d
```

Rendering is a fallback, not the default path: the plain fetch is tried first,
and the browser is used only when that HTML yields no item list. The browser is
never published to the host; only the feed server reaches it, over the stack's
own network.

## Managing feeds

A feed is built when someone asks for it. There is no scheduler and no
subscription to keep: a reader's request rebuilds a feed whose cached body has
expired, and the cache lifetime (`ttl`, fifteen minutes by default) is the only
thing that paces rebuilds. A link built once is never lost, because it lives in
the build history rather than in a subscription.

`/feeds` lists every feed the server has built, grouped by the site it came
from:

- **Source link** — the group is the site, meaning its registrable domain
  (eTLD+1), so `kontan.co.id`, `www.kontan.co.id` and `nasional.kontan.co.id`
  are one group, collapsed by default. The exact page each feed was built from
  is on its own row, in the same column, because that is what tells one feed of
  a site apart from another; a group holding more than one page says so.
- **Feed** — the generated `/extract` link, with a copy button that puts the
  whole URL on the clipboard. The link shown is shortened in the middle; the
  copy button and the link's tooltip carry it in full.
- **Preview** — builds the feed again from its own configuration and shows its
  items as a page, so a feed can be checked without pasting its URL anywhere.
  It reads the source, not the stored copy.
- **Item** — how many entries the newest build produced.
- **Built** — when that build ran, as a distance (`3h ago`); the exact time is
  in the tooltip.
- **State** — `fresh` (a stored body is current), `stale` (a stored body has
  expired, and the next request rebuilds it), `not cached` (only the build
  history is left), or `failed` (the newest build produced nothing, and its
  error is shown).
- **Action** — **Refresh now** rebuilds through the same path a reader takes,
  and **Forget** removes the feed from the list and drops its build history.

The toolbar also has **Export OPML**, which serves every listed feed as one
OPML 2.0 file — one `outline` per feed, with the `/extract` URL as its
`xmlUrl` and the page it was built from as its `htmlUrl`. A reader imports the
file in one step and nothing about feed URLs changes. The download is an
attachment named for the day, and it lists the same feeds the page shows,
because both are read from the build history.

The header sorts the list (`?sort=source`, `feed`, `items`, `built`, or
`state`, with `?dir=asc`/`desc`) and offers a select-all checkbox. Checking rows
and choosing **Refresh** or **Delete** in the toolbar applies the action to all
of them in one request; the toolbar sticks to the top of the window while the
list scrolls, and its Apply button only enables once something is checked.
Sorting and the group disclosure work without JavaScript: the script on the
page only adds the select-all, the enable-on-select and the copy conveniences,
and the page falls back to showing every group expanded with a working Apply.

The build history is what the page lists from, so a feed appears the moment it
is first built and stays there after its cached body expires. Only **Forget**
removes it.

The management page is open by default, which suits a server on localhost or a
trusted network. Set `FEEDME_ADMIN_TOKEN` — or pass `-admin-token` — and
`/feeds` asks for it before it shows anything or accepts an action, the export
included.

A browser is sent to a sign-in form at `/login`, which trades the token for a
session cookie scoped to `/feeds`; closing the browser ends the session. It is a
form rather than an HTTP Basic prompt because browsers have stopped showing that
prompt reliably, and an operator who cannot get past a prompt cannot reach their
own page. A script can keep presenting the token directly, as
`Authorization: Bearer <token>` or as the password of a Basic pair.

Feed URLs are never behind the token, so readers are unaffected either way. The
environment variable is the better of the two places to set it, because a
command-line flag is visible in the process list to every user on the machine.

## Security

- **robots.txt** is honoured by default. A site config may opt one host out
  (`respect_robots: false`), which is what makes a feed readable at a publisher
  that forbids its own feed path; `-no-robots` turns the check off everywhere and
  is the heavier of the two.
- **SSRF protection**: hostnames resolving to private, loopback, link-local, or
  reserved addresses are refused unless `-allow-private` is set.
- **Size limits**: bodies are capped (`max_body_bytes`), as is the number of
  elements parsed (`max_elements`), and fetches are bounded by a global
  concurrency limit and a per-host interval.
- **The management page is unauthenticated until a token is set.** Set
  `FEEDME_ADMIN_TOKEN` before exposing `/feeds` to the internet. The feed
  endpoints stay public either way.
- **No per-client rate limiting.** Put the server behind a reverse proxy if it
  is exposed, and set a real `user_agent` so site operators can contact you.

## Limitations

- `render_js` needs a configured browser (`render_url`); without one, a request
  that asks for it fails with `501` rather than returning an empty feed.
- There is no pagination or "load more" following; a feed covers the listing
  page it is given.
- Full text fetches at most 20 article bodies per request by default; raise
  `fulltext_max` (up to 200) to fetch more.
- Google News links are resolved through an undocumented Google endpoint. It
  answers today and may stop; the failure mode is a feed with teasers, not an
  error, and nothing else in the program depends on it.
- Auto-detection is good but not magic. Reach for `feedme probe` and an explicit
  selector when it guesses wrong.
- `FEEDME_ADMIN_TOKEN` is one password shared by everyone who manages the
  server, not accounts. Everyone signs in with the same token, there is no way to
  revoke one holder without changing it for all of them, and the session cookie
  lasts until the browser closes.

## Development

```sh
make test     # go test ./...
make vet      # go vet ./...
make all      # vet + test + build
```

Tests avoid the network: fetchers are stubbed and fixtures live under
`testdata/`.

Brand images are generated rather than hand-drawn: `python3 tools/brand.py`
rebuilds `docs/brand/` and the favicons from the mark the server embeds.
