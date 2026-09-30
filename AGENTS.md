# AGENTS.md

Working notes for an agent (or a person) changing this repository. Everything
here describes the checkout as it actually is, including the parts that are
deliberately local to this machine. Read it before editing anything.

---

## 1. What this is

`feedme` turns a web page that lists things into a feed a reader can subscribe
to. It serves RSS 2.0, Atom 1.0, or JSON Feed 1.1 from one URL per feed, and it
is a single static Go binary with no build step at deploy time.

The idea is that the source site needs no cooperation: the server fetches the
listing page, works out which elements are the items, and renders them as a
feed. Where a listing is built by JavaScript it can optionally ask a headless
browser for the rendered HTML.

State lives in one SQLite database: an HTTP cache, a rendered-feed cache, the
per-item bodies used for full text, and the build history that the management
page lists from. Nothing else is persisted, so the binary itself is disposable.

Module path is `feedme`. `go.mod` says `go 1.25.0` with `toolchain go1.25.11`.

---

## 2. Commands

The Makefile exports `GOTOOLCHAIN ?= go1.25.11`, so `make` targets pick the
right toolchain on their own. Running `go` directly outside `make` needs
`export GOTOOLCHAIN=go1.25.11` unless the system Go already matches.

| Command | What it does |
| --- | --- |
| `make all` | `vet` + `test` + `build`. The gate to run before committing. |
| `make build` | Builds `bin/feedme` with `-ldflags "-s -w -X main.version=$(VERSION)"`. |
| `make test` | `go test ./...` |
| `make vet` | `go vet ./...` |
| `make fmt` | `gofmt -l -w .` |
| `make tidy` | `go mod tidy` |
| `make install` | `go install` with the version stamped in |
| `make run` | Builds, then runs `bin/feedme` |
| `make clean` | Removes `bin/` |

`VERSION` is read from the git tag with `git describe`, so a local `make build`
reports the tag it came from: `0.2.0` at the tag, `0.2.0-3-g9e82db4` three
commits later, `-dirty` with uncommitted changes, and `docker` where there is no
git at all. Releases ignore it; `release.yml` stamps the tag itself.

Other gates that matter, and that a change should not break:

```sh
gofmt -l .                 # must print nothing
go vet ./...
go test -count=1 ./...     # -count=1 defeats the test cache
docker compose config      # the published compose file must stay valid
```

Run the server locally:

```sh
go run ./cmd/feedme serve -addr :8080 -db /tmp/feedme.db -site-dir configs/sites
```

Diagnose why a URL produces nothing:

```sh
go run ./cmd/feedme probe https://example.com/news
go run ./cmd/feedme https://example.com/news     # shorthand for the above
```

Two subcommands exist: `serve` and `probe`. `feedme <url>` is `feedme probe
<url>`. `feedme version` and `feedme help` are also there.

---

## 3. Repository layout

```
cmd/feedme/            the CLI: dispatch, wiring, and the adapters
internal/              everything that does not need a main function
configs/sites/         per-host site overrides, plus two bundled ones
docs/brand/            the images the README links to
tools/                 Python helpers for brand art and screenshots
.github/workflows/     ci.yml and release.yml
```

### `cmd/feedme`

| File | Role |
| --- | --- |
| `main.go` | Subcommand table, help and version output, the CLI banner. |
| `serve.go` | Flags, config loading, wiring every component, running the HTTP server. This is the only place that knows about all the pieces at once. |
| `probe.go` | Fetches one URL and reports what extraction found. The way to answer "why is this feed empty". |
| `admin.go` | `storeAdmin`: adapts the SQLite store to `web.FeedAdmin`. |
| `feedcache.go` | `storeFeedCache`: adapts the store to the interface the web layer wants for cached feeds. |

### `internal`

| Package | Role |
| --- | --- |
| `web` | HTTP only: routing, status codes, cache headers, the Go-assembled pages. |
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
| `urlx` | URL helpers, including the registrable-domain logic the page groups by. |
| `store` | SQLite: the caches and the build history. |
| `config` | Global settings and per-host site overrides. |

The split is deliberate: **HTTP belongs in `web`, feed content belongs in
`pipeline` and its helpers, and request parsing belongs in `feedurl`.** A change
that puts extraction logic in `web`, or status codes in `pipeline`, is going
against the grain of the codebase.

