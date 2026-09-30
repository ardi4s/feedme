# Limitations

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
- No per-client rate limiting on feed endpoints; use a reverse proxy.
- The server is a single binary with SQLite; it does not scale horizontally
  (shared database would need a proper RDBMS).
