package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"feedme/internal/store"
)

func testAdminStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A feed that was built stays on the page after its cached body goes away. This
// is the point of listing from the build history: a link an operator made must
// not disappear because a fifteen-minute cache entry expired.
func TestBuiltFeedsKeepsAFeedAfterItsCacheExpires(t *testing.T) {
	st := testAdminStore(t)
	ctx := context.Background()
	now := time.Now()
	key := "https://h/extract?url=https%3A%2F%2Fe.example%2Fnews"

	if err := st.RecordBuild(ctx, key, "https://e.example/news", true, 7, "article.card", "high", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.FeedCachePut(ctx, &store.FeedCache{
		Key: key, SourceURL: "https://e.example/news", Format: "rss", Status: 200,
		ItemCount: 7, Body: []byte("x"), FetchedAt: now, ExpiresAt: now.Add(time.Hour),
	}, nil); err != nil {
		t.Fatal(err)
	}

	a := storeAdmin{st: st}
	got, err := a.BuiltFeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("feeds = %d, want 1", len(got))
	}
	if !got[0].Cached || got[0].Stale {
		t.Errorf("feed = %+v, want a fresh cached feed", got[0])
	}

	// The cache entry goes away the way the hourly prune removes it.
	if err := st.FeedCacheDelete(ctx, key); err != nil {
		t.Fatal(err)
	}
	got, err = a.BuiltFeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("feeds after the cache went = %d, want the feed still listed", len(got))
	}
	if got[0].Cached {
		t.Error("the feed still claims a cached body")
	}
	if got[0].ItemCount != 7 || got[0].Detector != "article.card" {
		t.Errorf("feed = %+v, want the last build's details", got[0])
	}
	if label, _ := got[0].State(); label != "not cached" {
		t.Errorf("state = %q, want not cached", label)
	}
}

// Forget has to clear the history the page lists from, or the feed comes back
// the next time the page is drawn.
func TestForgetRemovesAFeedFromTheList(t *testing.T) {
	st := testAdminStore(t)
	ctx := context.Background()
	key := "https://h/extract?url=https%3A%2F%2Fe.example%2Fnews"
	if err := st.RecordBuild(ctx, key, "https://e.example/news", true, 7, "", "", ""); err != nil {
		t.Fatal(err)
	}

	a := storeAdmin{st: st}
	if err := a.Forget(ctx, key); err != nil {
		t.Fatal(err)
	}
	got, err := a.BuiltFeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("feeds = %d, want none after a forget", len(got))
	}
}

// A failed build is still a feed that exists, and the page has to say why it is
// not producing anything.
func TestBuiltFeedsReportsAFailedBuild(t *testing.T) {
	st := testAdminStore(t)
	ctx := context.Background()
	key := "https://h/extract?url=https%3A%2F%2Fe.example%2Fbad"
	if err := st.RecordBuild(ctx, key, "https://e.example/bad", false, 0, "", "", "listpage: no items found"); err != nil {
		t.Fatal(err)
	}

	got, err := storeAdmin{st: st}.BuiltFeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("feeds = %d, want 1", len(got))
	}
	if label, class := got[0].State(); label != "failed" || class != "err" {
		t.Errorf("state = %q/%q, want failed/err", label, class)
	}
	if got[0].LastError != "listpage: no items found" {
		t.Errorf("LastError = %q", got[0].LastError)
	}
}