---

## 4. How a request becomes a feed

For `GET /extract?url=…`:

1. `web` maps the request onto a `feedurl.Spec`.
2. `pipeline` fetches the page through `fetch` (consulting the HTTP cache), or
   asks `render` for the rendered HTML when `render_js=1`.
3. The fetched body is asked what it is. If it parses as a feed, `feedread`
   supplies the items and the listing path is skipped — `url` takes a feed
   address as readily as a page, because that is where a reader copies one. A
   feed with no entries reports that it is not one, so it fails as the empty page
   it looks like.
4. Otherwise `listpage` picks the item container and the fields; `extract` fetches
   article bodies when `fulltext=1`.
5. `filter` narrows and de-duplicates.
6. `feed` renders the requested format.
7. `web` writes the body with `ETag`, `Last-Modified`, `Cache-Control`, and the
   `X-Feedme-*` headers, and records the build through the `BuildRecorder`.

The render is cached in SQLite, so a reader polling a slow source does not
re-fetch it. The build record is what makes a feed appear on `/feeds`.

---

## 5. HTTP surface

Routes are handled by the switch in `Server.ServeHTTP` (`internal/web/server.go`).

| Path | Purpose | Gated? |
| --- | --- | --- |
| `/` | The builder form: Automatic, Simple, Advanced, Merge. | no |
| `/extract` | Build and return a feed. What readers subscribe to. | no |
| `/check` | Describe what a feed URL would produce, without fetching the source. | no |
| `/preview` | Build the feed and render its items as HTML. | no |
| `/feeds` | Management page: every feed that has been built. | **yes** |
| `/feeds/opml` | The same feed list as OPML 2.0, for import into a reader. | **yes** |
| `/login` | The sign-in form for `/feeds`. | no |
| `/healthz` | Liveness. | no |
| `/feedme.png`, `/favicon.png`, `/favicon.ico` | Embedded brand images. | no |

### Auth model

This is the part most likely to be broken by a careless change. Pin all of it:

- **Only `/feeds` and `/feeds/opml` are gated**, and only when
  `FEEDME_ADMIN_TOKEN` is set. Feed URLs must never be blocked, because readers
  are the point of the service. The export is gated because it shows everything
  the gate hides.
- **`/feeds/opml` is a path below `/feeds`, not a sibling spelling.** The
  session cookie's `Path=/feeds` only travels to paths below `/feeds`, so
  `/feeds.opml` would not carry the cookie and a signed-in operator clicking
  the export link would be sent back to the sign-in form.
- **With no token, there is no login at all** and `/feeds` is open, exactly as
  it was before auth existed. A new user must see no difference.
- **A browser is never challenged.** An unauthenticated request with
  `Accept: text/html` gets `303 → /login` and **no** `WWW-Authenticate` header.
  Chromium renders its own error page instead of the Basic prompt often enough
  that relying on the prompt locks operators out; that is why the form exists.
- **Everything else still gets the challenge**: no HTML in `Accept` means
  `401` plus `WWW-Authenticate: Basic realm="feedme", charset="UTF-8"`. `curl`
  and scripts depend on this and it must keep working.
- **Three credentials are accepted**, in `credentialOK`
  (`internal/web/login.go`): the session cookie, `Authorization: Bearer <token>`,
  and a Basic password (the username is ignored). Comparisons are constant time
  via `tokenEqual`.
- **`POST /login`** verifies the token, sets the cookie, and `303`s to `/feeds`.
  A wrong token re-renders the form with `200` and a message **on purpose** — a
  `401` is what a browser turns into an error page. Do not "correct" this to 401.
- The cookie is `feedme_session`: HMAC-SHA256 of the token, not the token
  itself, so rotating the token ends the old sessions. `Path=/feeds`,
  `HttpOnly`, `SameSite=Lax` (keeps cross-site form posts out of the session,
  while still letting a link from elsewhere open the page), `Secure` when
  `requestScheme` reports https, and session-scoped — closing the browser ends
  it. There is no logout route.

---

## 6. Web UI conventions

There are two stylesheets, and mixing them is a mistake:

