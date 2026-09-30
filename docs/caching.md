# Caching

Two caches sit between a reader and the source site, both in SQLite:

## Feed Cache

The **feed cache** stores the fully rendered document (`feed_cache`, plus the
`feed_items` of the current rendering). A cache hit answers the request without
running the pipeline at all. The items are swept when the cached body goes, so
an expired feed never leaves an orphaned item list behind.

## Build History

The **build history** (`feed_builds`) records every build, successful or not,
with the detector it used and any error. Its newest row per feed is the
registry `/feeds` lists feeds from, so a feed stays findable after its cached
body has expired. Only **Forget** removes it.

## HTTP Cache

The **HTTP cache** stores fetched responses (`http_cache`) so a repeated fetch
can be avoided. The pipeline reads listings, merged feeds, and article bodies
**fresh every time**: a feed is only rebuilt once the feed cache has expired,
and at that point a cached page would only publish a stale feed without saving
a request. Today the HTTP cache is consulted for `robots.txt`, whose own
in-memory cache shares the same `cache_ttl`.

## Feed Identity

The feed's identity is its canonical URL, which is also written into the
document as `rel="self"`. Two spellings of the same feed (`?url=X` and
`?url=X&format=rss`) share one entry; different options (`?url=X` and
`?url=X&max=1`) are different feeds.

## Cache Lifetime

A rendered feed is reused for `ttl` minutes, or 15 minutes by default. The same
window is advertised to readers through `Cache-Control`. An `ETag` and a
`Last-Modified` time let a reader revalidate with a `304` that costs nothing;
`If-None-Match` is the stronger validator, so it wins when a client sends both.

## Refresh and Fresh

- `refresh=1` bypasses the feed cache and rebuilds from the source, for this
  request only. It also answers a request that cached a 5xx.
- `fresh=1` asks for the same thing on every request: because it is part of the
  URL, a `fresh=1` feed is a different feed from the same URL without it, is
  never written to the feed cache, and answers with `Cache-Control: no-store`.
  It is still recorded in the build history like any other build.

## Pruning

Both caches are pruned hourly, and once at startup. The build history is pruned
in the same pass, keeping the newest twenty builds of each feed and always the
newest, so no feed drops off `/feeds`.
