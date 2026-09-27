package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"feedme/internal/feed"
	"feedme/internal/fetch"
	"feedme/internal/pipeline"
)

// stubFetcher serves canned pages and records the calls it received.
type stubFetcher struct {
	mu       sync.Mutex
	pages    map[string]*fetch.Response
	errs     map[string]error
	requests []string
}

func newStub() *stubFetcher {
	return &stubFetcher{pages: map[string]*fetch.Response{}, errs: map[string]error{}}
}

func (s *stubFetcher) add(url, body string) *stubFetcher {
	s.pages[url] = &fetch.Response{
		URL: url, Status: 200, Body: []byte(body),
		ContentType: "text/html", FetchedAt: time.Now(),
	}
	return s
}

func (s *stubFetcher) Get(ctx context.Context, rawURL string, revalidate bool) (*fetch.Response, error) {
	s.mu.Lock()
	s.requests = append(s.requests, rawURL)
	s.mu.Unlock()
	if err, ok := s.errs[rawURL]; ok {
		return nil, err
	}
	if r, ok := s.pages[rawURL]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("stub: no page for %s", rawURL)
}

func (s *stubFetcher) called() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

const listPage = `<html><head><title>Contoh Berita</title></head><body>
<nav class="nav"><a href="/a">a</a><a href="/b">b</a><a href="/c">c</a><a href="/d">d</a></nav>
<div>
 <article class="post"><a href="/2026/09/26/alpha"><h2>Alpha</h2></a>
  <time datetime="2026-09-26">26 September 2026</time><p>Kappa alpha.</p></article>
 <article class="post"><a href="/2026/09/25/beta"><h2>Beta</h2></a>
  <time datetime="2026-09-25">25 September 2026</time><p>Kappa beta.</p></article>
 <article class="post"><a href="/2026/09/24/gamma"><h2>Gamma</h2></a>
  <time datetime="2026-09-24">24 September 2026</time><p>Kappa gamma.</p></article>
</div>
<footer><a href="/x">x</a><a href="/y">y</a><a href="/z">z</a><a href="/w">w</a></footer>
</body></html>`

func newTestServer(f *stubFetcher) *Server {
	return New(Options{
		Pipeline: &pipeline.Pipeline{
			Fetch: f,
			Now:   func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) },
		},
		Now: func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) },
	})
}

func do(t *testing.T, s *Server, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestExtractServesRSS(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/rss+xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"<rss", "Alpha", "Beta", "Gamma"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q", want)
		}
	}
	if got := rec.Header().Get("X-Feedme-Items"); got != "3" {
		t.Errorf("X-Feedme-Items = %q, want 3", got)
	}
	if rec.Header().Get("X-Feedme-Detection") == "" {
		t.Error("the detection header should name the selector that was used")
	}
	if got := rec.Header().Get("X-Feedme-Confidence"); got == "" || got == "low" {
		t.Errorf("X-Feedme-Confidence = %q", got)
	}
}

func TestExtractFormats(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	cases := map[string]string{
		"rss":  "application/rss+xml; charset=utf-8",
		"atom": "application/atom+xml; charset=utf-8",
		"json": "application/feed+json; charset=utf-8",
	}
	for format, ct := range cases {
		rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&format="+format)
		if rec.Code != http.StatusOK {
			t.Fatalf("format %s: status = %d\n%s", format, rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Content-Type"); got != ct {
			t.Errorf("format %s: Content-Type = %q, want %q", format, got, ct)
		}
	}
}

func TestExtractJSONIsValidJSON(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&format=json")
	var doc struct {
		Version string `json:"version"`
		Items   []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, rec.Body)
	}
	if doc.Version == "" {
		t.Error("version is missing")
	}
	if len(doc.Items) != 3 {
		t.Errorf("items = %d", len(doc.Items))
	}
}