- **`themeCSS` in `internal/web/ui.go`** is the design system: tokens (colour,
  radius, one type ramp), plus the components the Go-assembled pages use. Pages
  built in Go — `/feeds` and `/login` — use this and share `brandHeader`.
  **A new component goes in `ui.go`, not inline on one page.** The file says so
  itself; honour it.
- **`indexHTMLHead` in `internal/web/server.go`** is a large raw string with its
  own older, Helvetica-based style block. `/` and the `/check` and `/preview`
  pages use it. Do not port it, and do not add `themeCSS` to it.

Other pieces:

- `iconLinksHTML` (`assets.go`) points a head at the embedded favicons.
- Brand images are embedded with `go:embed`, so the runtime image needs no
  static directory.
- `tools/brand.py` generates `social-preview.png` and the favicons.
- `tools/screenshot.py` regenerates `docs/brand/home.png` and `hero.png`.
- `docs/brand/hero.png` shows a real feed list from a real deployment, so
  re-shooting it changes what the README shows. **Do not overwrite it without
  asking.** The same goes for anything that depends on the deployment's contents.

---

## 7. Configuration

`serve` flags (see `runServe` in `cmd/feedme/serve.go`):

| Flag | Default | Meaning |
| --- | --- | --- |
| `-config` | | Path to a YAML config file. |
| `-site-dir` | | Directory of per-host site configs; overrides the config file. |
| `-addr` | `:8080` | Listen address. |
| `-db` | | SQLite path. |
| `-user-agent` | | Overrides the User-Agent sent to source sites. |
| `-timeout` | | Per-request timeout, e.g. `20s`. |
| `-fulltext-parallel` | `4` | How many article bodies to fetch at once. |
| `-render-url` | | Base URL of a headless browser (browserless); enables `render_js`. |
| `-admin-token` | `$FEEDME_ADMIN_TOKEN` | Password for `/feeds`. The flag wins over the env var. |
| `-no-robots` | off | Ignore `robots.txt`. |
| `-allow-private` | off | Permit requests to private IP ranges (LAN testing). |
| `-log-level` | `info` | `debug`, `info`, `warn`, or `error`. |
| `-quiet` | off | Warnings and errors only. |

The only environment variable read anywhere is `FEEDME_ADMIN_TOKEN`. The
environment is preferred over the flag for a secret, because a flag is visible
in the process list.

Per-host site configs exist as an escape hatch, not as a requirement: the
project is meant to work on sites it has never seen. Do not add
`configs/sites/<host>.yml` to fix a general problem.

The exception is a host whose feed path its own `robots.txt` forbids to every
client, where the endpoint is published for readers and there is no markup to
select. Two such configs are bundled, `news.google.com.yml` and
`www.google.com.yml`, each `respect_robots: false` and each scoped to one host.
A third is a decision worth raising first: the file ships in the image, so it
changes what every other install reads, and it can be the opening of an argument
that all of `robots.txt` is negotiable.

---

## 8. Storage

One SQLite database holds, among others, these tables: `http_cache`,
`feed_cache`, `items`, `feed_items`, `feed_builds`, `sources`, `seen_urls`.

`/feeds` is built from **`feed_builds`**: the build history, not a separate
registry. One listed feed is one distinct `feed_key`, and a feed appears the
moment it is first built and stays after its cached body expires. The only way
to remove it is the **Forget** action, which deletes that key's build history.

That has a consequence worth knowing before touching the page or store: a failed
build is still a build. Testing `/extract` against the live instance adds entries
to the operator's feed list. Clean up with Forget afterwards if you do that.

---

## 9. Testing conventions

- Tests live in the same package as the code they test (`package web`, not
  `web_test`), so they can reach unexported functions.
- Use `net/http/httptest`; build requests with `httptest.NewRequest`.
- Prefer table-driven cases with a `name` field.
- `internal/web/manage_auth_test.go` and `login_test.go` show the idiom for the
  gate: they construct `New(Options{ManageToken: …})` with **`Admin` left nil**,
  so a request that clears the gate answers `404` rather than rendering a page.
  `404` therefore means "the gate let it through" and any `401`/`303` means it
  refused. Keep that distinction when adding cases.
- Test the boundary, not the implementation: which credential is accepted, what
  status an unauthenticated caller sees, which cookie attributes are set.

---

## 10. Writing conventions

