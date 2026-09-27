# Changelog

Newest first. Versions are `MAJOR.MINOR.PATCH`.

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
