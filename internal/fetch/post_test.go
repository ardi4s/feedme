package fetch

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostSendsTheBodyAndTheContentType(t *testing.T) {
	var gotMethod, gotType, gotBody, gotUA string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotType = r.Header.Get("Content-Type")
		gotUA = r.Header.Get("User-Agent")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	c := New(Options{AllowPrivate: true, UserAgent: "feedme/test"})
	c.hc.Transport = ts.Client().Transport

	resp, err := c.Post(context.Background(), ts.URL+"/batchexecute",
		"application/x-www-form-urlencoded;charset=UTF-8", []byte("f.req=%5B%5B%5B"))
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotType != "application/x-www-form-urlencoded;charset=UTF-8" {
		t.Errorf("content type = %q", gotType)
	}
	if gotBody != "f.req=%5B%5B%5B" {
		t.Errorf("body = %q", gotBody)
	}
	if gotUA != "feedme/test" {
		t.Errorf("user agent = %q, want the client's", gotUA)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Errorf("response body = %q", resp.Body)
	}
}

// A POST asks a question whose answer changes, so a stored copy would be a
// stored answer to a question nobody asked again. Nothing may be read from or
// written to the cache.
func TestPostDoesNotTouchTheCache(t *testing.T) {
	var hits, stored int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		io.WriteString(w, "answer")
	}))
	defer ts.Close()

	c := New(Options{
		AllowPrivate: true,
		CacheGet: func(context.Context, string) (*Response, bool) {
			t.Error("Post consulted the cache")
			return nil, false
		},
		OnStore: func(context.Context, *Response) error {
			stored++
			return nil
		},
	})
	c.hc.Transport = ts.Client().Transport

	for i := 0; i < 2; i++ {
		if _, err := c.Post(context.Background(), ts.URL, "text/plain", nil); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 2 {
		t.Errorf("hits = %d, want one per Post", hits)
	}
	if stored != 0 {
		t.Errorf("Post stored %d responses, want none", stored)
	}
}

// The checks that protect the program from a third party's server apply to a
// POST exactly as they do to a GET: the request is the same kind of request.
func TestPostEnforcesTheSameLimits(t *testing.T) {
	t.Run("a body over the cap is refused", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, strings.Repeat("x", 4096))
		}))
		defer ts.Close()

		c := New(Options{AllowPrivate: true, MaxBodyBytes: 1000})
		c.hc.Transport = ts.Client().Transport

		if _, err := c.Post(context.Background(), ts.URL, "text/plain", nil); err == nil {
			t.Fatal("an oversized response was accepted")
		}
	})

	t.Run("a non-2xx status is an error naming the URL", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadRequest)
		}))
		defer ts.Close()

		c := New(Options{AllowPrivate: true})
		c.hc.Transport = ts.Client().Transport

		_, err := c.Post(context.Background(), ts.URL+"/x", "text/plain", nil)
		if err == nil {
			t.Fatal("a 400 was accepted")
		}
		if !strings.Contains(err.Error(), "400") {
			t.Fatalf("error = %v, want the status in it", err)
		}
	})

	t.Run("a scheme that is not http is refused before any request", func(t *testing.T) {
		c := New(Options{AllowPrivate: true})
		if _, err := c.Post(context.Background(), "ftp://example.com/x", "text/plain", nil); err == nil {
			t.Fatal("an ftp URL was accepted")
		}
	})
}

// robotsFor is how one host opts out without the whole check being turned off,
// which is the only way to read a feed whose publisher forbids its own feed path
// to automated clients.
func TestRobotsForOptsASingleHostOut(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			io.WriteString(w, "User-agent: *\nDisallow: /\n")
			return
		}
		hits++
		io.WriteString(w, "ok")
	}))
	defer ts.Close()

	// The opt-out is keyed by site, the way a site config names it, so the port
	// a test server happens to run on is not part of the name.
	host, _, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("with the check on, the request is refused", func(t *testing.T) {
		hits = 0
		c := New(Options{AllowPrivate: true, RespectRobots: true})
		c.hc.Transport = ts.Client().Transport

		if _, err := c.Get(context.Background(), ts.URL+"/page", true); err == nil {
			t.Fatal("a disallowed path was fetched")
		}
		if hits != 0 {
			t.Fatalf("hits = %d, want none", hits)
		}
	})

	t.Run("the same host is allowed once it opts out", func(t *testing.T) {
		hits = 0
		c := New(Options{
			AllowPrivate:  true,
			RespectRobots: true,
			RobotsFor: func(h string) bool {
				return h != host
			},
		})
		c.hc.Transport = ts.Client().Transport

		resp, err := c.Get(context.Background(), ts.URL+"/page", true)
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != "ok" {
			t.Fatalf("body = %q", resp.Body)
		}
		if hits != 1 {
			t.Fatalf("hits = %d, want 1", hits)
		}
	})

	t.Run("the opt-out does not reach other hosts", func(t *testing.T) {
		hits = 0
		c := New(Options{
			AllowPrivate:  true,
			RespectRobots: true,
			RobotsFor: func(h string) bool {
				return h != "someone-else.example"
			},
		})
		c.hc.Transport = ts.Client().Transport

		if _, err := c.Get(context.Background(), ts.URL+"/page", true); err == nil {
			t.Fatal("the opt-out leaked to this host")
		}
	})
}