- Comments explain **why**, not what. The existing prose is calm and specific;
  a comment that restates the code should be deleted, and one that explains a
  decision is worth keeping.
- Comments and docs are in **English**.
- No superlatives, and no comparisons against other projects. `README.md` uses
  a **Limitations** section instead of a feature-comparison table. Keep that.
  The one exception is the `## Feed readers` list under Endpoints: naming
  readers is not a comparison of feed generators, but the list stays factual —
  platform, how it runs, licence — with no ranking, star counts, or promotion.
- `CHANGELOG.md` is newest first, versions are `MAJOR.MINOR.PATCH`, and an entry
  says what changed for a user — not which files moved.
- The README describes what the code does. If behaviour changes and the README
  is not updated, the change is not finished.

---

## 11. Release

1. Add the `CHANGELOG.md` entry, newest first.
2. Commit on `main` and push.
3. Create an **annotated** tag `vX.Y.Z` and push it:
   ```sh
   git tag -a v0.2.0 -m "feedme 0.2.0" -m "…one or two lines…"
   git push origin v0.2.0
   ```
4. `release.yml` builds `linux/amd64` and `linux/arm64` tarballs, writes
   `checksums.txt`, attaches them to a GitHub release, and pushes the container
   image to GHCR.
5. GHCR tags are the version **without** the leading `v`, plus the minor and
   `latest`: `0.2.0`, `0.2`, `v0.2.0`, `latest`.
6. Point the deployment at the new version tag.

The public `docker-compose.yml` pins one released version and pulls it; it never
builds. Settings that are true of one machine (a build stanza, a shared network,
a fixed uid) belong in `docker-compose.override.yml`, which is gitignored.

`ci.yml` runs on pushes to `main`; both workflows must be green before a deploy.

---

## 12. This host (local only — never publish these values)

- Deployment compose: `/home/ubuntu/feedme-image/docker-compose.yml`, beside a
  `.env` (mode 600) holding `FEEDME_ADMIN_TOKEN`.
- The database is `/home/ubuntu/feedme/data/feedme.db`, i.e. still inside the
  checkout it was built from. `git clean -fdx` would delete it, and `data/` is
  gitignored. Moving it under `feedme-image/` is an open chore.
- The service joins the external network `innercircle` under the alias
  `feedme`, and nginx-proxy-manager proxies `rss.ardi4s.qzz.io` to
  `http://feedme:8080`.
- The host's user is uid/gid 1001, hence `user: "1001:1001"` in the compose.
- `render_js` answers `501` there: the `chromium` service (profile `browser`)
  is not in that compose yet.

None of this belongs in a tracked file. No `innercircle`, no `qzz.io`, no
`/home/ubuntu` path, no `.env`, no database.

---

## 13. Boundaries

- **Never commit personal data.** Before committing, check that no tracked file
  mentions the deployment's hostname, its private network, or an absolute home
  path. `git grep` for those three is enough.
- **Never gate an endpoint other than `/feeds` and its own subpaths.** Readers
  must not be blocked, and `/extract` staying open is what makes the gate on
  `/feeds` meaningful.
- **Never make the empty-token case require a login**, and never drop Basic or
  Bearer support — scripts and `curl` rely on both.
- **Never overwrite `docs/brand/hero.png`** or any screenshot of the live
  deployment without asking first.
- **Keep `docker-compose.yml` the generic default** (pull-only, one version tag,
  no host-specific settings).
- **Do not add per-site configs** for problems that should be solved generally.
  The two bundled `robots.txt` overrides are the exception, and the reasoning for
  them is in §7.
- `.gitignore` already covers `bin/`, `data/`, `*.db*`, `.env`, and
  `docker-compose.override.yml`. Add to it rather than force-adding.

---

## 14. Known drift — do not "fix" unasked

These are known and deliberate or accepted. Changing them changes behaviour or
published text, so raise it before touching them.

- The CLI help banner (`cmd/feedme/main.go`) still says *full-text feed
  generator for sites without one*, while the Makefile header and README say
  *RSS feed creator*.
- The default User-Agent is `feedme/0.1 (+self-hosted full-text feed generator)`
  in both `internal/config/config.go` and `internal/fetch/client.go`.
