package gnews

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"feedme/internal/fetch"
)

// fakeClient answers the two requests the resolver makes, from a function per
// request, and records the order they arrived in.
type fakeClient struct {
	get  func(rawURL string) (*fetch.Response, error)
	post func(rawURL string, body []byte) (*fetch.Response, error)

	gotURLs  []string
	postURLs []string
	postBody []byte
}

func (f *fakeClient) Get(_ context.Context, rawURL string, _ bool) (*fetch.Response, error) {
	f.gotURLs = append(f.gotURLs, rawURL)
	if f.get == nil {
		return nil, fmt.Errorf("unexpected GET %s", rawURL)
	}
	return f.get(rawURL)
}

func (f *fakeClient) Post(_ context.Context, rawURL, _ string, body []byte) (*fetch.Response, error) {
	f.postURLs = append(f.postURLs, rawURL)
	f.postBody = body
	if f.post == nil {
		return nil, fmt.Errorf("unexpected POST %s", rawURL)
	}
	return f.post(rawURL, body)
}

func ok(body []byte) (*fetch.Response, error) {
	return &fetch.Response{Status: http.StatusOK, Body: []byte(body)}, nil
}

func TestResolve(t *testing.T) {
	const id = "CBMigwFBVV95cUxNZUlxbVpreHZuNnl1UDVNeTJ1ZDF5UWZ2S2Z1Q1lUMjJiOXJWN0FxRE82VE0yYnBnZllVN0ZJS0VDamxJd2hLSmVIRnhLNzdqSFZpWXRvUk9qQ0pQSnZIdm9VTWx3Vi1lM3RZUFMxS1pZYXBLY0FhNVV3TDFhN0RFS0U4WQ"
	const page = `<c-wiz><div jscontroller="x"><div data-n-a-sg="SIG" data-n-a-ts="1790649352"></div></div></c-wiz>`

	t.Run("a signed id costs one page and one exchange", func(t *testing.T) {
		c := &fakeClient{
			get: func(string) (*fetch.Response, error) { return ok([]byte(page)) },
			post: func(string, []byte) (*fetch.Response, error) {
				return ok(answer(`[\"garturlres\",\"https://pluang.com/news-feed/agen-ai\",1,1]`))
			},
		}
		target, recognized, err := NewResolver(c, nil).Resolve(context.Background(),
			"https://news.google.com/rss/articles/"+id+"?oc=5&hl=id&gl=ID&ceid=ID:id")
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !recognized {
			t.Fatal("recognized = false, want true")
		}
		if target != "https://pluang.com/news-feed/agen-ai" {
			t.Fatalf("target = %q", target)
		}
		if len(c.gotURLs) != 1 || len(c.postURLs) != 1 {
			t.Fatalf("got %d GETs and %d POSTs, want one of each: %v %v",
				len(c.gotURLs), len(c.postURLs), c.gotURLs, c.postURLs)
		}
		// The locale has to travel with the request or the page comes back in
		// a language the reader did not ask for.
		got, err := url.Parse(c.gotURLs[0])
		if err != nil {
			t.Fatalf("parse page URL: %v", err)
		}
		if got.Query().Get("hl") != "id" || got.Query().Get("ceid") != "ID:id" {
			t.Fatalf("page URL = %q, want the locale carried over", c.gotURLs[0])
		}
		if c.postURLs[0] != batchexecute {
			t.Fatalf("POST went to %q, want %q", c.postURLs[0], batchexecute)
		}
		// The signature read off the page has to reach the endpoint.
		if !strings.Contains(string(c.postBody), "SIG") {
			t.Fatalf("request body does not carry the signature: %s", c.postBody)
		}
	})

	t.Run("a pre-2024 id costs nothing", func(t *testing.T) {
		c := &fakeClient{} // every request is a failure
		target, recognized, err := NewResolver(c, nil).Resolve(context.Background(),
			"https://news.google.com/rss/articles/"+legacyID("https://example.com/a")+"?oc=5")
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !recognized || target != "https://example.com/a" {
			t.Fatalf("Resolve() = %q, %v", target, recognized)
		}
		if len(c.gotURLs) != 0 || len(c.postURLs) != 0 {
			t.Fatalf("a decodable id made %d requests, want none", len(c.gotURLs)+len(c.postURLs))
		}
	})

	t.Run("a link that is not a Google News link is left alone", func(t *testing.T) {
		c := &fakeClient{}
		target, recognized, err := NewResolver(c, nil).Resolve(context.Background(),
			"https://www.liputan6.com/tekno/read/8301723")
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if recognized {
			t.Fatal("recognized = true, want false")
		}
		if target != "" {
			t.Fatalf("target = %q, want empty", target)
		}
	})

	// Each of these is a way the exchange can stop working. None of them may be
	// reported as "not a Google News link", because the caller's response to
	// that is to publish the link unchanged rather than to skip the body.
	failures := []struct {
		name  string
		setup func(*fakeClient)
	}{
		{
			name: "the article page cannot be read",
			setup: func(c *fakeClient) {
				c.get = func(string) (*fetch.Response, error) {
					return nil, errors.New("fetch: connection refused")
				}
			},
		},
		{
			name: "the article page carries no signature",
			setup: func(c *fakeClient) {
				c.get = func(string) (*fetch.Response, error) {
					return ok([]byte("<html><body>shell</body></html>"))
				}
			},
		},
		{
			name: "the exchange is refused",
			setup: func(c *fakeClient) {
				c.get = func(string) (*fetch.Response, error) { return ok([]byte(page)) }
				c.post = func(string, []byte) (*fetch.Response, error) {
					return nil, errors.New("400 bad request")
				}
			},
		},
		{
			name: "the exchange answers with something else",
			setup: func(c *fakeClient) {
				c.get = func(string) (*fetch.Response, error) { return ok([]byte(page)) }
				c.post = func(string, []byte) (*fetch.Response, error) {
					return ok(answer(`[\"garturlres\",null,0]`))
				}
			},
		},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{}
			tc.setup(c)
			_, recognized, err := NewResolver(c, nil).Resolve(context.Background(),
				"https://news.google.com/rss/articles/"+id+"?oc=5")
			if !recognized {
				t.Fatal("recognized = false; a Google News link that failed to open must still be recognised")
			}
			if err == nil {
				t.Fatal("err = nil, want a failure the caller can report")
			}
		})
	}
}

// The client interface is what the resolver is wired with in cmd/feedme; if a
// *fetch.Client stopped satisfying it, this fails at compile time.
var _ Client = (*fetch.Client)(nil)
