package web

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"feedme/internal/feed"
	"feedme/internal/feedurl"
)

// BuiltFeed is a feed the server knows about, as shown on the management page.
//
// A feed is listed from the moment it is first built, so the entry outlives the
// cached body it was stored in. Cached and Stale say what is backing the feed
// right now; LastError says whether the newest build even worked.
type BuiltFeed struct {
	Key       string
	SourceURL string
	Format    string
	ItemCount int
	Detector  string
	// FetchedAt is when the feed was last built.
	FetchedAt time.Time
	// ExpiresAt is when a stored body stops being usable without a rebuild.
	ExpiresAt time.Time
	// Cached reports whether a stored body currently backs the feed.
	Cached bool
	// Stale reports a stored body that has outlived its expiry.
	Stale bool
	// LastError explains why the most recent build failed, empty when it
	// succeeded.
	LastError string

	// Health fields (computed from build history)
	FailureStreak int
	TotalRecent   int
	LastSuccess   time.Time
	AvgBuildMs    int
}

// State is the one word the page shows for a feed: what it is right now.
func (f BuiltFeed) State() (label, class string) {
	switch {
	case f.LastError != "":
		return "failed", "err"
	case f.Cached && !f.Stale:
		return "fresh", "ok"
	case f.Cached:
		return "stale", "warn"
	default:
		return "not cached", "muted"
	}
}

// CombinedStatus returns a combined status label, CSS class, and tooltip
// that merges cache state (State) with build health (HealthState).
func (f BuiltFeed) CombinedStatus() (label, class, tooltip string) {
	// Priority: build failure > cache state > health > cache fresh
	switch {
	case f.LastError != "":
		// Build failed - highest priority
		return "failed", "err", f.LastError
	case f.FailureStreak > 0:
		// Consecutive failures
		return fmt.Sprintf("failing (%d/%d)", f.FailureStreak, f.TotalRecent), "err",
			fmt.Sprintf("%d consecutive failures; last success %s; avg build %s; %d/%d recent ok",
				f.FailureStreak,
				f.LastSuccessTime(),
				f.AvgBuildTime(),
				f.TotalRecent-f.FailureStreak, f.TotalRecent)
	case f.TotalRecent == 0:
		// No build history - fall back to cache state
		label, class := f.State()
		return label, class, "No build history; cache: " + label
	case f.LastSuccess.IsZero():
		// Never succeeded (but has history)
		return "never ok", "warn",
			fmt.Sprintf("Never succeeded; %s", f.HealthStateTooltip())
	case f.Stale:
		// Cache expired
		return "stale", "warn", "Cache expired, will rebuild on next request"
	case f.Cached:
		// Fresh cache with health info
		return "fresh", "ok", f.HealthStateTooltip()
	default:
		return "unknown", "muted", "No build history"
	}
}

// HealthState returns a health label and CSS class for the feed.

// HealthState returns a health label and CSS class for the feed.
func (f BuiltFeed) HealthState() (label, class string) {
	if f.TotalRecent == 0 {
		return "unknown", "muted"
	}
	if f.FailureStreak > 0 {
		return fmt.Sprintf("failing (%d/%d)", f.FailureStreak, f.TotalRecent), "err"
	}
	if f.LastSuccess.IsZero() {
		return "never ok", "warn"
	}
	return fmt.Sprintf("healthy (%d/%d)", f.TotalRecent-f.FailureStreak, f.TotalRecent), "ok"
}

// AvgBuildTime returns a human-readable average build time.
func (f BuiltFeed) AvgBuildTime() string {
	if f.AvgBuildMs <= 0 {
		return "—"
	}
	if f.AvgBuildMs < 1000 {
		return fmt.Sprintf("%dms", f.AvgBuildMs)
	}
	return fmt.Sprintf("%.1fs", float64(f.AvgBuildMs)/1000)
}

// LastSuccessTime returns a human-readable last success time.
func (f BuiltFeed) LastSuccessTime() string {
	if f.LastSuccess.IsZero() {
		return "never"
	}
	return agoTime(f.LastSuccess, time.Now())
}