- Six files cite **FiveFilter** as technical provenance — `internal/urlx/urlx.go`,
  `internal/config/config.go`, `internal/config/config_test.go`,
  `internal/feedurl/feedurl.go`, `internal/feedurl/feedurl_test.go`, and
  `internal/filter/filter.go`. That is intentional attribution of the design,
  not a leftover.

---

## 15. Waitlist — future ideas (brainstormed, not committed)

### Medium (1–2 weeks)
- **OPML import**: User pastes OPML → server creates all feeds at once.
- **Background rebuilder**: Periodic worker rebuilds stale/failed feeds so readers always get fresh content.
- **Webhook push**: `webhook_url` param → POST new items to Discord/Slack/Telegram in real time.
- **Custom output templates**: User defines RSS/Atom/JSON template for branding, extra fields, custom date format.

### Big Features (month+)
- **Multi-user / tenancy**: User accounts, API keys per user, quota, team sharing.
- **Full-text search**: SQLite FTS5 or Meilisearch → search across all feed items via `/search?q=…`.
- **Feed collections/bundles**: "Tech news" = merge 10 feeds → 1 URL. Tag/category based.
- **Plugin system**: Custom extractors (YouTube transcript, Twitter thread), custom filters (ML spam), custom renderers.
- **Analytics/telemetry**: Which items clicked, read time, popular feeds (opt-in, privacy-first).
- **Federation/ActivityPub**: Feed becomes an Actor → followers subscribe via Mastodon/Fediverse.

### Design Considerations
- **SQLite vs Postgres**: Current SQLite (single binary, simple). Horizontal scale needs Postgres + shared cache.
- **Single binary vs microservices**: Background worker, webhook delivery, search → separate processes?
- **Auth model**: Single token is simple. Multi-user needs DB migration, password reset, email, etc.
- **Extensibility**: Go plugins? WASM? Lua/JS scripting? RPC to separate service?

### Done (moved from waitlist)
- **Feed health dashboard**: `/feeds` now has a single "Status" column merging cache state (fresh/stale/failed) with build health (failure streak, avg build time, last success). Sortable, with tooltips.
- **Auto-discovery**: `url=` now detects `<link rel="alternate" type="application/rss+xml">` in HTML and auto-fetches the feed.
- **gzip/brotli compression** on `/extract` (Accept-Encoding: gzip/br).
- **Conditional requests**: If-None-Match (ETag) and If-Modified-Since return 304.
- **Export formats**: `/feeds/export?format=opml|csv|json` (OPML/CSV/JSON) with HEAD support.
- **Feed column widened** to 420px in management table.
- **OPML import** not yet done.

---

## 16. Pre-push Checklist

Before pushing to GitHub (especially before tagging a release), verify:

### Code Quality
- [ ] `gofmt -l .` → no output (run `gofmt -w .` if needed)
- [ ] `go vet ./...` → no warnings
- [ ] `go test -count=1 ./...` → all packages pass
- [ ] `make all` → builds successfully

### Security
- [ ] No secrets/tokens in code (check `git diff --cached`)
- [ ] No `.env`, `*.key`, `*.pem` tracked (`.gitignore` covers this)
- [ ] No hardcoded credentials in code

### Dependencies
- [ ] `go mod tidy` → no unnecessary deps
- [ ] `go mod verify` → checksums match
- [ ] Check for vulnerable deps: `govulncheck ./...` (if available)

### Documentation
- [ ] `README.md` updated for new features
- [ ] `CHANGELOG.md` updated with release notes
- [ ] `docs/` files updated for changed behavior
- [ ] `AGENTS.md` waitlist updated

### Release Prep (if tagging)
- [ ] `CHANGELOG.md` updated with release date
- [ ] Version bumped in `CHANGELOG.md` (remove "— unreleased")
- [ ] `git tag -a vX.Y.Z -m "feedme X.Y.Z"`
- [ ] `git push origin main --tags`

### CI/CD
- [ ] GitHub Actions pass on `main` branch
- [ ] Release workflow triggers on tag push
- [ ] Docker image builds for `linux/amd64,linux/arm64`
- [ ] Binaries built for `linux/amd64,linux/arm64`

### Post-release
- [ ] Docker image available at `ghcr.io/ardi4s/feedme:X.Y.Z`
- [ ] Binaries attached to GitHub Release
- [ ] Release notes published on GitHub