// A bad parameter is the caller's mistake and must be reported as such, not as
// a server failure.
func TestExtractRejectsBadParameter(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	cases := []string{
		"/extract",
		"/extract?url=not-a-url",
		"/extract?url=https%3A%2F%2Fe.example%2Fnews&max=0",
		"/extract?url=https%3A%2F%2Fe.example%2Fnews&format=pdf",
		"/extract?url=https%3A%2F%2Fe.example%2Fnews&guid=weird",
		"/extract?url=https%3A%2F%2Fe.example%2Fnews&date_after=nonsense",
		"/extract?url=file%3A%2F%2F%2Fetc%2Fpasswd",
	}
	for _, target := range cases {
		rec := do(t, s, http.MethodGet, target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body: %s)", target, rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: Content-Type = %q, want text/plain", target, ct)
		}
	}
	if f.called() != 0 {
		t.Errorf("a rejected request must not fetch anything, got %d calls", f.called())
	}
}

// The status code is what lets a reader tell "fix your URL" from "try later".
func TestExtractStatusForSiteOutcomes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"missing page", &fetch.StatusError{URL: "https://e.example/", Code: 404}, http.StatusNotFound},
		{"gone", &fetch.StatusError{URL: "https://e.example/", Code: 410}, http.StatusNotFound},
		{"server error", &fetch.StatusError{URL: "https://e.example/", Code: 500}, http.StatusBadGateway},
		{"rate limited", &fetch.StatusError{URL: "https://e.example/", Code: 429}, http.StatusServiceUnavailable},
		{"robots denied", fmt.Errorf("x: %w", fetch.ErrRobotsDenied), http.StatusForbidden},
		{"private address", fmt.Errorf("x: %w", fetch.ErrPrivateBlocked), http.StatusForbidden},
		{"challenge", &fetch.ChallengeError{URL: "https://e.example/", Code: 202}, http.StatusForbidden},
		{"too large", fmt.Errorf("x: %w", fetch.ErrTooLarge), http.StatusBadGateway},
		{"network down", errors.New("dial tcp: connection refused"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		f := newStub()
		f.errs["https://e.example/news"] = c.err
		s := newTestServer(f)
		rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d (body: %s)", c.name, rec.Code, c.want, rec.Body)
		}
	}
}

func TestExtractSetsCacheHeaders(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=900" {
		t.Errorf("Cache-Control = %q", got)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a cacheable response")
	}

	// The feed's own ttl wins over the default, since the publisher chose it.
	rec = do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&ttl=30")
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=1800" {
		t.Errorf("Cache-Control with ttl=30 = %q", got)
	}

	// A refresh must not be cached anywhere, or the reader keeps the old copy.
	rec = do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&refresh=1")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control with refresh = %q", got)
	}
}

func TestConditionalRequest(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	first := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	etag := first.Header().Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Error("a 304 must not carry a body")
	}
	// The pipeline ran again, because the validator is computed from the
	// rendered document and cannot be known before the document exists. In
	// production that second run is answered from the HTTP cache, and the
	// reader still saves the bandwidth of the whole feed. Skipping it entirely
	// would need a cache of rendered feeds, which is not implemented yet.
	if f.called() != 2 {
		t.Errorf("fetches = %d, want 2 (one per request)", f.called())
	}
	// A changed page must invalidate the reader's copy.
	f.add("https://e.example/news", strings.Replace(listPage, "Gamma", "Delta", 1))
	req = httptest.NewRequest(http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 once the page changed", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Delta") {
		t.Error("the new item is missing from the refreshed feed")
	}
}

func TestHeadHasNoBody(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodHead, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned %d bytes", rec.Body.Len())
	}
}

func TestMethodNotAllowed(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodPost, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	rec = do(t, s, http.MethodPost, "/check")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("/check status = %d, want 405", rec.Code)
	}
}

func TestFullTextIsPartialAndReported(t *testing.T) {
	f := newStub()
	f.add("https://e.example/news", listPage)
	f.add("https://e.example/2026/09/26/alpha", `<html><body><article><h1>Alpha</h1>
		<p>Isi artikel alpha yang cukup panjang untuk melewati ambang minimum
		karakter yang ditetapkan ekstraktor agar dapat dipakai sebagai konten
		penuh di dalam feed, bukan sekadar ringkasan pendek dari daftar.</p>
		<p>Paragraf kedua artikel alpha untuk memastikan panjangnya memadai.</p>
		</article></body></html>`)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&fulltext=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	h := rec.Header().Get("X-Feedme-Fulltext")
	if !strings.Contains(h, "1 fetched") {
		t.Errorf("X-Feedme-Fulltext = %q, want the partial result to be visible", h)
	}
	if !strings.Contains(rec.Body.String(), "alpha yang cukup panjang") {
		t.Error("the fetched article body is not in the feed")
	}
	// The two articles that could not be fetched keep their teasers rather than
	// disappearing.
	if !strings.Contains(rec.Body.String(), "Beta") {
		t.Error("an item whose body could not be fetched should still appear")
	}
}

