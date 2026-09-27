package fetch

import (
	"context"
	"net/http"
)

// RequestOptions are the per-request header overrides.
//
// They live on the context rather than on the Client because the Client is
// shared by every request the server handles, and a feed URL is allowed to
// name its own user agent. Two feed requests arriving at the same time may ask
// for different agents, so this cannot be a field on the Client.
type RequestOptions struct {
	// UserAgent replaces the client's default agent for this request.
	UserAgent string
	// Referer is sent when set. Some sites serve an article body to a request
	// that arrived from their own listing page and not to a bare one.
	Referer string
}

type requestOptionsKey struct{}

// WithRequestOptions returns a context carrying the given overrides. A nil or
// zero value returns ctx unchanged.
func WithRequestOptions(ctx context.Context, o RequestOptions) context.Context {
	if o.UserAgent == "" && o.Referer == "" {
		return ctx
	}
	return context.WithValue(ctx, requestOptionsKey{}, o)
}

// RequestOptionsFrom returns the overrides on ctx, or a zero value.
func RequestOptionsFrom(ctx context.Context) RequestOptions {
	if ctx == nil {
		return RequestOptions{}
	}
	if o, ok := ctx.Value(requestOptionsKey{}).(RequestOptions); ok {
		return o
	}
	return RequestOptions{}
}

// applyHeaders writes the request headers, letting the per-request values win
// over the client defaults.
func applyHeaders(req *http.Request, clientUA string) {
	agent := clientUA
	if o := RequestOptionsFrom(req.Context()); o.UserAgent != "" {
		agent = o.UserAgent
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en,*;q=0.5")
	if ref := RequestOptionsFrom(req.Context()).Referer; ref != "" {
		req.Header.Set("Referer", ref)
	}
	// Accept-Encoding is deliberately not set: Go's transport adds gzip itself
	// and transparently decompresses the response. Setting it by hand disables
	// that behaviour and we would end up parsing gzip bytes as text.
}
