package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"feedme/internal/pipeline"
)

// fakeAdmin records what the management page asked the store to do.
type fakeAdmin struct {
	mu     sync.Mutex
	built  []BuiltFeed
	forgot []string
}

func newFakeAdmin() *fakeAdmin { return &fakeAdmin{} }

func (a *fakeAdmin) BuiltFeeds(context.Context) ([]BuiltFeed, error) { return a.built, nil }

func (a *fakeAdmin) Forget(_ context.Context, key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forgot = append(a.forgot, key)
	return nil
}

func adminServer(f *stubFetcher, a FeedAdmin) *Server {
	pinned := func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	return New(Options{
		Pipeline: &pipeline.Pipeline{Fetch: f, Now: pinned},
		Now:      pinned,
		Cache:    newMemCache(),
		Recorder: &countingRecorder{},
		Admin:    a,
	})
}

func postForm(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func at(t *testing.T, body, want string) int {
	t.Helper()
	i := strings.Index(body, want)
	if i < 0 {
		t.Fatalf("the page does not contain %q:\n%s", want, body)
	}
	return i
}

// The page groups by site, not by the exact URL, so the spellings of one host
// and its subdomains read as one entry with the feeds inside it.
func TestFeedsPageGroupsBySite(t *testing.T) {
	a := newFakeAdmin()
	now := time.Now()
	a.built = []BuiltFeed{
		{Key: "https://h/extract?url=a", SourceURL: "https://kontan.co.id", ItemCount: 3, FetchedAt: now},
		{Key: "https://h/extract?url=b", SourceURL: "https://www.kontan.co.id", ItemCount: 5, FetchedAt: now},
		{Key: "https://h/extract?url=c", SourceURL: "https://nasional.kontan.co.id", ItemCount: 1, FetchedAt: now},
		{Key: "https://h/extract?url=d", SourceURL: "https://www.antaranews.com", ItemCount: 2, FetchedAt: now},
	}

	rec := do(t, adminServer(newStub(), a), http.MethodGet, "/feeds")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "4 feeds") || !strings.Contains(body, "2 sources") {
		t.Errorf("the headings do not count one site per group:\n%s", body)
	}
	if n := strings.Count(body, `class="group"`); n != 2 {
		t.Errorf("group rows = %d, want 2 (kontan and antaranews)", n)
	}
	for _, want := range []string{
		`<span class="srclink">kontan.co.id</span>`,
		`<span class="srclink">antaranews.com</span>`,
		`data-toggle="g0"`, `data-toggle="g1"`,
		`class="child" data-group="g0"`,
		// The group header carries only the site, so the header alone would
		// hide which page each feed came from.
		"3 pages",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
	// Every row keeps its exact page in the source column.
	for _, want := range []string{
		`href="https://kontan.co.id"`,
		`href="https://www.kontan.co.id"`,
		`href="https://nasional.kontan.co.id"`,
		`href="https://www.antaranews.com"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not name the source page %q", want)
		}
	}
}

// siteOf is the whole grouping rule, so it is pinned directly.
func TestSiteOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://kontan.co.id", "kontan.co.id"},
		{"https://www.kontan.co.id", "kontan.co.id"},
		{"https://nasional.kontan.co.id", "kontan.co.id"},
		{"https://www.kontan.co.id/search/indeks?q=1", "kontan.co.id"},
		{"HTTPS://WWW.Kontan.CO.ID/News", "kontan.co.id"},
		{"https://www.antaranews.com", "antaranews.com"},
		{"https://sub.example.co.uk/a/b", "example.co.uk"},
		{"https://github.com/example?tab=stars", "github.com"},
		{"http://127.0.0.1:8080/extract?url=x", "127.0.0.1"},
		{"http://localhost:8080/extract", "localhost"},
	}
	for _, c := range cases {
		if got := siteOf(c.in); got != c.want {
			t.Errorf("siteOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The header offers select-all, the bulk form, and a sort link per column.
func TestFeedsPageHasSelectAllAndBulkActions(t *testing.T) {
	a := newFakeAdmin()
	a.built = []BuiltFeed{{Key: "https://h/extract?url=x", SourceURL: "https://e.example/x", FetchedAt: time.Now()}}

	body := do(t, adminServer(newStub(), a), http.MethodGet, "/feeds").Body.String()
	for _, want := range []string{
		`id="select-all"`,
		`id="bulk"`,
		`name="bulk"`,
		`id="bulk-apply"`,
		`form="bulk" name="keys"`,
		`?sort=built&amp;dir=desc`,
		`?sort=items&amp;dir=desc`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

// A feed that was built but whose cached body has since expired is still
// listed, and says so instead of claiming to be fresh.
func TestFeedsPageListsAFeedKnownOnlyFromHistory(t *testing.T) {
	a := newFakeAdmin()
	key := "https://h/extract?url=https%3A%2F%2Fe.example%2Fnews"
	a.built = []BuiltFeed{{
		Key: key, SourceURL: "https://e.example/news", ItemCount: 7,
		Detector: "article.card", FetchedAt: time.Now(),
	}}

	rec := do(t, adminServer(newStub(), a), http.MethodGet, "/feeds")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, key) {
		t.Errorf("the page does not list the built feed:\n%s", body)
	}
	if !strings.Contains(body, "not cached") {
		t.Errorf("the page does not say the feed is uncached:\n%s", body)
	}
}

// A failed build is listed with its error, because that is the feed an operator
// needs to look at.
func TestFeedsPageShowsAFailedBuild(t *testing.T) {
	a := newFakeAdmin()
	a.built = []BuiltFeed{{
		Key:       "https://h/extract?url=https%3A%2F%2Fe.example%2Fbad",
		FetchedAt: time.Now(), LastError: "listpage: no items found",
	}}

	body := do(t, adminServer(newStub(), a), http.MethodGet, "/feeds").Body.String()
	if !strings.Contains(body, "failed") || !strings.Contains(body, "listpage: no items found") {
		t.Errorf("the page does not show the build failure:\n%s", body)
	}
	if !strings.Contains(body, `class="badge err"`) {
		t.Errorf("a failed feed does not get the error badge:\n%s", body)
	}
}

// The management page is hidden when no admin is wired.
func TestFeedsPageHiddenWithoutAdmin(t *testing.T) {
	rec := do(t, newTestServer(newStub()), http.MethodGet, "/feeds")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// "Refresh now" rebuilds through the same path a reader takes, so it stores a
// cache entry and records the build.
func TestFeedsRowRefreshRebuilds(t *testing.T) {
	a := newFakeAdmin()
	f := newStub().add("https://e.example/news", listPage)
	s := adminServer(f, a)
	key := "http://example.com/extract?url=https%3A%2F%2Fe.example%2Fnews"

	rec := postForm(t, s, "/feeds", url.Values{"action": {"refresh"}, "key": {key}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body)
	}
	if f.called() == 0 {
		t.Error("refresh did not fetch the source")
	}
	// The rendered feed is now cached under the key it was rebuilt for.
	if hit, ok := s.cache.(*memCache).entries[key]; !ok || len(hit.Body) == 0 {
		t.Error("refresh did not store the rendered feed")
	}
}

func TestFeedsRowForget(t *testing.T) {
	a := newFakeAdmin()
	rec := postForm(t, adminServer(newStub(), a), "/feeds", url.Values{"action": {"forget"}, "key": {"k"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.forgot) != 1 || a.forgot[0] != "k" {
		t.Errorf("forgot = %v", a.forgot)
	}
}

// The bulk form forgets every checked feed in one request.
func TestFeedsBulkForget(t *testing.T) {
	a := newFakeAdmin()
	rec := postForm(t, adminServer(newStub(), a), "/feeds",
		url.Values{"bulk": {"forget"}, "keys": {"k1", "k2", "k3"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.forgot) != 3 {
		t.Fatalf("forgot = %v, want all three", a.forgot)
	}
}

func TestFeedsBulkRefresh(t *testing.T) {
	a := newFakeAdmin()
	f := newStub().add("https://e.example/news", listPage)
	s := adminServer(f, a)
	key := "http://example.com/extract?url=https%3A%2F%2Fe.example%2Fnews"

	rec := postForm(t, s, "/feeds", url.Values{"bulk": {"refresh"}, "keys": {key}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
	}
	if f.called() == 0 {
		t.Error("bulk refresh did not fetch the source")
	}
}

// Selecting nothing is a client mistake, not a silent no-op: a bulk Apply on an
// empty selection would otherwise look like it worked.
func TestFeedsBulkWithNoSelectionIsRejected(t *testing.T) {
	rec := postForm(t, adminServer(newStub(), newFakeAdmin()), "/feeds",
		url.Values{"bulk": {"forget"}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFeedsUnknownActionIsRejected(t *testing.T) {
	s := adminServer(newStub(), newFakeAdmin())
	if rec := postForm(t, s, "/feeds", url.Values{"action": {"drop"}, "key": {"k"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("single action status = %d, want 400", rec.Code)
	}
	if rec := postForm(t, s, "/feeds", url.Values{"bulk": {"drop"}, "keys": {"k"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bulk action status = %d, want 400", rec.Code)
	}
}

// Sorting is server-side so the order survives a bookmark and works without
// scripting.
func TestFeedsSortsByItemCount(t *testing.T) {
	a := newFakeAdmin()
	now := time.Now()
	a.built = []BuiltFeed{
		{Key: "k1", SourceURL: "s1", ItemCount: 1, FetchedAt: now},
		{Key: "k2", SourceURL: "s2", ItemCount: 9, FetchedAt: now},
		{Key: "k3", SourceURL: "s3", ItemCount: 5, FetchedAt: now},
	}

	body := do(t, adminServer(newStub(), a), http.MethodGet, "/feeds?sort=items").Body.String()
	if at(t, body, "k2") > at(t, body, "k3") || at(t, body, "k3") > at(t, body, "k1") {
		t.Errorf("sort=items did not order 9, 5, 1:\n%s", body)
	}

	body = do(t, adminServer(newStub(), a), http.MethodGet, "/feeds?sort=items&dir=asc").Body.String()
	if at(t, body, "k1") > at(t, body, "k3") || at(t, body, "k3") > at(t, body, "k2") {
		t.Errorf("sort=items&dir=asc did not order 1, 5, 9:\n%s", body)
	}
}

// Feeding every source an empty page still lists the feeds, grouped under a
// marker rather than dropped.
func TestFeedsPageGroupsAFeedWithNoSource(t *testing.T) {
	a := newFakeAdmin()
	a.built = []BuiltFeed{{Key: "https://h/extract?url=x", FetchedAt: time.Now()}}

	body := do(t, adminServer(newStub(), a), http.MethodGet, "/feeds").Body.String()
	if !strings.Contains(body, "(no source)") {
		t.Errorf("the page does not mark a feed with no source:\n%s", body)
	}
}
