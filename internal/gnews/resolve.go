package gnews

import (
	"context"
	"fmt"
	"log/slog"

	"feedme/internal/fetch"
)

// Client is the part of the HTTP layer the resolver needs: one GET for the
// article page that carries the signature, one POST for the endpoint that
// exchanges it. Declared here rather than taken as *fetch.Client so that the
// decoder can be tested without a transport.
type Client interface {
	Get(ctx context.Context, rawURL string, forceFresh bool) (*fetch.Response, error)
	Post(ctx context.Context, rawURL, contentType string, body []byte) (*fetch.Response, error)
}

// Resolver opens Google News links.
//
// One instance is shared by every worker building a feed. It holds no state
// between calls on purpose: the signature is bound to one article and expires,
// so a cached answer would be a cached answer to a question that has since
// changed.
type Resolver struct {
	client Client
	logger *slog.Logger
}

// NewResolver returns a Resolver that asks client for the two requests, and
// reports what goes wrong to logger. A nil logger discards the detail, which is
// the right default for a test.
func NewResolver(client Client, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Resolver{client: client, logger: logger}
}

// Resolve returns the publisher URL behind a Google News link, whether the link
// was one this package recognises, and why it could not open it.
//
// The three answers are separate because a caller treats them differently: an
// unrecognised link is used as it stands, while a recognised one that could not
// be opened should be reported rather than fetched, since following it would
// only arrive back at the same shell. Nothing here fails the build.
func (r *Resolver) Resolve(ctx context.Context, rawURL string) (string, bool, error) {
	id, ok := ArticleID(rawURL)
	if !ok {
		return "", false, nil
	}
	// A pre-2024 id carries its own destination, so the common case for an old
	// link or a cached feed costs no request at all.
	if target, ok := DecodeLegacy(id); ok {
		return target, true, nil
	}
	target, err := r.resolveSigned(ctx, id, rawURL)
	if err != nil {
		r.logger.Debug("gnews: could not open a Google News link", "err", err)
		return "", true, err
	}
	return target, true, nil
}

// resolveSigned does the two-request exchange: read the signature off the
// article page, then trade it for the URL.
func (r *Resolver) resolveSigned(ctx context.Context, id, itemLink string) (string, error) {
	page, err := r.client.Get(ctx, paramsURL(id, itemLink), true)
	if err != nil {
		return "", fmt.Errorf("gnews: article page: %w", err)
	}
	sig, err := parseSignature(page.Body)
	if err != nil {
		return "", err
	}
	body, err := buildRequest(id, sig)
	if err != nil {
		return "", fmt.Errorf("gnews: build request: %w", err)
	}
	answer, err := r.client.Post(ctx, batchexecute, contentTypeForm, body)
	if err != nil {
		return "", fmt.Errorf("gnews: decoder: %w", err)
	}
	return parseAnswer(answer.Body)
}

// contentTypeForm is what the endpoint is written against; a plain
// application/x-www-form-urlencoded is answered with an error page instead.
const contentTypeForm = "application/x-www-form-urlencoded;charset=UTF-8"
