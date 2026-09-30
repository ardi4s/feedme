package gnews

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestArticleID(t *testing.T) {
	const signed = "CBMigwFBVV95cUxNZUlxbVpreHZuNnl1UDVNeTJ1ZDF5UWZ2S2Z1Q1lUMjJiOXJWN0FxRE82VE0yYnBnZllVN0ZJS0VDamxJd2hLSmVIRnhLNzdqSFZpWXRvUk9qQ0pQSnZIdm9VTWx3Vi1lM3RZUFMxS1pZYXBLY0FhNVV3TDFhN0RFS0U4WQ"
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			// The shape a Google News search feed publishes today.
			name: "rss search item",
			in:   "https://news.google.com/rss/articles/" + signed + "?oc=5",
			want: signed,
			ok:   true,
		},
		{
			name: "bare article form",
			in:   "https://news.google.com/articles/" + signed,
			want: signed,
			ok:   true,
		},
		{
			name: "read form",
			in:   "https://news.google.com/read/" + signed + "?hl=en-US&gl=US&ceid=US%3Aen",
			want: signed,
			ok:   true,
		},
		{
			name: "the redirector the older feeds used",
			in:   "https://news.google.com/__i/rss/rd/articles/" + signed + "?oc=5",
			want: signed,
			ok:   true,
		},
		{
			name: "a publisher link is not a Google News link",
			in:   "https://pluang.com/news-feed/shopify-buka-pembayaran-agen-ai",
			want: "",
			ok:   false,
		},
		{
			name: "a search page is not an article",
			in:   "https://news.google.com/search?q=ai&hl=en-US",
			want: "",
			ok:   false,
		},
		{
			// Same path on a host that is not Google's: the form name alone
			// must not be enough.
			name: "another host with an articles path",
			in:   "https://example.com/rss/articles/" + signed,
			want: "",
			ok:   false,
		},
		{
			name: "empty",
			in:   "",
			want: "",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ArticleID(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Fatalf("ArticleID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// legacyID builds an id in the format published before July 2024, where the
// publisher URL is inside the base64 payload.
func legacyID(target string) string {
	body := append([]byte{0x08, 0x13, 0x22, byte(len(target))}, []byte(target)...)
	body = append(body, 0xd2, 0x01, 0x00)
	return base64.StdEncoding.EncodeToString(body)
}

func TestDecodeLegacy(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			name: "a pre-2024 id needs no request",
			in:   legacyID("https://pluang.com/news-feed/shopify-buka-pembayaran-agen-ai-berbasis-browser"),
			want: "https://pluang.com/news-feed/shopify-buka-pembayaran-agen-ai-berbasis-browser",
			ok:   true,
		},
		{
			name: "unpadded base64 is accepted",
			in: strings.TrimRight(legacyID("https://example.com/a"),
				"="),
			want: "https://example.com/a",
			ok:   true,
		},
		{
			// The format that replaced it: the payload is an opaque token, so
			// the answer has to come from Google.
			name: "a signed id is not decodable",
			in:   legacyID("AU_yqLCOXqLrLwLrLwLrLrLwLrLwLrLwLrLw"),
			want: "",
			ok:   false,
		},
		{
			name: "not base64",
			in:   "not-base64!!",
			want: "",
			ok:   false,
		},
		{
			name: "base64 that is not a URL",
			in:   base64.StdEncoding.EncodeToString([]byte{0x08, 0x13, 0x22, 0x03, 'a', 'b', 'c'}),
			want: "",
			ok:   false,
		},
		{
			name: "a length that overruns the payload",
			in:   base64.StdEncoding.EncodeToString([]byte{0x08, 0x13, 0x22, 0x7f, 'a', 'b'}),
			want: "",
			ok:   false,
		},
		{
			name: "empty",
			in:   "",
			want: "",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DecodeLegacy(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Fatalf("DecodeLegacy() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParamsURL(t *testing.T) {
	t.Run("the locale is carried over from the item link", func(t *testing.T) {
		got := paramsURL("ID123", "https://news.google.com/rss/articles/ID123?oc=5&hl=id&gl=ID&ceid=ID:id")
		want := "https://news.google.com/rss/articles/ID123?ceid=ID%3Aid&gl=ID&hl=id"
		if got != want {
			t.Fatalf("paramsURL() = %q, want %q", got, want)
		}
	})
	t.Run("no locale is still a valid request", func(t *testing.T) {
		got := paramsURL("ID123", "https://news.google.com/rss/articles/ID123?oc=5")
		want := "https://news.google.com/rss/articles/ID123"
		if got != want {
			t.Fatalf("paramsURL() = %q, want %q", got, want)
		}
	})
}

func TestParseSignature(t *testing.T) {
	t.Run("the attributes are read wherever they sit", func(t *testing.T) {
		page := `<html><body><c-wiz><div jscontroller="AbCd">
			<div data-n-a-sg="AbIaSL93FrrJML0kqCvXzDbTMrt5" data-n-a-ts="1790649352"></div>
		</div></c-wiz></body></html>`
		sig, err := parseSignature([]byte(page))
		if err != nil {
			t.Fatalf("parseSignature() error = %v", err)
		}
		if sig.value != "AbIaSL93FrrJML0kqCvXzDbTMrt5" {
			t.Fatalf("signature = %q", sig.value)
		}
		if sig.timestamp != 1790649352 {
			t.Fatalf("timestamp = %d", sig.timestamp)
		}
	})
	t.Run("one attribute alone is not enough", func(t *testing.T) {
		page := `<div data-n-a-sg="only-the-signature"></div>`
		if _, err := parseSignature([]byte(page)); err == nil {
			t.Fatal("parseSignature() error = nil, want a failure")
		}
	})
	t.Run("a page with neither attribute is not a failure of the request", func(t *testing.T) {
		// A changed page looks exactly like this, so the error has to be
		// distinguishable from a transport problem by its own text.
		_, err := parseSignature([]byte("<html><body>shell</body></html>"))
		if err != errNoSignature {
			t.Fatalf("parseSignature() error = %v, want %v", err, errNoSignature)
		}
	})
}

func TestBuildRequest(t *testing.T) {
	body, err := buildRequest("ID123", signature{value: "SIG", timestamp: 1790649352})
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	// The form body is one parameter; everything interesting is inside it.
	text := string(body)
	if !strings.HasPrefix(text, "f.req=") {
		t.Fatalf("body does not start with f.req=: %q", text)
	}
	for _, want := range []string{"Fbv4je", "garturlreq", "ID123", "SIG", "1790649352"} {
		if !strings.Contains(text, want) {
			t.Fatalf("body is missing %q: %q", want, text)
		}
	}
}

// answer builds a response in the shape the endpoint returns: an anti-hijacking
// prefix, then rows of JSON whose third element is a JSON string.
func answer(payload string) []byte {
	return []byte(")]}'\n\n" +
		`[["wrb.fr","Fbv4je","` + payload + `",null,null,null,"0"]]` + "\n")
}

func TestParseAnswer(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{
			name: "the usual answer",
			in:   answer(`[\"garturlres\",\"https://pluang.com/news-feed/agen-ai\",1,1]`),
			want: "https://pluang.com/news-feed/agen-ai",
		},
		{
			name: "a row for another endpoint is skipped",
			in: []byte(")]}'\n\n" +
				`[["di",42],["wrb.fr","Fbv4je","[\"garturlres\",\"https://example.com/a\",1]",null,null,null,"0"]]`),
			want: "https://example.com/a",
		},
		{
			name: "a batch with several rows takes the first URL",
			in: []byte(")]}'\n\n" +
				`[["wrb.fr","Fbv4je","[\"garturlres\",\"https://example.com/first\",1]",null,null,null,"0"],` +
				`["wrb.fr","Fbv4je","[\"garturlres\",\"https://example.com/second\",1]",null,null,null,"0"]]`),
			want: "https://example.com/first",
		},
		{
			name: "a refusal is not a URL",
			in:   answer(`[\"garturlres\",null,0]`),
		},
		{
			name: "a payload that is not the decode response is not a URL",
			in:   answer(`[\"error\",\"not allowed\",0]`),
		},
		{
			name: "an html error page is not a URL",
			in:   []byte("<html><body>Error 400</body></html>"),
		},
		{
			name: "empty",
			in:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAnswer(tc.in)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("parseAnswer() = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAnswer() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseAnswer() = %q, want %q", got, tc.want)
			}
		})
	}
}