// HealthStateTooltip returns a detailed tooltip for the health badge.
func (f BuiltFeed) HealthStateTooltip() string {
	if f.TotalRecent == 0 {
		return "No build history available"
	}
	var parts []string
	if f.FailureStreak > 0 {
		parts = append(parts, fmt.Sprintf("%d consecutive failures", f.FailureStreak))
	} else if f.LastSuccess.IsZero() {
		parts = append(parts, "Never succeeded")
	} else {
		parts = append(parts, fmt.Sprintf("Last success %s", agoTime(f.LastSuccess, time.Now())))
	}
	if f.AvgBuildMs > 0 {
		parts = append(parts, fmt.Sprintf("avg build %s", f.AvgBuildTime()))
	}
	parts = append(parts, fmt.Sprintf("%d/%d recent builds ok", f.TotalRecent-f.FailureStreak, f.TotalRecent))
	return strings.Join(parts, "; ")
}

// FeedAdmin persists what the management page shows and changes.
//
// It is an interface so internal/web keeps no dependency on the storage
// package. Nil disables the page entirely.
type FeedAdmin interface {
	BuiltFeeds(ctx context.Context) ([]BuiltFeed, error)
	// Forget drops a feed entirely: its stored body and its build history, so
	// it stops being listed.
	Forget(ctx context.Context, key string) error
}

// Refresh rebuilds one feed by its canonical key, stores the result, and
// records the attempt. It is used by the management page's "refresh now", so a
// manual refresh goes through exactly the same path as a reader's request.
func (s *Server) Refresh(ctx context.Context, key string) error {
	u, err := url.Parse(key)
	if err != nil {
		return fmt.Errorf("feed key %q: %w", key, err)
	}
	spec, err := feedurl.Parse(u.Query())
	if err != nil {
		return err
	}
	spec.Feed.SelfLink = key
	spec.Feed.SelfType = contentTypeOf(spec.Format)

	res, err := s.pipe.Run(ctx, spec)
	if err != nil {
		s.record(ctx, key, spec.URL, false, 0, nil, err)
		return err
	}
	body, err := feed.Bytes(res.Feed, res.Format)
	if err != nil {
		return err
	}
	resp := feedResponse{
		body:         body,
		etag:         etagOf(body),
		contentType:  contentTypeOf(res.Format),
		itemCount:    len(res.Items),
		lastModified: feedModified(res, s.nowFn()),
		spec:         &spec,
		result:       &res,
	}
	s.record(ctx, key, spec.URL, true, len(res.Items), res.Detection, nil)
	if s.cache != nil {
		if err := s.cache.Put(ctx, key, asCacheEntry(resp), cachedItems(res.Items), s.lifetime(spec)); err != nil {
			return err
		}
	}
	return nil
}

