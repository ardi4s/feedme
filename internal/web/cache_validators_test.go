package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeedIfNoneMatch(t *testing.T) {
	cases := []struct {
		name          string
		values        []string
		modifiedSince bool
		want          int
	}{
		{name: "exact", values: []string{"$etag"}, want: http.StatusNotModified},
		{name: "weak", values: []string{"W/$etag"}, want: http.StatusNotModified},
		{name: "wildcard", values: []string{"*"}, want: http.StatusNotModified},
		{name: "whitespace", values: []string{" \tW/$etag\t "}, want: http.StatusNotModified},
		{name: "first in list", values: []string{`$etag, "other"`}, want: http.StatusNotModified},
		{name: "last in list", values: []string{`"other", $etag`}, want: http.StatusNotModified},
		{name: "weak in list", values: []string{`"other", W/$etag`}, want: http.StatusNotModified},
		{name: "empty list elements", values: []string{`, , $etag, ,`}, want: http.StatusNotModified},
		{name: "repeated fields", values: []string{`"other"`, "W/$etag"}, want: http.StatusNotModified},
		{name: "empty first field", values: []string{"", "$etag"}, want: http.StatusNotModified},
		{name: "quoted comma", values: []string{`"old,version", $etag`}, want: http.StatusNotModified},
		{name: "no validator", want: http.StatusOK},
		{name: "different", values: []string{`"other"`}, want: http.StatusOK},
		{name: "different weak", values: []string{`W/"other"`}, want: http.StatusOK},
		{name: "different list", values: []string{`"one", W/"two"`}, want: http.StatusOK},
		{name: "unquoted", values: []string{"other"}, want: http.StatusOK},
		{name: "lowercase weak prefix", values: []string{"w/$etag"}, want: http.StatusOK},
		{name: "missing comma", values: []string{`"other" $etag`}, want: http.StatusOK},
		{name: "invalid suffix", values: []string{"$etag invalid"}, want: http.StatusOK},
		{name: "invalid later member", values: []string{`$etag, invalid`}, want: http.StatusOK},
		{name: "unterminated", values: []string{`"other, $etag`}, want: http.StatusOK},
		{name: "control in tag", values: []string{"\"bad\x7f\", $etag"}, want: http.StatusOK},
		{name: "wildcard in list", values: []string{`"other", *`}, want: http.StatusOK},
		{name: "empty overrides date", values: []string{""}, modifiedSince: true, want: http.StatusOK},
		{name: "different overrides date", values: []string{`"other"`}, modifiedSince: true, want: http.StatusOK},
		{name: "malformed overrides date", values: []string{"invalid"}, modifiedSince: true, want: http.StatusOK},
		{name: "weak overrides date", values: []string{"W/$etag"}, modifiedSince: true, want: http.StatusNotModified},
		{name: "date without etag", modifiedSince: true, want: http.StatusNotModified},
	}
	for _, cached := range []bool{false, true} {
		name := "fresh"
		if cached {
			name = "cached"
		}
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(method, func(t *testing.T) {
					for _, tc := range cases {
						t.Run(tc.name, func(t *testing.T) {
							f := newStub().add("https://e.example/news", listPage)
							var cache FeedCache
							if cached {
								cache = newMemCache()
							}
							s := cachedServer(f, cache, nil)
							const target = "/extract?url=https%3A%2F%2Fe.example%2Fnews"
							first := do(t, s, http.MethodGet, target)
							if first.Code != http.StatusOK || first.Header().Get("ETag") == "" {
								t.Fatalf("initial feed: status = %d, ETag = %q", first.Code, first.Header().Get("ETag"))
							}
							req := httptest.NewRequest(method, target, nil)
							for _, value := range tc.values {
								req.Header.Add("If-None-Match", strings.ReplaceAll(value, "$etag", first.Header().Get("ETag")))
							}
							if tc.modifiedSince {
								req.Header.Set("If-Modified-Since", first.Header().Get("Last-Modified"))
							}
							rec := httptest.NewRecorder()
							s.ServeHTTP(rec, req)
							if rec.Code != tc.want {
								t.Errorf("status = %d, want %d", rec.Code, tc.want)
							}
							for _, header := range []string{"ETag", "Last-Modified", "Cache-Control"} {
								if got, want := rec.Header().Get(header), first.Header().Get(header); got != want {
									t.Errorf("%s = %q, want %q", header, got, want)
								}
							}
							if method == http.MethodHead || tc.want == http.StatusNotModified {
								if rec.Body.Len() != 0 {
									t.Errorf("response body has %d bytes, want none", rec.Body.Len())
								}
							} else if rec.Body.String() != first.Body.String() {
								t.Error("the returned feed differs from the initial feed")
							}
							wantFetches := 2
							if cached {
								wantFetches = 1
							}
							if got := f.called(); got != wantFetches {
								t.Errorf("source fetches = %d, want %d", got, wantFetches)
							}
						})
					}
				})
			}
		})
	}
}
