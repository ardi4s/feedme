package fetch

import (
	"context"
	"net/http"
	"testing"
)

// The client's user agent is the default, so a request that does not ask for a
// different one is not dressed as anyone else.
func TestApplyHeadersUsesClientUserAgentByDefault(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://e.example/", nil)
	applyHeaders(req, "feedme/0.1")
	if got := req.Header.Get("User-Agent"); got != "feedme/0.1" {
		t.Errorf("User-Agent = %q, want the client default", got)
	}
	if got := req.Header.Get("Referer"); got != "" {
		t.Errorf("Referer = %q, want empty", got)
	}
}

func TestApplyHeadersLetsTheRequestOverrideTheUserAgent(t *testing.T) {
	ctx := WithRequestOptions(context.Background(), RequestOptions{UserAgent: "FeedReader/2.0"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://e.example/", nil)
	applyHeaders(req, "feedme/0.1")
	if got := req.Header.Get("User-Agent"); got != "FeedReader/2.0" {
		t.Errorf("User-Agent = %q, want the override", got)
	}
}

func TestApplyHeadersSendsTheReferer(t *testing.T) {
	ctx := WithRequestOptions(context.Background(), RequestOptions{Referer: "https://e.example/list"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://e.example/a", nil)
	applyHeaders(req, "feedme/0.1")
	if got := req.Header.Get("Referer"); got != "https://e.example/list" {
		t.Errorf("Referer = %q", got)
	}
	if got := req.Header.Get("User-Agent"); got != "feedme/0.1" {
		t.Errorf("User-Agent = %q, want the client default", got)
	}
}

// The zero value carries nothing, so it must not wrap the context at all.
func TestWithRequestOptionsZeroValueIsANoOp(t *testing.T) {
	base := context.Background()
	if got := WithRequestOptions(base, RequestOptions{}); got != base {
		t.Error("a zero RequestOptions should return the context unchanged")
	}
}
