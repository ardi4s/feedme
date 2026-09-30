# Development

## Commands

```sh
make test     # go test ./...
make vet      # go vet ./...
make all      # vet + test + build
```

## Test Conventions

- Tests avoid the network: fetchers are stubbed and fixtures live under
  `testdata/`.
- Tests live in the same package as the code they test (`package web`, not
  `web_test`), so they can reach unexported functions.
- Use `net/http/httptest`; build requests with `httptest.NewRequest`.
- Prefer table-driven cases with a `name` field.
- Test the boundary, not the implementation: which credential is accepted,
  what status an unauthenticated caller sees, which cookie attributes are set.

## Brand Images

Brand images are generated rather than hand-drawn:
```sh
python3 tools/brand.py
```
rebuilds `docs/brand/` and the favicons from the mark the server embeds.

## Go Version

Go 1.25 or newer. The Makefile exports `GOTOOLCHAIN ?= go1.25.11`, so `make`
targets pick the right toolchain on their own. Running `go` directly outside
`make` needs `export GOTOOLCHAIN=go1.25.11` unless the system Go already
matches.

## Code Organization

| Package | Role |
| --- | --- |
| `web` | HTTP only: routing, status codes, cache headers, Go-assembled pages. |
| `feedurl` | Parses the stateless feed URL into a plan for building one. |
| `pipeline` | Runs one feed request end to end: fetch, detect, extract, filter, render. |
| `listpage` | Decides which elements of a listing page are the items. |
| `extract` | Pulls an article body out for `fulltext=1`. |
| `domx` | Low-level HTML helpers shared by the HTML-reading packages. |
| `fetch` | The HTTP client layer: rate limiting, robots, size caps, SSRF checks. |
| `render` | Asks a headless browser for rendered HTML, for `render_js=1`. |
| `feed` | Renders items as RSS 2.0, Atom 1.0, or JSON Feed 1.1. |
| `feedread` | Parses an existing RSS/Atom/JSON feed, so feeds can be merged. |
| `filter` | `filter`, `filterout`, `strip`, and deduplication. |
| `dates` | Parses the many date formats that appear in news markup. |
| `urlx` | URL helpers, including the registrable-domain logic. |
| `store` | SQLite: the caches and the build history. |
| `config` | Global settings and per-host site overrides. |
| `gnews` | Google News link resolution. |
| `redirect` | Click-through link resolution (Google Alerts, Bing News). |

The split is deliberate: **HTTP belongs in `web`, feed content belongs in
`pipeline` and its helpers, and request parsing belongs in `feedurl`.** A change
that puts extraction logic in `web`, or status codes in `pipeline`, is going
against the grain of the codebase.

## Gates

Other gates that matter, and that a change should not break:

```sh
gofmt -l .                 # must print nothing
go vet ./...
go test -count=1 ./...     # -count=1 defeats the test cache
docker compose config      # the published compose file must stay valid
```
