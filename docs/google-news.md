# Google News, Google Alerts, and Click-Through Links

An aggregator publishes links of its own so that it can count the click. Those
links are opaque, and a feed built from one would carry teasers instead of
articles, so `fulltext=1` opens them first.

## Two Kinds, Handled Differently

### 1. Links that name their destination in the query string

- Google Alerts: `google.com/url?…&url=…`
- Google Search: `google.com/url?…&q=…`
- Bing News: `bing.com/news/apiclick.aspx?…&url=…`

These are **read, not fetched**. They cost no request, so the item is published
with the publisher's own link, and filters like `domain=` and `url_contains=`
see the publisher rather than the aggregator.

### 2. Google News links (since July 2024)

The path is an opaque token and the page it opens is a JavaScript shell that
redirects in the browser. The publisher URL is obtained from Google with two
extra requests, and it is used for **the article body only** — the entry keeps
its Google News link, which already works for a reader and stays stable.

```
/extract?url=https://news.google.com/rss/search?q=agentic+ai&hl=en-US&gl=US&ceid=US:en&fulltext=1
```

## Robots.txt

Google News and Google Alerts both forbid automated clients from the very path
that serves their feed (`news.google.com` disallows everything, `google.com`
disallows `/alerts/`), so reading them needs `respect_robots: false` on the host.
A config for each ships in `configs/sites/`:

```yaml
# configs/sites/news.google.com.yml
respect_robots: false
# and configs/sites/www.google.com.yml for an Alerts feed
```

Both are scoped to one host. `google.com` itself and any other Google host keep
the check, and `-no-robots` is still what turns it off everywhere — which is why
these two hosts are a config rather than a flag.

## Cost and Caveats

- **Three requests per item** for Google News (signature page, decode endpoint,
  then the article) instead of one. `fulltext_max` is the knob, and
  `per_host_interval` spaces the requests to the same host.
- **`domain=` filtering does not apply to Google News items**, because their
  published link is still `news.google.com`. The publisher is only known at body
  fetch time, and the entry is not rewritten. `url_contains=` and
  `text_contains=` work on the Google News URL; use `url=` with the publisher's
  own feed when you need to select by site.
- The decoder talks to an undocumented Google endpoint that has already changed
  shape more than once, so it is written to fail quietly: a link that cannot be
  opened is counted in `X-Feedme-Fulltext` as a failure, its item keeps the
  teaser, and the feed is otherwise complete. `feedme probe <google news link>`
  reports what happened, including the publisher URL when it was obtained.

## Google Alerts

A Google Alerts feed is an Atom feed, and works the same way as any other:
`url=https://www.google.com/alerts/feeds/<id>/<id>&fulltext=1`, or the same
address in `feeds[]`. Its items are click-through links of the first kind, so
they come out pointing straight at the publisher and `domain=` selects by site.
The items that stay without a body are the publishers that refuse the request — a
403 from a paywalled site, or a page that is built client-side — and they keep
their teaser, which is what a full-text feed does with a page it cannot read.
