package web

import (
	"encoding/xml"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The OPML export is the management page's feed list in another format, so it
// sits behind the same gate and answers with the same credentials. These tests
// pin the boundary and the document shape: a reader that imports the file gets
// one outline per listed feed, with the feed URL it can subscribe to.

func TestOPMLRequiresTheToken(t *testing.T) {
	// Admin is nil here, so a request that clears the gate answers 404 rather
	// than writing a document. That keeps these cases about authorization
	// alone, the same distinction manage_auth_test.go pins for /feeds.
	s := New(Options{ManageToken: testToken})
	cases := []struct {
		name     string
		decorate func(*http.Request)
		want     int
	}{
		{"no credentials", nil, http.StatusUnauthorized},
		{"wrong basic password", func(r *http.Request) { r.SetBasicAuth("feedme", "wrong") }, http.StatusUnauthorized},
		{"wrong bearer token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, http.StatusUnauthorized},
		{"basic, username ignored", func(r *http.Request) { r.SetBasicAuth("anything", testToken) }, http.StatusNotFound},
		{"bearer token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken) }, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doDecorated(t, s, http.MethodGet, opmlPath, tc.decorate)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestOPMLOpenWithoutAToken(t *testing.T) {
	// No token means no login at all and the export behaves as it did before
	// auth existed, exactly as the page does. The admin is wired, so a 404 here
	// would mean the document itself is broken, not the gate.
	rec := do(t, adminServer(nil, newFakeAdmin()), http.MethodGet, opmlPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	if _, err := parseOPML(rec.Body.Bytes()); err != nil {
		t.Fatalf("the body is not well-formed OPML: %v", err)
	}
}

func TestOPMLListsEveryBuiltFeed(t *testing.T) {
	a := newFakeAdmin()
	a.built = []BuiltFeed{
		{
			Key:       "https://feedme.example/extract?url=https%3A%2F%2Fnews.example%2Ffeed&fulltext=1",
			SourceURL: "https://news.example/feed",
			ItemCount: 12,
		},
		{
			Key:       "https://feedme.example/extract?url=https%3A%2F%2Fother.example%2Fnews",
			SourceURL: "https://other.example/news",
		},
	}
	rec := do(t, adminServer(nil, a), http.MethodGet, opmlPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	doc, err := parseOPML(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("the body is not well-formed OPML: %v", err)
	}
	if doc.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", doc.Version)
	}
	if len(doc.Body) != len(a.built) {
		t.Fatalf("%d outlines, want %d", len(doc.Body), len(a.built))
	}
	for i, want := range a.built {
		got := doc.Body[i]
		if got.XMLURL != want.Key {
			t.Errorf("outline %d xmlUrl = %q, want the feed URL %q", i, got.XMLURL, want.Key)
		}
		if got.HTMLURL != want.SourceURL {
			t.Errorf("outline %d htmlUrl = %q, want the source %q", i, got.HTMLURL, want.SourceURL)
		}
		if got.Text == "" {
			t.Errorf("outline %d has a blank name; a reader would list it unnamed", i)
		}
		if got.Type != "rss" {
			t.Errorf("outline %d type = %q, want rss", i, got.Type)
		}
	}
}

func TestOPMLSurvivesQueryPunctuation(t *testing.T) {
	// A feed URL is a query string, so its & and = are everywhere. The escaping
	// is the whole correctness question: an unescaped & ends the attribute and
	// the rest of the URL is lost on import.
	a := newFakeAdmin()
	a.built = []BuiltFeed{{
		Key:       "https://feedme.example/extract?url=https%3A%2F%2Fe.example%2Fn%3Fa%3D1%26b%3D2&fulltext=1&max=5",
		SourceURL: "https://e.example/n?a=1&b=2",
	}}
	rec := do(t, adminServer(nil, a), http.MethodGet, opmlPath)
	doc, err := parseOPML(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("the body is not well-formed OPML: %v", err)
	}
	if got := doc.Body[0].XMLURL; got != a.built[0].Key {
		t.Errorf("xmlUrl did not survive the round trip:\n got %q\nwant %q", got, a.built[0].Key)
	}
}

func TestOPMLEmptyListIsAValidDocument(t *testing.T) {
	// Nothing built yet is a normal answer, and a reader importing an empty
	// file should see a document it can read rather than an error page.
	rec := do(t, adminServer(nil, newFakeAdmin()), http.MethodGet, opmlPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	doc, err := parseOPML(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("the body is not well-formed OPML: %v", err)
	}
	if len(doc.Body) != 0 {
		t.Errorf("%d outlines, want 0", len(doc.Body))
	}
}

func TestOPMLMergeFeedWithoutASourceIsNamed(t *testing.T) {
	// A merge with no url= has no source page, so the name falls back to the
	// feed URL rather than going out blank.
	a := newFakeAdmin()
	a.built = []BuiltFeed{{Key: "https://feedme.example/extract?feeds%5B%5D=https%3A%2F%2Fa.example%2Ffeed"}}
	rec := do(t, adminServer(nil, a), http.MethodGet, opmlPath)
	doc, err := parseOPML(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("the body is not well-formed OPML: %v", err)
	}
	if got := doc.Body[0].Text; got != a.built[0].Key {
		t.Errorf("text = %q, want the feed URL as the fallback name", got)
	}
}

func TestOPMLHeaders(t *testing.T) {
	// The file is a download: the type tells a browser what it is, the
	// disposition names it, and no-store keeps a stale list out of a cache.
	a := newFakeAdmin()
	a.built = []BuiltFeed{{Key: "https://feedme.example/extract?url=https%3A%2F%2Fe.example"}}
	rec := do(t, adminServer(nil, a), http.MethodGet, opmlPath)
	if ct := rec.Header().Get("Content-Type"); ct != "text/x-opml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="feedme-`) || !strings.HasSuffix(cd, `.opml"`) {
		t.Errorf("Content-Disposition = %q, want an attached .opml filename", cd)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestOPMLHasNoStoreBodyWhenPOSTed(t *testing.T) {
	// Only GET answers with a document; anything else is a method error, the
	// same as the page.
	rec := do(t, adminServer(nil, newFakeAdmin()), http.MethodPost, opmlPath)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestFeedsPageLinksTheExport(t *testing.T) {
	// The export is reached from the toolbar, so the page names it.
	a := newFakeAdmin()
	a.built = []BuiltFeed{{Key: "https://feedme.example/extract?url=https%3A%2F%2Fe.example"}}
	body := do(t, adminServer(nil, a), http.MethodGet, feedsPath).Body.String()
	at(t, body, `href="/feeds/opml"`)
}

func TestOPMLAcceptsTheSessionCookie(t *testing.T) {
	// The session cookie is scoped to Path=/feeds, and a cookie only travels to
	// paths below its path. /feeds.opml would not carry it and a signed-in
	// operator clicking the export link would be sent back to the sign-in
	// form, which is why the export lives at /feeds/opml.
	a := newFakeAdmin()
	a.built = []BuiltFeed{{Key: "https://feedme.example/extract?url=https%3A%2F%2Fe.example"}}
	// Admin and the token are both wired, so the sign-in form has something to
	// trade for a cookie.
	s := New(Options{ManageToken: testToken, Admin: a})

	cookie := signIn(t, s, testToken)
	if cookie.Path != feedsPath {
		t.Errorf("cookie path = %q, want %q", cookie.Path, feedsPath)
	}

	req := httptest.NewRequest(http.MethodGet, opmlPath, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the session cookie did not reach %s: got %d", opmlPath, rec.Code)
	}
	if _, err := parseOPML(rec.Body.Bytes()); err != nil {
		t.Fatalf("the body is not well-formed OPML: %v", err)
	}
}

func TestFeedsPageLinksAPreviewPerFeed(t *testing.T) {
	// The preview is built from the feed's own query, so it shows exactly what
	// the feed produces. The link is escaped, so an ampersand in the query goes
	// out as the entity a browser reads back as the same URL.
	a := newFakeAdmin()
	a.built = []BuiltFeed{{
		Key: "https://feedme.example/extract?url=https%3A%2F%2Fe.example%2Fnews&fulltext=1",
	}}
	body := do(t, adminServer(nil, a), http.MethodGet, feedsPath).Body.String()
	preview, ok := previewURLFor(a.built[0].Key)
	if !ok {
		t.Fatal("no preview link for a key that is one")
	}
	at(t, body, `<a class="btn" href="`+html.EscapeString(preview)+`"`)
}

func TestPreviewURLForKeepsTheQuery(t *testing.T) {
	key := "https://feedme.example/extract?url=https%3A%2F%2Fe.example&fulltext=1&max=5"
	got, ok := previewURLFor(key)
	if !ok {
		t.Fatal("no link for a URL that is one")
	}
	want := "https://feedme.example/preview?url=https%3A%2F%2Fe.example&fulltext=1&max=5"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPreviewURLForRefusesWhatIsNotAURL(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"no scheme", "feedme.example/extract?url=x"},
		{"no host", "not a url at all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := previewURLFor(tc.key); ok {
				t.Errorf("got %q for a key that is not a URL", got)
			}
		})
	}
}

// parseOPML decodes the document the way a reader importing it would, so a test
// that passes here means the file is usable outside this server.
func parseOPML(body []byte) (*opmlDoc, error) {
	var doc opmlDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
