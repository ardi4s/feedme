package main

import (
	"context"
	"sort"
	"time"

	"feedme/internal/store"
	"feedme/internal/web"
)

// storeAdmin adapts the SQLite store to the management page's interface, the
// same way storeFeedCache adapts it for reading. Keeping the adapter here holds
// the dependency arrow pointing one way: web states what it needs, cmd wires it.
type storeAdmin struct {
	st *store.Store
}

// BuiltFeeds lists every feed the server has built, newest first.
//
// The build history is the registry and the cache is only an overlay on it: a
// feed is listed from the moment it was first built and stays listed after its
// cached body expires or is pruned. Listing from the cache alone is what made a
// feed an operator had made vanish fifteen minutes later, with no way back to
// the URL they had built.
func (a storeAdmin) BuiltFeeds(ctx context.Context) ([]web.BuiltFeed, error) {
	cached, err := a.st.FeedCacheList(ctx, 200)
	if err != nil {
		return nil, err
	}
	builds, err := a.st.FeedBuildSummary(ctx, 500)
	if err != nil {
		return nil, err
	}
	now := time.Now()

	byKey := make(map[string]*web.BuiltFeed, len(builds)+len(cached))
	for _, b := range builds {
		if _, seen := byKey[b.Key]; seen {
			continue
		}
		byKey[b.Key] = &web.BuiltFeed{
			Key:       b.Key,
			SourceURL: b.SourceURL,
			ItemCount: b.ItemCount,
			Detector:  b.Detector,
			FetchedAt: b.BuiltAt,
			LastError: b.Error,
		}
	}
	for _, c := range cached {
		f, ok := byKey[c.Key]
		if !ok {
			// A stored body with no build record. It should not happen, but a
			// feed the server can serve must never be hidden by the page.
			f = &web.BuiltFeed{
				Key:       c.Key,
				SourceURL: c.SourceURL,
				ItemCount: c.ItemCount,
				Detector:  c.Detector,
				FetchedAt: c.FetchedAt,
			}
			byKey[c.Key] = f
		}
		f.Format = c.Format
		f.Cached = true
		f.ExpiresAt = c.ExpiresAt
		f.Stale = !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(now)
	}

	out := make([]web.BuiltFeed, 0, len(byKey))
	for _, f := range byKey {
		out = append(out, *f)
	}
	// Newest first, with the key breaking ties so the order is stable. This is
	// the store's own order; the page re-sorts it for display.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FetchedAt.Equal(out[j].FetchedAt) {
			return out[i].FetchedAt.After(out[j].FetchedAt)
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// Forget drops a feed completely: its stored body and its build history. The
// page lists feeds from that history, so clearing only the cache would leave
// the feed listed with nothing behind it.
func (a storeAdmin) Forget(ctx context.Context, key string) error {
	if err := a.st.FeedCacheDelete(ctx, key); err != nil {
		return err
	}
	return a.st.FeedBuildsDelete(ctx, key)
}
