package fetch

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html/charset"
)

func TestDetectChallengeMarkers(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		want       bool
		anyOfWords []string
	}{
		{"cloudflare", 200,
			`<html><head><title>Just a moment...</title></head><body></body></html>`,
			true, []string{"just a moment", "cf"}},
		{"datadome", 200,
			`<html><body>Verifying you are human<script src="https://js.datadome.co/t.js"></script></body></html>`,
			true, []string{"verifying you are human", "data-dome"}},
		{"incapsula", 200,
			`<html><body>Request unsuccessful. Incapsula incident 1234</body></html>`,
			true, []string{"incapsula"}},
		{"202 empty shell", 202,
			`<html><head><title>Kompas.com</title></head><body><div id="root"></div></body></html>`,
			true, []string{"202"}},
		{"200 empty shell is not a challenge", 200,
			`<html><head><title>Small page</title></head><body><div id="root"></div></body></html>`,
			false, nil},
		{"real article", 200,
			`<html><body><article><p>` + strings.Repeat("word ", 200) + `</p></article></body></html>`,
			false, nil},
		{"empty", 200, ``, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}, StatusCode: tc.status}
			resp.Header.Set("Content-Type", "text/html; charset=utf-8")
			marker, got := detectChallenge(resp, []byte(tc.body))
			if got != tc.want {
				t.Fatalf("detectChallenge = %v (%q), want %v", got, marker, tc.want)
			}
			if len(tc.anyOfWords) == 0 {
				return
			}
			hit := false
			for _, w := range tc.anyOfWords {
				if strings.Contains(marker, w) {
					hit = true
				}
			}
			if !hit {
				t.Errorf("marker = %q, want one of %v", marker, tc.anyOfWords)
			}
		})
	}
}

// A 202 with a challenge page is the signature of an anti-bot layer. Treating
// it as success made the extractor "succeed" on the interstitial, which is
// worse than reporting the block.
func TestChallengeDetectedOn202Interstitial(t *testing.T) {
	resp := &http.Response{Header: http.Header{}, StatusCode: http.StatusAccepted}
	resp.Header.Set("Content-Type", "text/html")
	body := []byte(`<html><head><title>Kompas.com</title></head><body><div id="root"></div></body></html>`)

	marker, challenged := detectChallenge(resp, body)
	if !challenged {
		t.Fatal("a 202 with a tiny empty HTML body should be treated as a challenge")
	}
	err := statusError("https://kompas.com/a", 0, nil)
	_ = err
	e := &ChallengeError{URL: "https://kompas.com/a", Code: 202, Size: len(body), Marker: marker}
	if !strings.Contains(e.Error(), "bot-protection") {
		t.Errorf("error should name the problem: %s", e.Error())
	}
	if !strings.Contains(e.Error(), "JavaScript") {
		t.Errorf("error should say what to do about it: %s", e.Error())
	}
}

// A large body is never scanned, so a real long article cannot be turned into
// a false challenge just because it discusses captchas.
func TestChallengeDetectionSkipsLargeBodies(t *testing.T) {
	resp := &http.Response{Header: http.Header{}, StatusCode: 200}
	resp.Header.Set("Content-Type", "text/html")
	big := []byte(`<html><body><p>please enable javascript and cookies to continue</p>` +
		strings.Repeat("<p>filler content here</p>", 40000) + `</body></html>`)
	if _, challenged := detectChallenge(resp, big); challenged {
		t.Error("a large body must not be treated as a challenge page")
	}
}

func TestChallengeMarkerRequiresHTMLContentType(t *testing.T) {
	// A JSON API can legitimately contain the word "captcha" in its data.
	resp := &http.Response{Header: http.Header{}, StatusCode: 200}
	resp.Header.Set("Content-Type", "application/json")
	body := []byte(`{"message":"checking your browser"}`)
	if _, challenged := detectChallenge(resp, body); challenged {
		t.Error("non-HTML content types must not trigger challenge detection")
	}
}

func TestStatusErrorGivesActionableHint(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{http.StatusForbidden, "user agents"},
		{http.StatusMethodNotAllowed, "blocks this client"},
		{http.StatusTooManyRequests, "rate limited"},
		{http.StatusNotFound, "404"},
	}
	for _, tc := range cases {
		e := statusError("https://e.com/a", tc.code, []byte("nope"))
		if !strings.Contains(e.Error(), tc.want) {
			t.Errorf("HTTP %d error %q should mention %q", tc.code, e.Error(), tc.want)
		}
	}
	if se, ok := statusError("https://e.com/a", 404, nil).(*StatusError); !ok {
		t.Error("statusError should return a *StatusError")
	} else if se.IsBlocked() {
		t.Error("a 404 is a missing page, not an anti-bot block")
	}
	if !statusError("https://e.com/a", 403, nil).(*StatusError).IsBlocked() {
		t.Error("a 403 should be reported as a block")
	}
}

func TestStatusSnippetIsShortened(t *testing.T) {
	got := statusSnippet([]byte(strings.Repeat("very long error text ", 100)))
	if len(got) > 130 {
		t.Errorf("snippet should be shortened, got %d bytes: %q", len(got), got)
	}
	if statusSnippet([]byte("   \n  ")) != "" {
		t.Error("blank body should give an empty snippet")
	}
	if statusSnippet([]byte("first line\nsecond line")) != "first line" {
		t.Error("snippet should be a single line")
	}
}