// handleFeeds serves the management page and its form posts.
func (s *Server) handleFeeds(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderFeeds(w, r)
	case http.MethodPost:
		s.mutateFeeds(w, r)
	default:
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// mutateFeeds applies one management action and redirects back to the page, so
// a browser refresh does not repeat it.
//
// Two shapes arrive here. A row's buttons submit their own small form with an
// `action` and a `key`; the toolbar submits `bulk` with every checked `keys`.
// The two never arrive together, because a row button belongs to its own form
// and not to the toolbar's.
func (s *Server) mutateFeeds(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "bad form: "+err.Error())
		return
	}
	ctx := r.Context()

	if bulk := strings.TrimSpace(r.FormValue("bulk")); bulk != "" {
		if !knownAction(bulk) {
			s.fail(w, r, http.StatusBadRequest, "unknown action "+strconv.Quote(bulk))
			return
		}
		var keys []string
		for _, key := range r.Form["keys"] {
			if key = strings.TrimSpace(key); key != "" {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			s.fail(w, r, http.StatusBadRequest, "no feeds selected")
			return
		}
		// One failure does not stop the rest: a bulk action over a list is
		// expected to do as much as it can, and the first error is reported
		// after the loop.
		var firstErr error
		for _, key := range keys {
			if err := s.feedAction(ctx, bulk, key); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			s.fail(w, r, http.StatusBadGateway, firstErr.Error())
			return
		}
		http.Redirect(w, r, "/feeds", http.StatusSeeOther)
		return
	}

	action := strings.TrimSpace(r.FormValue("action"))
	key := strings.TrimSpace(r.FormValue("key"))
	if action == "" {
		s.fail(w, r, http.StatusBadRequest, "no action given")
		return
	}
	if !knownAction(action) {
		s.fail(w, r, http.StatusBadRequest, "unknown action "+strconv.Quote(action))
		return
	}
	if err := s.feedAction(ctx, action, key); err != nil {
		s.fail(w, r, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, "/feeds", http.StatusSeeOther)
}

// knownAction keeps the two per-feed actions in one place, so a bulk request
// and a row button cannot drift apart.
func knownAction(action string) bool {
	return action == "forget" || action == "refresh"
}

func (s *Server) feedAction(ctx context.Context, action, key string) error {
	switch action {
	case "forget":
		return s.admin.Forget(ctx, key)
	case "refresh":
		return s.Refresh(ctx, key)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

// sortSpec is the column the page is ordered by.
type sortSpec struct {
	key  string
	desc bool
}

// The orderable columns. Source is the default because the page groups by it,
// so an unsorted view still reads top to bottom.
const (
	sortSource   = "source"
	sortFeed     = "feed"
	sortItems    = "items"
	sortBuilt    = "built"
	sortState    = "state"
	sortHealth   = "health"
	sortStreak   = "streak"
	sortAvgBuild = "avgbuild"
	sortLastOK   = "lastok"
)

func defaultSortDir(key string) string {
	if key == sortBuilt || key == sortItems || key == sortStreak || key == sortAvgBuild {
		return "desc"
	}
	return "asc"
}

func parseSort(r *http.Request) sortSpec {
	q := r.URL.Query()
	key := q.Get("sort")
	switch key {
	case sortSource, sortFeed, sortItems, sortBuilt, sortState,
		sortHealth, sortStreak, sortAvgBuild, sortLastOK:
	default:
		key = sortSource
	}
	desc := defaultSortDir(key) == "desc"
	switch q.Get("dir") {
	case "asc":
		desc = false
	case "desc":
		desc = true
	}
	return sortSpec{key: key, desc: desc}
}

// feedGroup is the feeds built from one site. The page collapses each group,
// because a site an operator has tuned usually has several feeds and showing
// them all at once buries the other sites.
//
// The group is the registrable domain rather than the exact page, so the two
// spellings of a host (kontan.co.id and www.kontan.co.id) and its subdomains
// share one entry. The exact page is still on every row, in the source column,
// because that is what tells one feed of a site apart from the next.
type feedGroup struct {
	Site  string
	Feeds []BuiltFeed
}

// groupBySite keeps the order it is given, so sorting the flat list first is
// what decides both the group order and the order inside each group.
func groupBySite(feeds []BuiltFeed) []feedGroup {
	index := make(map[string]int, len(feeds))
	groups := make([]feedGroup, 0, len(feeds))
	for _, f := range feeds {
		site := siteOf(f.SourceURL)
		i, ok := index[site]
		if !ok {
			i = len(groups)
			index[site] = i
			groups = append(groups, feedGroup{Site: site})
		}
		groups[i].Feeds = append(groups[i].Feeds, f)
	}
	return groups
}

// siteOf reduces a source URL to the site it belongs to: the registrable
// domain, also called the eTLD+1. www.kontan.co.id, nasional.kontan.co.id and
// kontan.co.id are one site, and the public suffix list is what knows that
// co.id is a suffix while id is not — a "last two labels" rule gets that wrong.
//
// A host with no registrable domain (an IP literal, localhost, an internal
// name) is returned as written, so it still groups with itself. Anything that
// cannot be parsed at all is returned whole rather than folded into the
// no-source group, so a feed is never hidden under a label that is not its own.
func siteOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return raw
	}
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return host
	}
	if site, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return site
	}
	return host
}

// distinctPages counts the pages a site's feeds were built from, so a group
// holding several pages says so instead of reading as a single source.
func distinctPages(feeds []BuiltFeed) int {
	seen := make(map[string]struct{}, len(feeds))
	for _, f := range feeds {
		seen[f.SourceURL] = struct{}{}
	}
	return len(seen)
}

func sortFeeds(feeds []BuiltFeed, spec sortSpec) []BuiltFeed {
	out := append([]BuiltFeed(nil), feeds...)
	sort.SliceStable(out, func(i, j int) bool {
		c := cmpFeed(out[i], out[j], spec.key)
		if spec.desc {
			c = -c
		}
		return c < 0
	})
	return out
}

