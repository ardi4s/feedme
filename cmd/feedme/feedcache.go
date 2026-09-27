package main

import (
	"context"
	"net/url"
	"time"

	"feedme/internal/store"
	"feedme/internal/web"
)

// storeFeedCache adapts the SQLite store to the interface the web layer wants.
//
// The adapter lives here rather than in internal/web so that the HTTP layer
// keeps no dependency on the storage package: web states what it needs, and the
// command that owns the database says how it is met.
type storeFeedCache struct {
	st *store.Store
}

func (c storeFeedCache) Get(ctx context.Context, key string) (*web.CachedFeed, bool) {
	entry, err := c.st.FeedCacheGet(ctx, key)
	if err != nil || entry == nil {
		return nil, false
	}
	return &web.CachedFeed{
		Body:        entry.Body,
		ETag:        entry.ETag,
		ContentType: entry.Format,
		ItemCount:   entry.ItemCount,
		Detector:    entry.Detector,
		Confidence:  entry.Confidence,
		FetchedAt:   entry.FetchedAt,
	}, true
}

func (c storeFeedCache) Put(ctx context.Context, key string, f *web.CachedFeed, items []web.CachedItem, lifetime time.Duration) error {
	// The caller's build time is kept, not replaced with the write time, so the
	// Last-Modified header a cache hit reports matches the one the build sent.
	builtAt := f.FetchedAt
	if builtAt.IsZero() {
		builtAt = time.Now()
	}
	row := &store.FeedCache{
		Key:        key,
		SourceURL:  sourceURLOf(key),
		Format:     f.ContentType,
		ETag:       f.ETag,
		ItemCount:  f.ItemCount,
		Detector:   f.Detector,
		Confidence: f.Confidence,
		Status:     f.PageStatus,
		Body:       f.Body,
		FetchedAt:  builtAt,
		ExpiresAt:  builtAt.Add(lifetime),
	}
	rows := make([]store.FeedItem, 0, len(items))
	for i, it := range items {
		rows = append(rows, store.FeedItem{
			Position:    i,
			URL:         it.URL,
			Title:       it.Title,
			Author:      it.Author,
			PublishedAt: it.PublishedAt,
			Summary:     it.Summary,
			ContentHTML: it.ContentHTML,
			Image:       it.Image,
		})
	}
	return c.st.FeedCachePut(ctx, row, rows)
}

// sourceURLOf pulls the page URL out of a canonical feed URL, for the row's
// benefit when someone reads the database by hand.
func sourceURLOf(key string) string {
	u, err := url.Parse(key)
	if err != nil {
		return ""
	}
	return u.Query().Get("url")
}

// storeRecorder notes every build, so that a feed which quietly stopped
// producing items can be explained after the fact.
type storeRecorder struct {
	st *store.Store
}

func (rec storeRecorder) Record(ctx context.Context, key, sourceURL string, ok bool, itemCount int, selector, confidence, errMsg string) error {
	return rec.st.RecordBuild(ctx, key, sourceURL, ok, itemCount, selector, confidence, errMsg)
}
