package web

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// The brand images are embedded in the binary, so a route must serve exactly
// the bytes that were compiled in. A missing or renamed asset makes the logo
// and the favicon 404 in production, which is easy to miss because the pages
// still render.
func TestBrandAssetsAreServed(t *testing.T) {
	s := newTestServer(newStub())
	cases := []struct {
		path        string
		contentType string
		body        []byte
		magic       []byte
	}{
		{"/feedme.png", "image/png", logoPNG, []byte("\x89PNG\r\n\x1a\n")},
		{"/favicon.png", "image/png", faviconPNG, []byte("\x89PNG\r\n\x1a\n")},
		{"/favicon.ico", "image/x-icon", faviconICO, []byte("\x00\x00\x01\x00")},
	}
	for _, tc := range cases {
		rec := do(t, s, http.MethodGet, tc.path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", tc.path, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Type"); got != tc.contentType {
			t.Errorf("GET %s: Content-Type = %q, want %q", tc.path, got, tc.contentType)
		}
		if len(tc.body) == 0 {
			t.Errorf("%s: embedded asset is empty", tc.path)
			continue
		}
		if got := rec.Header().Get("Content-Length"); got == "" {
			t.Errorf("GET %s: Content-Length is not set", tc.path)
		}
		if !bytes.Equal(rec.Body.Bytes(), tc.body) {
			t.Errorf("GET %s: served %d bytes, embedded asset is %d bytes", tc.path, rec.Body.Len(), len(tc.body))
		}
		if !bytes.HasPrefix(tc.body, tc.magic) {
			t.Errorf("%s: embedded asset does not start with the expected signature", tc.path)
		}
	}
}

// A HEAD request must answer with the headers but no body, so a browser can
// probe the asset without downloading it.
func TestBrandAssetHeadHasNoBody(t *testing.T) {
	s := newTestServer(newStub())
	for _, path := range []string{"/feedme.png", "/favicon.png", "/favicon.ico"} {
		rec := do(t, s, http.MethodHead, path)
		if rec.Code != http.StatusOK {
			t.Errorf("HEAD %s: status = %d, want 200", path, rec.Code)
			continue
		}
		if rec.Body.Len() != 0 {
			t.Errorf("HEAD %s: body = %d bytes, want 0", path, rec.Body.Len())
		}
	}
}

// Every assembled page head must point at the embedded assets and show the
// logo next to the wordmark; otherwise the browser falls back to a missing
// favicon and the header loses its image.
func TestPagesPointAtBrandAssets(t *testing.T) {
	s := newTestServer(newStub())
	index := do(t, s, http.MethodGet, "/").Body.String()

	for _, want := range []string{
		`<link rel="icon" href="/favicon.ico" sizes="any">`,
		`<link rel="icon" type="image/png" href="/favicon.png">`,
		`<link rel="apple-touch-icon" href="/feedme.png">`,
		`<img src="/feedme.png"`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("the index page is missing %q", want)
		}
	}
	for _, want := range []string{
		`href="/favicon.ico"`,
		`href="/favicon.png"`,
		`href="/feedme.png"`,
	} {
		if !strings.Contains(iconLinksHTML, want) {
			t.Errorf("iconLinksHTML is missing %q", want)
		}
	}
	if !strings.Contains(previewStyle, `.brand img { width: 1.6rem; height: 1.6rem;`) {
		t.Error("the preview header does not size the logo")
	}
}
