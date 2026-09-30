# Parameters Reference

Every parameter is optional except `url`. Repeated parameters may be given as
`key=a&key=b`, `key[]=a&key[]=b`, or the indexed `key[0]=a&key[1]=b`. All three
spellings are accepted. A blank value is the same as leaving the parameter out,
except for booleans, where a present-but-empty value is `false`.

## Source

| Parameter | Default | Meaning |
| --- | --- | --- |
| `url` | | The listing page to build a feed from, or an existing feed to read as one. Required unless `feeds` is given. Must be `http` or `https`. |
| `feeds` | | One or more existing RSS, Atom, or JSON Feed URLs to read and merge. Repeatable (bare or `feeds[]`). |
| `id_or_class` | | A bare element id or class name, e.g. `news-item`. A short way to say `item`. Cannot be combined with `item`. |
| `keep_qs_params` | `1` | Query string for item links: `1` keeps everything (minus campaign noise), `0` drops it, or a comma-separated list of parameter names to keep. |

A `url` that is already a feed is read as what it is, so a feed address copied
out of a reader works where it was copied, and `url` and `feeds[]` give the same
feed for the same document. The body is already fetched, so a feed costs no
request that a listing page does not. A page that merely mentions a feed URL is
still read as a page.

## Extraction

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

## Filtering

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

## Output

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

## Fetching and rendering

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
