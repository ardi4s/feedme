package render

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The renderer must speak the contract the sidecar exposes: a JSON body with the
// URL, posted to /content, answered with HTML.
func TestHTTPRendererPostsTheURLAndReturnsHTML(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><body>rendered</body></html>")
	}))
	defer srv.Close()

	out, err := NewHTTP(srv.URL, time.Second, 1<<20).Render(context.Background(), "https://e.example/news")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/content" {
		t.Errorf("path = %q, want /content", gotPath)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("request body %q is not JSON: %v", gotBody, err)
	}
	if payload["url"] != "https://e.example/news" {
		t.Errorf("url in body = %v", payload["url"])
	}
	if string(out) != "<html><body>rendered</body></html>" {
		t.Errorf("output = %q", out)
	}
}

func TestHTTPRendererErrorsOnBrowserFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no browser capacity", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := NewHTTP(srv.URL, time.Second, 1<<20).Render(context.Background(), "https://e.example/")
	if err == nil {
		t.Fatal("want an error when the browser fails")
	}
	if !strings.Contains(err.Error(), "no browser capacity") {
		t.Errorf("error %q should carry the browser's message", err)
	}
}

func TestHTTPRendererRejectsAnEmptyDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	if _, err := NewHTTP(srv.URL, time.Second, 1<<20).Render(context.Background(), "https://e.example/"); err == nil {
		t.Fatal("want an error for an empty document")
	}
}

func TestHTTPRendererEnforcesTheSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 4096))
	}))
	defer srv.Close()

	if _, err := NewHTTP(srv.URL, time.Second, 100).Render(context.Background(), "https://e.example/"); err == nil {
		t.Fatal("want an error for an oversized document")
	}
}

func TestHTTPRendererNeedsAnEndpoint(t *testing.T) {
	_, err := NewHTTP("", 0, 0).Render(context.Background(), "https://e.example/")
	if !errors.Is(err, ErrNotConfigured) {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}