func TestMaxIsHonoured(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&max=1")
	if got := rec.Header().Get("X-Feedme-Items"); got != "1" {
		t.Errorf("X-Feedme-Items = %q, want 1", got)
	}
}

func TestCheckDoesNotFetch(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet,
		"/check?url=https%3A%2F%2Fe.example%2Fnews&fulltext=1&max=5&item%5B%5D=article.post&url_contains%5B%5D=kontan&format=atom")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if f.called() != 0 {
		t.Errorf("fetches = %d, /check must not touch the network", f.called())
	}
	body := rec.Body.String()
	for _, want := range []string{"article.post", "kontan", "atom", "fulltext"} {
		if !strings.Contains(body, want) {
			t.Errorf("the check page does not mention %q", want)
		}
	}
	// The page ends in a link that builds exactly the feed that was described.
	if !strings.Contains(body, "/extract?") {
		t.Error("the check page should link to the feed it describes")
	}
	// Values shown in HTML must be escaped, or a title containing markup
	// becomes a hole in the page.
	rec = do(t, s, http.MethodGet, "/check?url=https%3A%2F%2Fe.example%2Fnews&title=%3Cscript%3Ealert(1)%3C%2Fscript%3E")
	if strings.Contains(rec.Body.String(), "<script>alert(1)") {
		t.Error("the check page did not escape a value")
	}
}

func TestCheckValidates(t *testing.T) {
	f := newStub()
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/check?url=https%3A%2F%2Fe.example%2F&max=0")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// Preview must build the feed and show its items: a user decides whether to
// subscribe by seeing what the feed would contain.
func TestPreviewShowsItems(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet, "/preview?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if f.called() == 0 {
		t.Error("preview must fetch the source, not just describe it")
	}
	body := rec.Body.String()
	for _, want := range []string{"Alpha", "Beta", "Gamma", "https://e.example/2026/09/26/alpha", "items"} {
		if !strings.Contains(body, want) {
			t.Errorf("the preview is missing %q", want)
		}
	}
	if strings.Contains(body, "No items were found") {
		t.Error("the preview reported no items for a page that has three")
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag = %q, want noindex, nofollow", got)
	}
}

func TestPreviewValidates(t *testing.T) {
	s := newTestServer(newStub())
	rec := do(t, s, http.MethodGet, "/preview?url=https%3A%2F%2Fe.example%2F&max=0")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPreviewReportsAnEmptyFeed(t *testing.T) {
	f := newStub().add("https://e.example/plain", `<html><body><p>nothing here</p></body></html>`)
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/preview?url=https%3A%2F%2Fe.example%2Fplain")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "No items were found") {
		t.Error("an empty preview should say so")
	}
}

// A filter that drops everything is the classic empty feed. The preview has to
// name the rule, because "no items" alone cannot be told apart from a page that
// genuinely published nothing.
func TestPreviewExplainsWhatAFilterDropped(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	rec := do(t, newTestServer(f), http.MethodGet,
		"/preview?url=https%3A%2F%2Fe.example%2Fnews&url_contains%5B%5D=%2Fnothing-matches%2F")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "url_contains") {
		t.Errorf("the preview does not name the rule that dropped the items:\n%s", body)
	}
	if !strings.Contains(body, "filtered out") {
		t.Errorf("the preview does not say the items were filtered:\n%s", body)
	}
}

