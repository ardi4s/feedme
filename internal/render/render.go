// Package render turns a JavaScript-rendered page into HTML by asking a
// headless browser to load it.
//
// The browser is a separate process on purpose. Chromium is large, it is an
// attack surface, and it crashes; none of that belongs inside the feed server.
// This package speaks to it over HTTP, so the server stays small and the
// browser can be scaled, restarted, or replaced without touching it.
package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotConfigured is returned when a request asks for rendering but this server
// has no browser to render with. It is a configuration problem, not a site
// problem, and the handler reports it as such.
var ErrNotConfigured = errors.New("render_js was requested but no browser is configured (set render_url)")

// Renderer renders one page to HTML.
//
// It is an interface so the pipeline can be tested against a fake, and so a
// different browser backend can be dropped in without changing the pipeline.
type Renderer interface {
	// Render loads target in a browser and returns the resulting HTML.
	Render(ctx context.Context, target string) ([]byte, error)
}

// HTTPRenderer asks a remote headless browser for a page's HTML.
//
// The request shape is the one browserless exposes at POST /content: a JSON
// body with the URL, an HTML response. Keeping to that contract means the
// sidecar can be any image that implements it, and this package stays on the
// standard library.
type HTTPRenderer struct {
	endpoint string
	client   *http.Client
	maxBytes int64
}

// NewHTTP returns a renderer pointed at a browser service. endpoint is the base
// URL, for example "http://chromium:3000". A zero timeout gets a sensible
// default, because a browser with no deadline can hang a request forever.
func NewHTTP(endpoint string, timeout time.Duration, maxBytes int64) *HTTPRenderer {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	return &HTTPRenderer{
		endpoint: strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		client:   &http.Client{Timeout: timeout},
		maxBytes: maxBytes,
	}
}

// Render loads target and returns the rendered HTML.
func (r *HTTPRenderer) Render(ctx context.Context, target string) ([]byte, error) {
	if r.endpoint == "" {
		return nil, ErrNotConfigured
	}
	payload, err := json.Marshal(map[string]any{
		"url": target,
		// Wait for the network to settle, which is what lets a client-rendered
		// list appear before the HTML is taken.
		"gotoOptions": map[string]any{"waitUntil": "networkidle2"},
	})
	if err != nil {
		return nil, fmt.Errorf("render: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/content", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("render: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/html")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("render: browser request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(snippet))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("render: browser returned %s: %s", resp.Status, msg)
	}

	// One byte past the cap is enough to know the document is too large without
	// reading the whole thing into memory.
	data, err := io.ReadAll(io.LimitReader(resp.Body, r.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("render: read body: %w", err)
	}
	if int64(len(data)) > r.maxBytes {
		return nil, fmt.Errorf("render: document exceeds %d bytes", r.maxBytes)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("render: browser returned an empty document")
	}
	return data, nil
}
