package web

// themeCSS is the shared stylesheet for the pages whose markup is assembled in
// Go. It holds the design tokens and the handful of components those pages use,
// so a page does not invent its own palette.
//
// The tokens are the ones shadcn/ui popularised: a near-black foreground on a
// white background, one grey line colour, a small radius, and muted secondary
// text. They are plain CSS custom properties rather than a generated utility
// sheet, which keeps the deploy a single static binary with no build step.
//
// The state badges and the disclosure rows carry the whole page; the rest is
// spacing. A page that grows a new component should add it here rather than
// inline, so the pages keep looking like one application.
const themeCSS = `
* { box-sizing: border-box; }
:root {
  --font: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto,
    "Helvetica Neue", Arial, sans-serif;
  /* One type ramp, shared with the pages the server renders, so no rule has to
     invent its own size. */
  --text-xs: .8rem; /* hints, notes, small labels */
  --text-sm: .875rem; /* labels, buttons, secondary text */
  --text-md: .9375rem; /* body copy */
  --background: #ffffff;
  --foreground: #0a0a0a;
  --card: #ffffff;
  --muted: #f5f5f5;
  --muted-foreground: #737373;
  --border: #e5e5e5;
  --primary: #171717;
  --primary-foreground: #fafafa;
  --accent: #f5f5f5;
  --destructive: #b91c1c;
  --destructive-soft: #fef2f2;
  --ok: #15803d;
  --ok-soft: #f0fdf4;
  --warn: #b45309;
  --warn-soft: #fffbeb;
  --radius: 10px;
  --ring: rgba(10, 10, 10, .08);
}
body {
  margin: 0; background: var(--background); color: var(--foreground);
  font-family: var(--font);
  font-size: var(--text-md); line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
main { max-width: 66rem; margin: 0 auto; padding: 1.5rem 1rem 4rem; }
a { color: inherit; }
h1 { font-size: 1.25rem; font-weight: 600; letter-spacing: -.02em; margin: 0; }

.top { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 1.5rem; }
.brand { display: inline-flex; align-items: center; gap: .5rem; font-weight: 600; font-size: 1rem; letter-spacing: -.015em; text-decoration: none; }
.brand img { width: 1.5rem; height: 1.5rem; display: block; border-radius: 6px; }
.top nav a { color: var(--muted-foreground); font-size: var(--text-xs); text-decoration: none; }
.top nav a:hover { color: var(--foreground); }

.head { display: flex; align-items: baseline; justify-content: space-between; gap: 1rem; flex-wrap: wrap; margin-bottom: .9rem; }
.lede { color: var(--muted-foreground); font-size: var(--text-sm); margin: 0; }
.lede .warn { color: var(--warn); }

.btn {
  display: inline-flex; align-items: center; gap: .3rem;
  font: inherit; font-size: var(--text-sm); font-weight: 500; line-height: 1;
  padding: .42rem .7rem; border-radius: 8px;
  border: 1px solid var(--border); background: var(--card); color: var(--foreground);
  cursor: pointer; white-space: nowrap;
}
.btn:hover { background: var(--accent); }
.btn:focus-visible { outline: none; box-shadow: 0 0 0 3px var(--ring); }
.btn.primary { background: var(--primary); border-color: var(--primary); color: var(--primary-foreground); }
.btn.primary:hover { opacity: .9; }
.btn.danger { color: var(--destructive); }
.btn.danger:hover { background: var(--destructive-soft); border-color: #fecaca; }
.btn.icon { padding: .28rem .34rem; color: var(--muted-foreground); font-size: var(--text-xs); }
.btn.icon:hover { color: var(--foreground); }
.btn.icon.copied { color: var(--ok); border-color: #bbf7d0; }
.btn:disabled { opacity: .5; cursor: not-allowed; }
.btn:disabled:hover { background: var(--card); }

.toolbar {
  /* Sticks to the window while the list scrolls, so the bulk action is still
     on screen when a row far down the page is ticked. */
  position: sticky; top: 0; z-index: 2;
  display: flex; align-items: center; justify-content: space-between; gap: .75rem; flex-wrap: wrap;
  padding: .55rem 0; margin-bottom: .35rem; background: var(--background);
}
.bulk { display: flex; align-items: center; gap: .4rem; margin: 0; }
.bulk .count { color: var(--muted-foreground); font-size: var(--text-xs); }
.bulk.has .count { color: var(--foreground); font-weight: 600; }
select {
  font: inherit; font-size: var(--text-sm); padding: .4rem 1.5rem .4rem .55rem;
  border: 1px solid var(--border); border-radius: 8px;
  background: var(--card); color: var(--foreground);
}
select:focus-visible { outline: none; box-shadow: 0 0 0 3px var(--ring); }

.table-wrap { border: 1px solid var(--border); border-radius: var(--radius); overflow-x: auto; background: var(--card); }
table { width: 100%; min-width: 58rem; border-collapse: collapse; table-layout: fixed; }
th, td { text-align: left; padding: .5rem .7rem; vertical-align: middle; border-bottom: 1px solid var(--border); }
thead th { font-size: var(--text-xs); font-weight: 500; text-transform: uppercase; letter-spacing: .05em; color: var(--muted-foreground); background: #fafafa; }
thead a { display: inline-flex; align-items: center; gap: .25rem; color: inherit; text-decoration: none; }
thead a:hover { color: var(--foreground); }
tbody tr:last-child > td { border-bottom: 0; }

th.check, td.check { width: 2.4rem; }
th.items, td.items { width: 4.5rem; text-align: right; font-variant-numeric: tabular-nums; }
th.built, td.built { width: 7rem; color: var(--muted-foreground); white-space: nowrap; }
th.state, td.state { width: 7rem; }
th.action, td.action { width: 11.5rem; }
th.source, td.source { width: 12rem; }
td.source { color: var(--muted-foreground); font-size: var(--text-sm); }
td.source a { color: inherit; text-decoration: none; }
td.source a:hover { color: var(--foreground); text-decoration: underline; }

tr.child td { font-size: var(--text-sm); }
tr.child:hover td { background: #fafafa; }
html.js tr.child { display: none; }
html.js tr.child.open { display: table-row; }
td.feed { min-width: 0; }
td.feed .uri { display: inline-block; max-width: calc(100% - 2rem); overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; vertical-align: middle; font-size: var(--text-sm); text-decoration: none; }
td.feed .uri:hover { text-decoration: underline; }
td.feed .copy { margin-left: .25rem; vertical-align: middle; }

tr.group td { background: #fafafa; padding: .45rem .7rem; }
.chev { border: 0; background: transparent; cursor: pointer; padding: 0 .15rem 0 0;
  font: inherit; font-size: var(--text-xs); color: var(--muted-foreground); width: 1rem; }
.chev:hover { color: var(--foreground); }
.srclink { font-size: var(--text-md); font-weight: 500; word-break: break-all; }
.pill { margin-left: .45rem; font-size: var(--text-xs); color: var(--muted-foreground); white-space: nowrap; }

.badge { display: inline-block; font-size: var(--text-xs); font-weight: 500; padding: .12rem .45rem;
  border-radius: 999px; border: 1px solid var(--border); background: var(--muted); color: var(--muted-foreground); }
.badge.ok { background: var(--ok-soft); border-color: #bbf7d0; color: var(--ok); }
.badge.warn { background: var(--warn-soft); border-color: #fde68a; color: var(--warn); }
.badge.err { background: var(--destructive-soft); border-color: #fecaca; color: var(--destructive); }

.actions { white-space: nowrap; }
.actions form { display: inline; }
.actions .btn { margin-right: .3rem; }
.actions .btn:last-child { margin-right: 0; }
.err { color: var(--destructive); font-size: var(--text-xs); margin-top: .15rem; word-break: break-word; }
.empty { padding: 2.5rem 1rem; text-align: center; color: var(--muted-foreground); font-size: var(--text-md); }

.note { color: var(--muted-foreground); font-size: var(--text-xs); line-height: 1.5; margin: .8rem 0 0; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  background: var(--muted); padding: .08rem .3rem; border-radius: 4px; font-size: .9em; }

/* The sign-in page is the only page here that is not a list, so it is the only
   one with a card and a text field. Both live in this file rather than inline,
   for the same reason as everything else: a second page should not invent a
   second palette. */
.login { max-width: 24rem; margin: 3rem auto 0; }
.login .card { border: 1px solid var(--border); border-radius: var(--radius);
  background: var(--card); padding: 1.3rem; }
.login h1 { margin-bottom: .35rem; }
.login .lede { margin-bottom: 1rem; }
.login label { display: block; font-size: var(--text-sm); font-weight: 500; margin-bottom: .3rem; }
.login input { font: inherit; font-size: var(--text-md); width: 100%; padding: .45rem .55rem;
  border: 1px solid var(--border); border-radius: 8px; background: var(--card); color: var(--foreground); }
.login input:focus-visible { outline: none; border-color: var(--foreground); box-shadow: 0 0 0 3px var(--ring); }
.login .btn { width: 100%; justify-content: center; margin-top: .9rem; }
.login .err { margin: 0 0 .8rem; }
`

// The nav a page shows beside the wordmark. Every page builds its header
// through brandHeader, so the logo and the wordmark cannot drift into two sizes
// or two spellings from one page to the next.
const (
	navNewFeed = `<nav><a href="/">New feed</a></nav>`
	navManage  = `<nav><a href="/feeds">Manage feeds</a></nav>`
	navBoth    = `<nav><a href="/">New feed</a><a href="/feeds">Manage feeds</a></nav>`
)

// brandHeader is the wordmark, the link back to the builder, and the nav for the
// page it is on.
func brandHeader(nav string) string {
	return `<header class="top"><a class="brand" href="/">` +
		`<img src="/feedme.png" alt="" width="26" height="26">feedme</a>` +
		nav + `</header>`
}
