# Merging Existing Feeds

Give `feeds` one or more RSS, Atom, or JSON Feed URLs and their entries are read
into the same pipeline as scraped ones:

```
/extract?feeds[]=https://a.example/rss&feeds[]=https://b.example/feed.xml&fulltext=1
```

## Behavior

- Entries from every source are merged, **deduplicated by GUID** (then link,
  then title+date), and sorted by date. The first source that reads names the
  feed when `title` is not set.
- Every normal option still applies: filters, `max`, `fulltext`, `guid`, `ttl`,
  `fresh`, and the output `format`.
- `url` and `feeds` can be combined to mix a scraped listing with existing
  feeds.
- With `fulltext=1`, each merged entry's article is fetched and used as the
  body, which is how a summary-only feed becomes a full-text one.
- A source that cannot be read is skipped and counted; the merge only fails when
  **every** source fails. The `X-Feedme-Feeds` header reports
  `N fetched, M failed`.

## Deduplication

Items are deduplicated by GUID first (from the feed's `<guid>`, `<id>`, or
JSON `id` field), then by normalized link, then by title+date as a fallback.
This prevents duplicate items when feeds have different tracking parameters but
the same content.

No `url` is required when `feeds` is given.