func TestDecodeWithCharsets(t *testing.T) {
	// ISO-8859-1 declared in the header: the single high byte must decode to ñ.
	latin1 := []byte("El se\xf1or")
	enc, _ := charset.Lookup("iso-8859-1")
	got, err := decodeWith(enc, latin1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "El señor" {
		t.Errorf("latin-1 decode = %q, want %q", got, "El señor")
	}

	// UTF-8 must survive untouched.
	enc, _ = charset.Lookup("utf-8")
	got, err = decodeWith(enc, []byte("El señor"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "El señor" {
		t.Errorf("utf-8 decode = %q", got)
	}
}

func TestMetaCharset(t *testing.T) {
	cases := map[string]string{
		`<meta charset="iso-8859-1">`: "iso-8859-1",
		`<meta http-equiv="Content-Type" content="text/html; charset=windows-1252">`: "windows-1252",
		`<meta charset='utf-8'>`:                 "utf-8",
		`<html><body>no meta here</body></html>`: "",
	}
	for in, want := range cases {
		if got := metaCharset([]byte(in)); got != want {
			t.Errorf("metaCharset(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRobotsParsing(t *testing.T) {
	r := ParseRobots(`
User-agent: *
Disallow: /private/
Allow: /private/public
Disallow: /*.json$
Crawl-delay: 2

User-agent: BadBot
Disallow: /
`)
	if r == nil {
		t.Fatal("ParseRobots returned nil")
	}
	if r.Allowed("/private/secret") {
		t.Error("a disallowed path should be blocked")
	}
	if !r.Allowed("/private/public") {
		t.Error("Allow should override an earlier Disallow for the same agent")
	}
	if r.Allowed("/data.json") {
		t.Error("a wildcard suffix rule should block .json")
	}
	if !r.Allowed("/news/article") {
		t.Error("an unrelated path should be allowed")
	}
}

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1",
		"169.254.169.254", // cloud metadata: the classic SSRF target
		"0.0.0.0", "::1", "fd00::1", "fe80::1",
	}
	for _, s := range blocked {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"1.1.1.1", "8.8.8.8", "2001:4860:4860::8888", "93.184.216.34"}
	for _, s := range allowed {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestValidateURLRejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.com/x",
		"gopher://example.com/",
		"javascript:alert(1)",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if err := validateURL(u); err == nil {
			t.Errorf("%s should be rejected", raw)
		}
	}
	u, _ := url.Parse("https://example.com/a")
	if err := validateURL(u); err != nil {
		t.Errorf("a plain https URL should be allowed: %v", err)
	}
}

func TestNormalizeURLDropsFragment(t *testing.T) {
	u, _ := url.Parse("https://Example.COM/a/b?x=1#frag")
	got := normalizeURL(u)
	if strings.Contains(got, "#") {
		t.Errorf("normalizeURL kept a fragment: %q", got)
	}
	if !strings.HasPrefix(got, "https://example.com/a/b") {
		t.Errorf("normalizeURL = %q, want lowercase host", got)
	}
}

// The SSRF guard must apply to redirects too, not just the first request.
func TestDialControlBlocksPrivateTargets(t *testing.T) {
	dial := dialControl(&net.Dialer{Timeout: 0}, false)
	_, err := dial(context.Background(), "tcp", "10.1.2.3:80")
	if err == nil {
		t.Error("dialling a private address should fail")
	}
	if !strings.Contains(err.Error(), "private") {
		t.Errorf("error should explain why: %v", err)
	}
}

// A cached copy answers a request unless the caller asks for a fresh one. The
// flag is the difference between serving a reader quickly and publishing a page
// that changed since the copy was stored.
func TestGetPrefersTheCacheUnlessForcedFresh(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<html><body>live</body></html>")
	}))
	defer ts.Close()

	cached := &Response{
		URL: ts.URL + "/", Status: 200, Body: []byte("<html><body>cached</body></html>"),
		ContentType: "text/html; charset=utf-8",
	}
	c := New(Options{
		AllowPrivate: true,
		CacheGet: func(context.Context, string) (*Response, bool) {
			return cached, true
		},
	})
	c.hc.Transport = ts.Client().Transport

	got, err := c.Get(context.Background(), ts.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "<html><body>cached</body></html>" || !got.FromCache {
		t.Errorf("forceFresh=false should answer from the cache, got %q fromCache=%v", got.Body, got.FromCache)
	}
	if hits != 0 {
		t.Fatalf("forceFresh=false reached the network: %d hits", hits)
	}

	got, err = c.Get(context.Background(), ts.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "<html><body>live</body></html>" {
		t.Errorf("forceFresh=true should reach the origin, got %q", got.Body)
	}
	if hits != 1 {
		t.Fatalf("forceFresh=true made %d network hits, want 1", hits)
	}
}

// An empty cache is still a miss: a caller that allowed caching must fall
// through to the network rather than serve nothing.
func TestGetFetchesWhenTheCacheIsEmpty(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		io.WriteString(w, "<html><body>live</body></html>")
	}))
	defer ts.Close()

	c := New(Options{
		AllowPrivate: true,
		CacheGet:     func(context.Context, string) (*Response, bool) { return nil, false },
	})
	c.hc.Transport = ts.Client().Transport

	got, err := c.Get(context.Background(), ts.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "<html><body>live</body></html>" {
		t.Errorf("a cache miss should fetch, got %q", got.Body)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}
