package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "feedme.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Every command reopens the database, so applying the schema must be safe to
// repeat.
func TestOpenAppliesSchemaIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "feedme.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// The parent directories are created on demand.
	if _, err := Open(filepath.Join(t.TempDir(), "a", "b", "c.db")); err != nil {
		t.Fatalf("nested path: %v", err)
	}
}

func TestCreateSourceIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	src, err := s.CreateSource(ctx, "https://kompas.com/read/2026/09/14/x", "no-feed")
	if err != nil {
		t.Fatal(err)
	}
	if src.ID == 0 {
		t.Fatal("CreateSource did not return an id")
	}
	if src.Kind != "no-feed" {
		t.Errorf("kind = %q", src.Kind)
	}

	again, err := s.CreateSource(ctx, "https://kompas.com/read/2026/09/14/x", "no-feed")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != src.ID {
		t.Errorf("same URL produced a new source: %d then %d", src.ID, again.ID)
	}
	all, err := s.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d sources, want 1", len(all))
	}
}

func TestSourceByURLAndUpdate(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	src, err := s.CreateSource(ctx, "https://e.example/list", "no-feed")
	if err != nil {
		t.Fatal(err)
	}

	found, err := s.SourceByURL(ctx, "https://e.example/list")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("SourceByURL did not find the source")
	}
	missing, err := s.SourceByURL(ctx, "https://e.example/other")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Error("SourceByURL should return nil, not an error, for a miss")
	}

	src.Title = "Judul Situs"
	src.ResolvedFeedURL = "https://e.example/feed.xml"
	src.SiteHost = "e.example"
	src.LastFetchedAt = time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	got, err := s.SourceByID(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Judul Situs" || got.SiteHost != "e.example" ||
		got.ResolvedFeedURL != "https://e.example/feed.xml" {
		t.Errorf("update did not persist: %+v", got)
	}
	if got.LastFetchedAt.IsZero() {
		t.Error("LastFetchedAt was not persisted")
	}

	if err := s.DeleteSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	all, err := s.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("source was not deleted, %d remain", len(all))
	}
}

// Upserting the same URL must update the row in place. This is what stops a
// re-run from duplicating every item in a feed.
func TestUpsertItemUpdatesInPlace(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	src, err := s.CreateSource(ctx, "https://e.example/list", "no-feed")
	if err != nil {
		t.Fatal(err)
	}

	item := &Item{
		SourceID:    src.ID,
		URL:         "https://e.example/a",
		Title:       "First",
		ContentHTML: "<p>hello</p>",
		WordCount:   1,
		ContentHash: "hash-1",
		PublishedAt: time.Now().UTC().Truncate(time.Second),
	}
	inserted, err := s.UpsertItem(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Error("a newly inserted item should report a change")
	}
	if item.ID == 0 {
		t.Error("UpsertItem did not assign an id")
	}

	item.Title = "Second"
	item.ContentHash = "hash-2"
	changed, err := s.UpsertItem(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changing the content should report a change")
	}
	all, err := s.Items(ctx, src.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d items, want 1 (upsert must not duplicate)", len(all))
	}
	if all[0].Title != "Second" || all[0].ContentHash != "hash-2" {
		t.Errorf("item was not updated: %+v", all[0])
	}
	if all[0].PublishedAt.IsZero() {
		t.Error("PublishedAt was not persisted")
	}

	// An identical re-run must not report a change, so the feed is stable.
	same := &Item{
		SourceID: src.ID, URL: "https://e.example/a", Title: "Second",
		ContentHTML: "<p>hello</p>", ContentHash: "hash-2", PublishedAt: item.PublishedAt,
	}
	changed, err = s.UpsertItem(ctx, same)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("re-saving identical content should report no change")
	}
}

func TestItemsRespectLimit(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	src, err := s.CreateSource(ctx, "https://e.example/list", "no-feed")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		it := &Item{
			SourceID:    src.ID,
			URL:         "https://e.example/" + string(rune('a'+i)),
			Title:       "t",
			ContentHash: "h",
		}
		if _, err := s.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Items(ctx, src.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %d items, want 3", len(got))
	}
}

func TestMarkSeen(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	src, err := s.CreateSource(ctx, "https://e.example/list", "no-feed")
	if err != nil {
		t.Fatal(err)
	}
	// MarkSeen reports "already seen", so false means the URL is new.
	seen, err := s.MarkSeen(ctx, src.ID, "https://e.example/a")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Error("the first MarkSeen should report the URL as new")
	}
	seen, err = s.MarkSeen(ctx, src.ID, "https://e.example/a")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Error("a repeated MarkSeen should report the URL as already seen")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	now := time.Now().UTC()
	c := &CachedResponse{
		URL:          "https://e.example/page",
		Status:       200,
		ContentType:  "text/html; charset=utf-8",
		ETag:         `"abc"`,
		LastModified: "Wed, 21 Oct 2026 07:28:00 GMT",
		FinalURL:     "https://e.example/page/",
		FetchedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
		Body:         []byte("<html>cached</html>"),
	}
	if err := s.CachePut(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.CacheGet(ctx, "https://e.example/page")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("cache entry not found")
	}
	if string(got.Body) != "<html>cached</html>" {
		t.Errorf("body = %q", got.Body)
	}
	if got.ETag != `"abc"` || got.FinalURL != "https://e.example/page/" {
		t.Errorf("headers lost: %+v", got)
	}
}