func cmpFeed(a, b BuiltFeed, key string) int {
	if c := cmpFeedKey(a, b, key); c != 0 {
		return c
	}
	// The exact page comes before the timestamp, so the feeds built from one
	// page stay together inside their site's group instead of interleaving with
	// the site's other pages.
	if c := strings.Compare(a.SourceURL, b.SourceURL); c != 0 {
		return c
	}
	// Newest first then, so a run of variants for one page reads as a timeline.
	// The key ends the comparison, so the order is total and the table never
	// reshuffles between requests.
	if !a.FetchedAt.Equal(b.FetchedAt) {
		if a.FetchedAt.After(b.FetchedAt) {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Key, b.Key)
}

func cmpFeedKey(a, b BuiltFeed, key string) int {
	switch key {
	case sortFeed:
		return strings.Compare(a.Key, b.Key)
	case sortItems:
		return cmpInt(a.ItemCount, b.ItemCount)
	case sortBuilt:
		switch {
		case a.FetchedAt.Before(b.FetchedAt):
			return -1
		case a.FetchedAt.After(b.FetchedAt):
			return 1
		}
		return 0
	case sortState:
		return cmpInt(stateRank(a), stateRank(b))
	case sortHealth:
		return cmpInt(healthRank(a), healthRank(b))
	case sortStreak:
		return cmpInt(a.FailureStreak, b.FailureStreak)
	case sortAvgBuild:
		return cmpInt(a.AvgBuildMs, b.AvgBuildMs)
	case sortLastOK:
		switch {
		case a.LastSuccess.IsZero() && b.LastSuccess.IsZero():
			return 0
		case a.LastSuccess.IsZero():
			return 1
		case b.LastSuccess.IsZero():
			return -1
		case a.LastSuccess.Before(b.LastSuccess):
			return 1
		case a.LastSuccess.After(b.LastSuccess):
			return -1
		}
		return 0
	default:
		return strings.Compare(siteOf(a.SourceURL), siteOf(b.SourceURL))
	}
}

// healthRank orders health by severity: failing > never ok > healthy > unknown
func healthRank(f BuiltFeed) int {
	if f.TotalRecent == 0 {
		return 3 // unknown
	}
	if f.FailureStreak > 0 {
		return 0 // failing
	}
	if f.LastSuccess.IsZero() {
		return 1 // never ok
	}
	return 2 // healthy
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// stateRank orders the states by how much attention they want: a failed feed
// first, a fresh one last.
func stateRank(f BuiltFeed) int {
	label, _ := f.State()
	switch label {
	case "failed":
		return 0
	case "stale":
		return 1
	case "not cached":
		return 2
	default:
		return 3
	}
}

func (s *Server) renderFeeds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	built, err := s.admin.BuiltFeeds(ctx)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	spec := parseSort(r)
	groups := groupBySite(sortFeeds(built, spec))
	now := s.nowFn()

	var b strings.Builder
	b.WriteString(feedsPageHead())
	b.WriteString(`<div class="head"><h1>Built feeds</h1><p class="lede">`)
	fmt.Fprintf(&b, `%d feed%s`, len(built), pluralSuffix(len(built)))
	if n := len(groups); n > 0 {
		fmt.Fprintf(&b, ` &middot; %d source%s`, n, pluralSuffix(n))
	}
	if stale := countStale(built); stale > 0 {
		fmt.Fprintf(&b, ` &middot; <span class="warn">%d expired</span>`, stale)
	}
	b.WriteString(`</p></div>`)

	b.WriteString(feedsToolbar)
	if len(built) == 0 {
		b.WriteString(`<div class="table-wrap"><p class="empty">Nothing built yet. ` +
			`Build a feed from the <a href="/">home page</a> and it stays listed here.</p></div>`)
	} else {
		b.WriteString(`<div class="table-wrap"><table>`)
		b.WriteString(feedsColgroup)
		writeFeedsHead(&b, spec)
		b.WriteString(`<tbody>`)
		for i, g := range groups {
			writeGroup(&b, i, g, now)
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(feedsScript)
	b.WriteString(`</main></body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, b.String())
}

const feedsColgroup = `<colgroup>` +
	`<col class="check" style="width: 40px;">` +
	`<col class="source" style="width: 220px;">` +
	`<col class="feed" style="width: 420px;">` +
	`<col class="items" style="width: 60px;">` +
	`<col class="built" style="width: 120px;">` +
	`<col class="status" style="width: 180px;">` +
	`<col class="action" style="width: 160px;">` +
	`</colgroup>`

// feedsToolbar carries the bulk form. It sits outside the table so the row
// forms can live in their own cells; the row checkboxes reach it through the
// form attribute instead. The bar sticks to the top of the window while the
// list scrolls, so the action is still in view when a row far down is ticked.
const feedsToolbar = `<div class="toolbar">` +
	`<form id="bulk" class="bulk" method="post" action="/feeds">` +
	`<span class="count"><span id="sel-count">0</span> selected</span>` +
	`<select name="bulk" aria-label="Bulk action">` +
	`<option value="refresh">Refresh</option>` +
	`<option value="forget">Delete</option>` +
	`</select>` +
	`<button id="bulk-apply" class="btn" type="submit">Apply</button>` +
	`</form>` +
	`<div class="export-menu">` +
	`<a class="btn" href="/feeds/export?format=opml" title="Download every listed feed as OPML 2.0, for import into a reader">Export OPML</a>` +
	`<a class="btn" href="/feeds/export?format=csv" title="Download every listed feed as CSV for spreadsheet import">Export CSV</a>` +
	`<a class="btn" href="/feeds/export?format=json" title="Download every listed feed as JSON for programmatic use">Export JSON</a>` +
	`</div>` +
	`</div>`

func writeFeedsHead(b *strings.Builder, spec sortSpec) {
	b.WriteString(`<thead><tr>`)
	b.WriteString(`<th class="check"><input type="checkbox" id="select-all" aria-label="Select all"></th>`)
	writeSortHeader(b, sortSource, "Source link", "", spec)
	writeSortHeader(b, sortFeed, "Feed", "", spec)
	writeSortHeader(b, sortItems, "Item", "items", spec)
	writeSortHeader(b, sortBuilt, "Built", "built", spec)
	b.WriteString(`<th class="status">Status</th>`)
	b.WriteString(`<th class="action">Action</th>`)
	b.WriteString(`</tr></thead>`)
}

// writeSortHeader links a column so it can be ordered, and shows the direction
// with an arrow. Clicking the active column flips it; clicking another starts
// at that column's natural direction, newest-first for counts and oldest-first
// for names.
func writeSortHeader(b *strings.Builder, key, label, class string, spec sortSpec) {
	attr := ""
	if class != "" {
		attr = ` class="` + class + `"`
	}
	if spec.key == key {
		// The link flips whatever the current direction is.
		arrow, next := "&#8593;", "desc"
		if spec.desc {
			arrow, next = "&#8595;", "asc"
		}
		fmt.Fprintf(b, `<th%s><a href="?sort=%s&amp;dir=%s">%s <span aria-hidden="true">%s</span></a></th>`,
			attr, key, next, label, arrow)
		return
	}
	fmt.Fprintf(b, `<th%s><a href="?sort=%s&amp;dir=%s">%s</a></th>`,
		attr, key, defaultSortDir(key), label)
}

func writeGroup(b *strings.Builder, i int, g feedGroup, now time.Time) {
	id := fmt.Sprintf("g%d", i)
	fmt.Fprintf(b, `<tr class="group" data-group="%s">`, id)
	fmt.Fprintf(b, `<td class="check"><input type="checkbox" data-group-check="%s" `+
		`aria-label="Select every feed from this site"></td>`, id)
	b.WriteString(`<td colspan="6">`)
	fmt.Fprintf(b, `<button type="button" class="chev" data-toggle="%s" aria-expanded="false" `+
		`aria-label="Expand this site">&#9656;</button>`, id)
	if g.Site == "" {
		b.WriteString(`<span class="srclink">(no source)</span>`)
	} else {
		fmt.Fprintf(b, `<span class="srclink">%s</span>`, html.EscapeString(g.Site))
	}
	fmt.Fprintf(b, `<span class="pill">%d feed%s</span>`, len(g.Feeds), pluralSuffix(len(g.Feeds)))
	if pages := distinctPages(g.Feeds); pages > 1 {
		fmt.Fprintf(b, `<span class="pill">%d pages</span>`, pages)
	}
	b.WriteString(`</td></tr>`)
	for _, f := range g.Feeds {
		writeFeedRow(b, id, f, now)
	}
}

func writeFeedRow(b *strings.Builder, group string, f BuiltFeed, now time.Time) {
	fmt.Fprintf(b, `<tr class="child" data-group="%s">`, group)
	fmt.Fprintf(b, `<td class="check"><input type="checkbox" form="bulk" name="keys" value="%s" `+
		`aria-label="Select feed"></td>`, html.EscapeString(f.Key))
	writeSourceCell(b, f)
	writeFeedCell(b, f)
	fmt.Fprintf(b, `<td class="items">%d</td>`, f.ItemCount)
	fmt.Fprintf(b, `<td class="built"><span title="%s">%s</span></td>`,
		html.EscapeString(shortTime(f.FetchedAt)), html.EscapeString(agoTime(f.FetchedAt, now)))
	label, class, tooltip := f.CombinedStatus()
	fmt.Fprintf(b, `<td class="status"><span class="badge %s" title="%s">%s</span></td>`,
		class, html.EscapeString(tooltip), html.EscapeString(label))
	writeRowActions(b, f.Key)
	b.WriteString(`</tr>`)
}

// writeSourceCell names the exact page a feed was built from. The group header
// carries only the site, so this is where the page itself stays visible; two
// feeds of one site are told apart by it.
func writeSourceCell(b *strings.Builder, f BuiltFeed) {
	b.WriteString(`<td class="source">`)
	if f.SourceURL == "" {
		b.WriteString(`&mdash;`)
	} else {
		src := html.EscapeString(f.SourceURL)
		fmt.Fprintf(b, `<a href="%s" title="%s" target="_blank" rel="noopener">%s</a>`,
			src, src, html.EscapeString(shortenURL(f.SourceURL, 38)))
	}
	b.WriteString(`</td>`)
}

// writeFeedCell shows the created feed link. The URL is long and its tail is
// the identifying part, so it is shortened in the middle; the copy button
// carries the whole thing.
func writeFeedCell(b *strings.Builder, f BuiltFeed) {
	key := html.EscapeString(f.Key)
	b.WriteString(`<td class="feed">`)
	fmt.Fprintf(b, `<a class="uri" href="%s" title="%s" target="_blank" rel="noopener">%s</a>`,
		key, key, html.EscapeString(shortenURL(f.Key, 54)))
	fmt.Fprintf(b, `<button type="button" class="btn icon copy" data-copy="%s" `+
		`title="Copy feed link" aria-label="Copy feed link">&#10697;</button>`, key)
	if f.LastError != "" {
		fmt.Fprintf(b, `<div class="err">%s</div>`, html.EscapeString(f.LastError))
	}
	b.WriteString(`</td>`)
}

// writeRowActions puts the per-feed actions in their own form. The form is
// inline in the cell rather than one form around the table, because the bulk
// checkboxes already belong to the toolbar's form and forms cannot nest. The
// preview link sits outside the form: it is a read, and it opens in its own tab
// like the source and feed links do.
func writeRowActions(b *strings.Builder, key string) {
	b.WriteString(`<td class="action"><div class="actions">`)
	if preview, ok := previewURLFor(key); ok {
		fmt.Fprintf(b, `<a class="btn" href="%s" title="Build the feed again and show its items" `+
			`target="_blank" rel="noopener">Preview</a>`, html.EscapeString(preview))
	}
	b.WriteString(`<form method="post" action="/feeds">`)
	fmt.Fprintf(b, `<input type="hidden" name="key" value="%s">`, html.EscapeString(key))
	b.WriteString(`<button class="btn" type="submit" name="action" value="refresh">Refresh</button>`)
	b.WriteString(`<button class="btn danger" type="submit" name="action" value="forget">Forget</button>`)
	b.WriteString(`</form></div></td>`)
}

// previewURLFor swaps a feed URL's path for the preview page and keeps the
// query verbatim, because the query is the feed's configuration: a preview
// built from it shows exactly what the feed produces. A key that is not a URL
// yields no link rather than a broken one.
func previewURLFor(key string) (string, bool) {
	u, err := url.Parse(key)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	u.Path = "/preview"
	return u.String(), true
}

// feedsScript is the whole page's JavaScript: select-all, the group checkbox
// and disclosure, the bulk action's enabled state, and copy. Everything it does
// is an enhancement, so with scripting off the page still lists, groups and
// acts on every feed.
const feedsScript = `<script>
(function () {
 var boxes = document.querySelectorAll('input[name="keys"]');
 var counter = document.getElementById('sel-count');
 var bulkForm = document.getElementById('bulk');
 var apply = document.getElementById('bulk-apply');

 function refreshCount() {
  var n = 0;
  boxes.forEach(function (b) { if (b.checked) n++; });
  if (counter) counter.textContent = String(n);
  if (bulkForm) bulkForm.classList.toggle('has', n > 0);
  // The action is only offered once something is selected, so an empty Apply
  // cannot be fired by mistake. With scripting off the button stays enabled and
  // the server answers an empty selection with a clear 400.
  if (apply) {
   apply.disabled = n === 0;
   apply.classList.toggle('primary', n > 0);
  }
 }

 boxes.forEach(function (b) { b.addEventListener('change', refreshCount); });

 var all = document.getElementById('select-all');
 if (all) all.addEventListener('change', function () {
  boxes.forEach(function (b) { b.checked = all.checked; });
  refreshCount();
 });

 document.querySelectorAll('[data-group-check]').forEach(function (g) {
  g.addEventListener('change', function () {
   var id = g.getAttribute('data-group-check');
   document.querySelectorAll('tr.child[data-group="' + id + '"] input[name="keys"]')
    .forEach(function (b) { b.checked = g.checked; });
   refreshCount();
  });
 });

 document.querySelectorAll('[data-toggle]').forEach(function (t) {
  t.addEventListener('click', function () {
   var id = t.getAttribute('data-toggle');
   var open = t.getAttribute('aria-expanded') === 'true';
   document.querySelectorAll('tr.child[data-group="' + id + '"]')
    .forEach(function (row) { row.classList.toggle('open', !open); });
   t.setAttribute('aria-expanded', open ? 'false' : 'true');
   t.innerHTML = open ? '&#9656;' : '&#9662;';
  });
 });

 document.querySelectorAll('[data-copy]').forEach(function (btn) {
  btn.addEventListener('click', function () {
   var text = btn.getAttribute('data-copy') || '';
   copyText(text).then(function () {
    btn.classList.add('copied');
    btn.innerHTML = '&#10003;';
    setTimeout(function () { btn.classList.remove('copied'); btn.innerHTML = '&#10697;'; }, 1200);
   }, function () {});
  });
 });

 // copyText prefers the async clipboard, which needs a secure context, and
 // falls back to a hidden textarea so copy still works over plain http.
 function copyText(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
   return navigator.clipboard.writeText(text);
  }
  return new Promise(function (resolve, reject) {
   var ta = document.createElement('textarea');
   ta.value = text;
   ta.setAttribute('readonly', '');
   ta.style.position = 'fixed';
   ta.style.opacity = '0';
   document.body.appendChild(ta);
   ta.select();
   var ok = false;
   try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
   document.body.removeChild(ta);
   ok ? resolve() : reject();
  });
 }

 refreshCount();
})();
</script>`

// feedsPageHead is the head of the management page, and the header it shares
// with every other page.
func feedsPageHead() string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>feedme - built feeds</title>` + iconLinksHTML +
		`<script>document.documentElement.classList.add('js')</script>` +
		`<style>` + themeCSS + `</style></head><body><main>` + brandHeader(navNewFeed)
}

func countStale(feeds []BuiltFeed) int {
	n := 0
	for _, f := range feeds {
		if f.Stale {
			n++
		}
	}
	return n
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// shortenURL drops the scheme, then keeps the head and the tail of what is
// left. The tail is kept because it is the part that tells one feed of a source
// apart from the next.
func shortenURL(s string, max int) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if len(s) <= max {
		return s
	}
	head := max * 2 / 5
	tail := max - head - 1
	if head < 1 || tail < 1 {
		return s[:max]
	}
	return s[:head] + "\u2026" + s[len(s)-tail:]
}

// agoTime is the readable form of a build time. The exact timestamp is kept in
// the title, so nothing is lost by showing the distance.
func agoTime(t, now time.Time) string {
	if t.IsZero() {
		return "\u2014"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Local().Format("2 Jan 2006")
	}
}

func shortTime(t time.Time) string {
	if t.IsZero() {
		return "\u2014"
	}
	return t.Local().Format("2006-01-02 15:04")
}