func TestPreviewEscapesItemText(t *testing.T) {
	f := newStub().add("https://e.example/news", `<html><head><title>x</title></head><body>
 <article class="post"><a href="/1"><h2>&lt;script&gt;alert(1)&lt;/script&gt;</h2></a></article>
 <article class="post"><a href="/2"><h2>safe</h2></a></article>
 <article class="post"><a href="/3"><h2>safe too</h2></a></article>
 </body></html>`)
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/preview?url=https%3A%2F%2Fe.example%2Fnews&item%5B%5D=article.post")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script>alert(1)") {
		t.Error("the preview did not escape item text")
	}
}

// The check page must link to a feed URL a reader can actually open: a doubled
// "?" makes /extract reject the request.
func TestCheckLinkIsWellFormed(t *testing.T) {
	s := newTestServer(newStub())
	rec := do(t, s, http.MethodGet, "/check?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "/extract??") {
		t.Error("the check page built a malformed feed link")
	}
}

func TestIndexPageHasAForm(t *testing.T) {
	f := newStub()
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `action="/extract"`) {
		t.Error("the index page should offer a form")
	}
	if !strings.Contains(body, `name="url"`) {
		t.Error("the form should ask for a URL")
	}
}

func TestHealthz(t *testing.T) {
	s := newTestServer(newStub())
	rec := do(t, s, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "ok" {
		t.Errorf("body = %q", got)
	}
}

func TestUnknownPath(t *testing.T) {
	s := newTestServer(newStub())
	if rec := do(t, s, http.MethodGet, "/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestLoggerReceivesRequests(t *testing.T) {
	var buf lockedBuffer
	f := newStub().add("https://e.example/news", listPage)
	s := New(Options{
		Pipeline: &pipeline.Pipeline{Fetch: f},
		Logger:   slog.New(slog.NewTextHandler(&buf, nil)),
	})
	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if !strings.Contains(buf.String(), "/extract") {
		t.Errorf("the logger saw %q", buf.String())
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The server must not be able to write a feed it cannot render, and the
// response must always be one of the three declared formats.
func TestResponseIsAlwaysADeclaredFormat(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case strings.Contains(string(body), "<rss"):
		if !strings.Contains(string(body), "</rss>") {
			t.Error("the RSS document is truncated")
		}
	case strings.Contains(string(body), "<feed"):
		if !strings.Contains(string(body), "</feed>") {
			t.Error("the Atom document is truncated")
		}
	default:
		t.Errorf("unrecognised document:\n%s", body)
	}
	var _ feed.Format = feed.FormatRSS
}

// The index offers three levels of effort, and both actions, so a newcomer can
// build a feed without reading any documentation.
func TestIndexPageOffersModes(t *testing.T) {
	s := newTestServer(newStub())
	body := do(t, s, http.MethodGet, "/").Body.String()

	for _, want := range []string{
		`data-mode="auto"`,
		`data-mode="simple"`,
		`data-mode="advanced"`,
		`data-panel="advanced"`,
		`data-mode="merge"`,
		`data-panel="merge"`,
		`id="feeds"`,
		`id="id_or_class"`,
		`id="render_js"`,
		`id="allow_cross_host"`,
		`id="force_host"`,
		`id="fulltext_max"`,
		`id="preview"`,
		`href="/feeds"`,
		`/preview?`,
		`/extract?`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the index page is missing %q", want)
		}
	}
	if strings.Contains(body, `id="load"`) {
		t.Error("the index page still offers the removed Load parameters button")
	}
}

// The advanced options are collapsed by default, so the builder opens on the
// source field and two buttons. They stay in the form, so a value typed into a
// collapsed group is still submitted; that is why they are <details> rather
// than hidden elements.
func TestIndexCollapsesTheAdvancedGroups(t *testing.T) {
	s := newTestServer(newStub())
	body := do(t, s, http.MethodGet, "/").Body.String()

	if strings.Contains(body, "<legend>") {
		t.Error("a <legend> survived: every group should be a <details>")
	}
	if strings.Contains(body, "<fieldset") {
		t.Error("a <fieldset> survived: it cannot collapse without JavaScript")
	}
	for _, want := range []string{
		`<details class="group" open>`,
		`<summary>Extraction<span class="group-note">CSS or XPath</span></summary>`,
		`<summary>Full text<span class="group-note">Fetch article bodies</span></summary>`,
		`<code>xpath:</code>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the index page is missing %q", want)
		}
	}
	if n := strings.Count(body, `<details class="group">`); n != 6 {
		t.Errorf("%d groups start collapsed, want 6", n)
	}
	if n := strings.Count(body, `<details class="group" open>`); n != 3 {
		t.Errorf("%d groups start open, want 3", n)
	}
}

// Preview must not be a submit button: it opens /preview in a new tab, and a
// submit would navigate away from the form instead.
func TestIndexPreviewIsNotASubmit(t *testing.T) {
	s := newTestServer(newStub())
	body := do(t, s, http.MethodGet, "/").Body.String()
	preview := body[strings.Index(body, `id="preview"`):]
	if i := strings.Index(preview, ">"); i >= 0 {
		preview = preview[:i]
	}
	if strings.Contains(preview, `type="submit"`) {
		t.Error("the Preview button must be type=button, not submit")
	}
}

// render_js on a server with no browser configured is a "not implemented here",
// not a site failure and not a silent empty feed.
func TestRenderJSWithoutBrowserIsNotImplemented(t *testing.T) {
	f := newStub().add("https://e.example/news", `<html><body><div id="app">Loading</div></body></html>`)
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&render_js=1")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body: %s)", rec.Code, rec.Body)
	}
}

const simpleRSS = `<rss version="2.0"><channel><title>Contoh RSS</title>
 <link>https://a.example/</link>
 <item><title>Alpha</title><link>https://a.example/1</link>
  <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>Ringkasan alpha.</description></item>
</channel></rss>`

// A request may name only existing feeds; no listing page is required.
func TestExtractMergesFeedsWithoutAURL(t *testing.T) {
	f := newStub().add("https://a.example/rss", simpleRSS)
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/extract?feeds%5B%5D=https%3A%2F%2Fa.example%2Frss")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "<title>Contoh RSS</title>") {
		t.Errorf("the feed title is missing:\n%s", rec.Body)
	}
	if got := rec.Header().Get("X-Feedme-Feeds"); got != "1 fetched, 0 failed" {
		t.Errorf("X-Feedme-Feeds = %q", got)
	}
}

// Neither a URL nor a feed is a request with no source, which is a 400.
func TestExtractRequiresASource(t *testing.T) {
	s := newTestServer(newStub())
	rec := do(t, s, http.MethodGet, "/extract")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "feeds") {
		t.Errorf("the error should mention feeds: %s", rec.Body)
	}
}

// The check page must describe a merge request, because that is where a user
// confirms what the URL will do before building it.
func TestCheckDescribesMergedFeeds(t *testing.T) {
	s := newTestServer(newStub())
	rec := do(t, s, http.MethodGet, "/check?feeds%5B%5D=https%3A%2F%2Fa.example%2Frss&fulltext=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "a.example/rss") {
		t.Errorf("the check page does not list the feed:\n%s", rec.Body)
	}
}

// The check page must show the raised full-text cap, because it is what decides
// how many article bodies the feed will fetch.
func TestCheckShowsFullTextMax(t *testing.T) {
	s := newTestServer(newStub())
	rec := do(t, s, http.MethodGet, "/check?url=https%3A%2F%2Fe.example%2F&fulltext=1&fulltext_max=75")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "fulltext_max") || !strings.Contains(body, "75") {
		t.Errorf("the check page does not report fulltext_max:\n%s", body)
	}
}

// The check page must show fresh, because it decides whether the feed ever
// serves a stored document.
func TestCheckShowsFresh(t *testing.T) {
	s := newTestServer(newStub())
	rec := do(t, s, http.MethodGet, "/check?url=https%3A%2F%2Fe.example%2F&fresh=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "fresh") {
		t.Errorf("the check page does not report fresh:\n%s", body)
	}
}

// fresh=1 is part of a feed's identity, so the rel="self" link a reader copies
// out of the feed must keep it.
func TestFreshAppearsInTheSelfLink(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&fresh=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "fresh=1") {
		t.Errorf("the self link lost fresh=1:\n%s", rec.Body)
	}
}

// A generated feed must not be indexed.
func TestFeedResponseIsNotIndexable(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)
	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag = %q, want noindex, nofollow", got)
	}
}