func TestCacheExpiry(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	now := time.Now().UTC()
	c := &CachedResponse{
		URL:       "https://e.example/stale",
		Status:    200,
		FetchedAt: now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-time.Hour), // already expired
		Body:      []byte("<html>stale</html>"),
	}
	if err := s.CachePut(ctx, c); err != nil {
		t.Fatal(err)
	}
	// An expired entry must be reported as a miss, not served as fresh.
	got, err := s.CacheGet(ctx, "https://e.example/stale")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("an expired entry was served: %+v", got)
	}
	// CachePrune should collect it.
	n, err := s.CachePrune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CachePrune removed %d rows, want 1", n)
	}
}

func TestCachePutOverwrites(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	now := time.Now().UTC()
	for _, body := range []string{"<html>one</html>", "<html>two</html>"} {
		if err := s.CachePut(ctx, &CachedResponse{
			URL: "https://e.example/p", Status: 200,
			FetchedAt: now, ExpiresAt: now.Add(time.Hour), Body: []byte(body),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.CacheGet(ctx, "https://e.example/p")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || string(got.Body) != "<html>two</html>" {
		t.Errorf("cache was not overwritten: %+v", got)
	}
}

func TestCacheMiss(t *testing.T) {
	s := openTemp(t)
	got, err := s.CacheGet(context.Background(), "https://e.example/never")
	if err != nil {
		t.Fatalf("a miss should not be an error: %v", err)
	}
	if got != nil {
		t.Errorf("expected a miss, got %+v", got)
	}
}

// Re-polling a source must not make every item look new, or a reader shows the
// whole feed as freshly updated on every run. fetched_at may only advance when
// the stored content actually changed.
func TestUpsertDoesNotTouchFetchedAtWhenNothingChanged(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	src, err := s.CreateSource(ctx, "https://e.example/list", "no-feed")
	if err != nil {
		t.Fatal(err)
	}
	item := &Item{
		SourceID: src.ID, URL: "https://e.example/a", Title: "Stable",
		ContentHTML: "<p>body</p>", ContentHash: "h1",
	}
	if _, err := s.UpsertItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	before, err := s.Items(ctx, src.ID, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the next poll some time later returning identical content.
	time.Sleep(1100 * time.Millisecond)
	again := &Item{
		SourceID: src.ID, URL: "https://e.example/a", Title: "Stable",
		ContentHTML: "<p>body</p>", ContentHash: "h1",
	}
	changed, err := s.UpsertItem(ctx, again)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("identical content should not report a change")
	}
	after, err := s.Items(ctx, src.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !after[0].FetchedAt.Equal(before[0].FetchedAt) {
		t.Errorf("fetched_at moved on an unchanged item: %v then %v",
			before[0].FetchedAt, after[0].FetchedAt)
	}

	// A genuine content change must advance it.
	updated := &Item{
		SourceID: src.ID, URL: "https://e.example/a", Title: "Stable",
		ContentHTML: "<p>new body</p>", ContentHash: "h2",
	}
	changed, err = s.UpsertItem(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed content should report a change")
	}
}

// A page that fails to extract on one run must not wipe the good copy that is
// already stored.
func TestUpsertKeepsStoredValuesWhenExtractionIsEmpty(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	src, err := s.CreateSource(ctx, "https://e.example/list", "no-feed")
	if err != nil {
		t.Fatal(err)
	}
	good := &Item{
		SourceID: src.ID, URL: "https://e.example/a", Title: "Judul Bagus",
		Author: "Reporter", ContentHTML: "<p>isi artikel</p>",
		Excerpt: "ringkasan", Image: "https://e.example/i.jpg",
		Extractor: "semantic", WordCount: 3, ContentHash: "h1",
	}
	if _, err := s.UpsertItem(ctx, good); err != nil {
		t.Fatal(err)
	}

	// The site broke: extraction now returns nothing useful.
	empty := &Item{SourceID: src.ID, URL: "https://e.example/a", ContentHash: "h2"}
	if _, err := s.UpsertItem(ctx, empty); err != nil {
		t.Fatal(err)
	}
	got, err := s.Items(ctx, src.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	it := got[0]
	if it.Title != "Judul Bagus" {
		t.Errorf("title was wiped: %q", it.Title)
	}
	if it.Author != "Reporter" {
		t.Errorf("author was wiped: %q", it.Author)
	}
	if it.ContentHTML != "<p>isi artikel</p>" {
		t.Errorf("content was wiped: %q", it.ContentHTML)
	}
	if it.Excerpt != "ringkasan" {
		t.Errorf("excerpt was wiped: %q", it.Excerpt)
	}
	if it.WordCount != 3 {
		t.Errorf("word count was wiped: %d", it.WordCount)
	}
}
