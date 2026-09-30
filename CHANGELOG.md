# Changelog

Newest first. Versions are `MAJOR.MINOR.PATCH`.

## 0.3.0 — 2026-09-30

- A feed address in the `url` field is read as a feed. Pasting one there used to
  fail with `no items found`, because there is no HTML in an RSS document for the
  listing path to look at, and `feeds[]` was the only field that worked. The
  document is now read as what it is, so a URL copied from a reader works where
  it is copied. A page that only mentions a feed URL is still read as a page, and
  the two fields give the same feed.
- OPML export. `/feeds/opml` serves every listed feed as one OPML 2.0 file —
  one `outline` per feed, with the `/extract` URL as its `xmlUrl` — so a reader
  imports the whole list in one step. It is reached from a new toolbar button
  on `/feeds` and sits behind the same token; with no token it is open, like
  the page itself.
- A **Preview** action per feed on `/feeds`. It rebuilds the feed from its own
  configuration and shows its items as a page, so a feed can be checked without
  pasting its URL anywhere. It reads the source rather than the stored copy.
- Full-text feeds from Google News. Its item links have been opaque since July
  2024 — the page they open is a shell that redirects in the browser — so the
  body came back as a shell and every item kept its teaser. The publisher's page
  is now fetched, at two extra requests per item, and a link that cannot be
  opened leaves the item with its teaser instead of failing the build. The
  published link is still the Google News one, which a reader can follow as it
  stands. Reading the feed needs `respect_robots: false` for the host, since
  Google forbids automated clients from that path, and a config for it now ships
  in `configs/sites/`; see
  [Google News, Google Alerts, and click-through links](README.md#google-news-google-alerts-and-click-through-links).
- Click-through links that name their destination in their own query string —
  Google Alerts and Bing News among them — are opened before the feed is
  filtered, so the item is published with the publisher's link and a `domain=`
  or `url_contains=` rule matches the article rather than the aggregator. A
  Google Alerts feed is now a full-text feed, like any other feed you can hand
  `feeds[]` or `url`.
- Google's click-through is read under both parameter names it uses: `url` for
  an Alerts item and `q` for a search-result link. Reading only one of them
  would have left every Alerts item a wrapper.
- `feedme probe` opens a click-through link and reports the publisher URL, so a
  feed that came back with teasers can be diagnosed the same way any other
  extraction problem is.
- A site config may set `respect_robots: false` for one host, which is narrower
  than `-no-robots` and silences nothing else. A robots refusal now names both
  ways out instead of only reporting that it happened. Two such configs are
  bundled, for `news.google.com` and `www.google.com`, so a Google News or Google
  Alerts feed is readable without meeting the 403 first. Each covers one host and
  leaves every other host's rules in force.
- `robots.txt` is no longer requested from the wrong port: the origin was built
  from the hostname alone, so a service on a non-default port had its rules read
  from another server's file, or from none at all.

## 0.2.0 — 2026-09-27

- Sign in to the management page through a form at `/login` instead of an HTTP
  Basic prompt, which browsers have stopped showing reliably: Chromium answers
  the navigation with an error page, leaving the operator locked out. The form
  exchanges the token for a session cookie scoped to `/feeds`. The token itself
  is still accepted as a bearer token or as a Basic password, so scripts and
  `curl` are unaffected.

## 0.1.0 — 2026-09-27

First release.

- Serve a listing page as RSS 2.0, Atom 1.0, or JSON Feed 1.1 through
  `/extract`, one URL per feed.
- Detect item containers automatically, with `item`, `body`, `title`, `date`,
  and `summary` selectors to override the guess.
- Merge existing feeds, with `filter`, `filterout`, `strip`, deduplication, and
  `fulltext=1` to replace summaries with article bodies.
- `/feeds` lists every feed the server has built, grouped by registrable
  domain, with server-side sorting, bulk refresh, and forget.
- `FEEDME_ADMIN_TOKEN` optionally guards the management page.
- Optional headless-browser rendering through `render_url` for listings built
  by JavaScript.
- Single static binary, SQLite for the HTTP and feed caches, and a `probe`
  subcommand for working out why a feed came back empty.
