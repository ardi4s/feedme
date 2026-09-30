# Managing Feeds

A feed is built when someone asks for it. There is no scheduler and no
subscription to keep: a reader's request rebuilds a feed whose cached body has
expired, and the cache lifetime (`ttl`, fifteen minutes by default) is the only
thing that paces rebuilds. A link built once is never lost, because it lives in
the build history rather than in a subscription.

## `/feeds` Page

Lists every feed the server has built, grouped by the site it came from:

### Columns

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

- **Status** — a merged view of cache state and build health:
  - `failed` — build failed (error shown in tooltip)
  - `failing (3/10)` — 3 consecutive failures of 10 recent builds; tooltip shows streak, last success, avg build time, success ratio
  - `never ok` — feed has history but never succeeded
  - `stale` — cache expired, will rebuild on next request
  - `fresh` — cache valid; tooltip shows last success, avg build time, success ratio
  - `unknown` — no build history, cache state unknown

  Hover the badge for detailed diagnostics (failure streak, last success, avg build time, success ratio).

- **Action** — **Refresh now** rebuilds through the same path a reader takes,
  and **Forget** removes the feed from the list and drops its build history.

### Toolbar

The toolbar also has **Export OPML**, which serves every listed feed as one
OPML 2.0 file — one `outline` per feed, with the `/extract` URL as its
`xmlUrl` and the page it was built from as its `htmlUrl`. A reader imports the
file in one step and nothing about feed URLs changes. The download is an
attachment named for the day, and it lists the same feeds the page shows,
because both are read from the build history.

The header sorts the list (`?sort=source`, `feed`, `items`, `built`, `status`,
with `?dir=asc`/`desc`) and offers a select-all checkbox. Checking rows
and choosing **Refresh** or **Delete** in the toolbar applies the action to all
of them in one request; the toolbar sticks to the top of the window while the
list scrolls, and its Apply button only enables once something is checked.

Sorting and the group disclosure work without JavaScript: the script on the
page only adds the select-all, the enable-on-select and the copy conveniences,
and the page falls back to showing every group expanded with a working Apply.

## Authentication

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
