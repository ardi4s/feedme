package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// FeedCache is a rendered feed, kept so that a reader's conditional request can
// be answered without building the feed again.
//
// The row is keyed by the canonical feed query, not by the page URL: the same
// page rendered with different options is a different feed and must not share a
// cache entry.
type FeedCache struct {
	// Key is the canonical form of the feed's request parameters.
	Key string
	// SourceURL is the page the feed was built from.
	SourceURL string
	// Format is the serialisation that Body holds.
	Format string
	// Status is the listing page's HTTP status at build time.
	Status int
	// ETag identifies Body.
	ETag string
	// ItemCount is how many entries the build produced.
	ItemCount int
	// Detector is the selector auto-detection chose, empty when the request
	// named one.
	Detector string
	// Confidence is the detection's confidence, empty when configured.
	Confidence string
	// Body is the rendered document.
	Body []byte
	// FetchedAt is when the build ran.
	FetchedAt time.Time
	// ExpiresAt is when the entry stops being usable without a rebuild.
	ExpiresAt time.Time
	// LastError records why the last rebuild failed, if it did.
	LastError string
}

// FeedItem is one entry of a built feed.
type FeedItem struct {
	Position    int
	URL         string
	Title       string
	Author      string
	PublishedAt time.Time
	Summary     string
	ContentHTML string
	Image       string
}

// FeedCacheGet returns a still-valid rendered feed, or nil when there is none.
//
// A stale entry is not returned. Serving it would make a dead site look alive,
// and a reader that keeps a subscription to a broken feed stops noticing that
// it is broken.
func (s *Store) FeedCacheGet(ctx context.Context, key string) (*FeedCache, error) {
	var (
		c                  FeedCache
		fetched, expires   int64
		status, itemCount  int
		detector, confiden string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT feed_key, source_url, format, status, COALESCE(etag,''), item_count,
		 COALESCE(detector,''), COALESCE(confidence,''), body,
		 COALESCE(fetched_at,0), COALESCE(expires_at,0), COALESCE(last_error,'')
		 FROM feed_cache WHERE feed_key = ? AND expires_at >= ?`,
		key, time.Now().Unix()).
		Scan(&c.Key, &c.SourceURL, &c.Format, &status, &c.ETag, &itemCount,
			&detector, &confiden, &c.Body, &fetched, &expires, &c.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Status = status
	c.ItemCount = itemCount
	c.Detector = detector
	c.Confidence = confiden
	c.FetchedAt = timeFromNull(sql.NullInt64{Int64: fetched, Valid: fetched > 0})
	c.ExpiresAt = timeFromNull(sql.NullInt64{Int64: expires, Valid: expires > 0})
	return &c, nil
}

// FeedCachePut stores a rendered feed and replaces its item list.
func (s *Store) FeedCachePut(ctx context.Context, c *FeedCache, items []FeedItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO feed_cache (feed_key, source_url, format, status, etag, item_count,
		 detector, confidence, body, fetched_at, expires_at, last_error)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,'')
		 ON CONFLICT(feed_key) DO UPDATE SET
			source_url=excluded.source_url, format=excluded.format, status=excluded.status,
			etag=excluded.etag, item_count=excluded.item_count, detector=excluded.detector,
			confidence=excluded.confidence, body=excluded.body,
			fetched_at=excluded.fetched_at, expires_at=excluded.expires_at, last_error=''`,
		c.Key, c.SourceURL, c.Format, c.Status, c.ETag, c.ItemCount,
		c.Detector, c.Confidence, c.Body,
		c.FetchedAt.Unix(), c.ExpiresAt.Unix()); err != nil {
		return err
	}
	// The item list is replaced wholesale rather than merged. A feed is a
	// statement about the current state of a page, and a reader that still saw
	// yesterday's items after they fell off the page would be reading a lie.
	if _, err := tx.ExecContext(ctx, `DELETE FROM feed_items WHERE feed_key = ?`, c.Key); err != nil {
		return err
	}
	for _, it := range items {
		var published any
		if !it.PublishedAt.IsZero() {
			published = it.PublishedAt.Unix()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO feed_items (feed_key, position, url, title, author,
			 published_at, summary, content_html, image) VALUES (?,?,?,?,?,?,?,?,?)`,
			c.Key, it.Position, it.URL, it.Title, it.Author, published,
			it.Summary, it.ContentHTML, it.Image); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordBuild appends to the build history of a feed.
//
// A failed build is recorded too. When a feed that used to have forty items
// suddenly has none, the question is always "what changed", and the answer is in
// this table.
func (s *Store) RecordBuild(ctx context.Context, key, sourceURL string, ok bool, itemCount int, selector, confidence, buildErr string) error {
	flag := 0
	if ok {
		flag = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feed_builds (feed_key, source_url, ok, item_count, selector, confidence, error, built_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		key, sourceURL, flag, itemCount, selector, confidence, buildErr, time.Now().Unix())
	return err
}

// FeedBuilds returns the most recent builds of a feed, newest first.
func (s *Store) FeedBuilds(ctx context.Context, key string, limit int) ([]FeedBuild, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source_url, ok, item_count, COALESCE(selector,''), COALESCE(confidence,''),
		 COALESCE(error,''), COALESCE(built_at,0)
		 FROM feed_builds WHERE feed_key = ? ORDER BY built_at DESC, id DESC LIMIT ?`,
		key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedBuild
	for rows.Next() {
		var b FeedBuild
		var built int64
		if err := rows.Scan(&b.ID, &b.SourceURL, &b.OK, &b.ItemCount, &b.Selector,
			&b.Confidence, &b.Error, &built); err != nil {
			return nil, err
		}
		b.BuiltAt = timeFromNull(sql.NullInt64{Int64: built, Valid: built > 0})
		out = append(out, b)
	}
	return out, rows.Err()
}

// FeedBuild is one entry in a feed's build history.
type FeedBuild struct {
	ID         int64
	SourceURL  string
	OK         int
	ItemCount  int
	Selector   string
	Confidence string
	Error      string
	BuiltAt    time.Time
}

// FeedCacheList returns every stored feed, newest first, for the management
// page. Expired rows are included on purpose: a feed that stopped being
// refreshed is exactly what an operator needs to see.
func (s *Store) FeedCacheList(ctx context.Context, limit int) ([]*FeedCache, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT feed_key, source_url, format, status, COALESCE(etag,''), item_count,
		 COALESCE(detector,''), COALESCE(confidence,''), length(body),
		 COALESCE(fetched_at,0), COALESCE(expires_at,0), COALESCE(last_error,'')
		 FROM feed_cache ORDER BY fetched_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*FeedCache
	for rows.Next() {
		var (
			c                FeedCache
			size             int
			fetched, expires int64
		)
		if err := rows.Scan(&c.Key, &c.SourceURL, &c.Format, &c.Status, &c.ETag, &c.ItemCount,
			&c.Detector, &c.Confidence, &size, &fetched, &expires, &c.LastError); err != nil {
			return nil, err
		}
		c.FetchedAt = timeFromNull(sql.NullInt64{Int64: fetched, Valid: fetched > 0})
		c.ExpiresAt = timeFromNull(sql.NullInt64{Int64: expires, Valid: expires > 0})
		out = append(out, &c)
	}
	return out, rows.Err()
}

// FeedCacheDelete forgets a cached feed and its stored items, so the next
// request rebuilds from the source.
func (s *Store) FeedCacheDelete(ctx context.Context, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM feed_items WHERE feed_key = ?`, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM feed_cache WHERE feed_key = ?`, key); err != nil {
		return err
	}
	return tx.Commit()
}

// FeedCachePrune deletes expired feed bodies, sweeps the item lists that no
// body points at, and reports how many bodies went.
//
// The sweep is by orphan, not only by the rows just expired, and that is what
// makes it self-healing: an item list whose cache row was pruned in an earlier
// pass has no reader either, and no expiry test will ever find it again. Items
// are the innards of a cached rendering, so once the rendering is gone all they
// do is grow the file.
func (s *Store) FeedCachePrune(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM feed_cache WHERE expires_at < ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM feed_items
		 WHERE feed_key NOT IN (SELECT feed_key FROM feed_cache)`); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// FeedBuildSummary is the most recent build of one feed, which is what the
// management page lists a feed from.
type FeedBuildSummary struct {
	// Key is the canonical form of the feed's request parameters.
	Key string
	// SourceURL is the page the feed was built from.
	SourceURL string
	// OK reports whether the most recent build succeeded.
	OK bool
	// ItemCount is how many entries that build produced.
	ItemCount int
	// Detector is the selector the build used, empty when it named none.
	Detector string
	// Confidence is the detection's confidence, empty when configured.
	Confidence string
	// Error explains a failed build, empty when it succeeded.
	Error string
	// BuiltAt is when that build ran.
	BuiltAt time.Time

	// Health fields (computed from build history)
	// FailureStreak is consecutive failed builds at the end of history.
	FailureStreak int
	// TotalRecent is number of recent builds checked for streak (max 50).
	TotalRecent int
	// LastSuccess is when the feed last built successfully, zero if never.
	LastSuccess time.Time
	// AvgBuildMs is average build duration in milliseconds over recent builds.
	AvgBuildMs int
}

// FeedBuildSummary returns the most recent build of every feed, newest first.
//
// This is the registry the management page reads. A feed stays here once it has
// been built, whether or not a cached body still exists for it, so a link an
// operator made once can always be found again instead of vanishing with the
// cache it happened to be stored in.
//
// The row is picked by id, not by timestamp: ids are assigned in insert order,
// so the highest id of a key is its newest build and ties on built_at cannot
// make the choice depend on the query planner.
func (s *Store) FeedBuildSummary(ctx context.Context, limit int) ([]FeedBuildSummary, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT b.feed_key, b.source_url, b.ok, b.item_count,
		 COALESCE(b.selector,''), COALESCE(b.confidence,''), COALESCE(b.error,''), b.built_at
		 FROM feed_builds b
		 JOIN (SELECT feed_key, MAX(id) AS id FROM feed_builds GROUP BY feed_key) newest
		   ON newest.id = b.id
		 ORDER BY b.built_at DESC, b.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedBuildSummary
	for rows.Next() {
		var (
			b     FeedBuildSummary
			ok    int
			built int64
		)
		if err := rows.Scan(&b.Key, &b.SourceURL, &ok, &b.ItemCount,
			&b.Detector, &b.Confidence, &b.Error, &built); err != nil {
			return nil, err
		}
		b.OK = ok != 0
		b.BuiltAt = timeFromNull(sql.NullInt64{Int64: built, Valid: built > 0})
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Enrich with health data
	for i := range out {
		streak, total, lastSucc, avgMs, err := s.FeedHealth(ctx, out[i].Key)
		if err != nil {
			// Ignore health errors, don't fail the whole request
			out[i].FailureStreak = 0
			out[i].TotalRecent = 0
			out[i].LastSuccess = time.Time{}
			out[i].AvgBuildMs = 0
		} else {
			out[i].FailureStreak = streak
			out[i].TotalRecent = total
			out[i].LastSuccess = lastSucc
			out[i].AvgBuildMs = avgMs
		}
	}
	return out, nil
}

// FeedBuildsDelete removes a feed's build history.
//
// The management page lists feeds from that history, so this is what makes
// "Forget" actually forget: clearing the cache alone would leave the feed on
// the page with nothing behind it.
func (s *Store) FeedBuildsDelete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM feed_builds WHERE feed_key = ?`, key)
	return err
}

// FeedBuildsPrune caps the build history, keeping the newest keep rows of each
// feed and dropping the rest.
//
// The newest row of every key always survives: the management page lists feeds
// from this table, so pruning a feed down to nothing would make it disappear
// from the page, which is the same complaint the history exists to fix.
func (s *Store) FeedBuildsPrune(ctx context.Context, keep int) (int64, error) {
	if keep < 1 {
		keep = 1
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM feed_builds WHERE id IN (
		   SELECT id FROM (
		     SELECT id, ROW_NUMBER() OVER (
		       PARTITION BY feed_key ORDER BY built_at DESC, id DESC
		     ) AS rank
		     FROM feed_builds
		   ) WHERE rank > ?
		 )`, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FeedHealth computes health metrics for a feed from its build history.
// It examines up to 50 most recent builds.
func (s *Store) FeedHealth(ctx context.Context, feedKey string) (failureStreak int, totalRecent int, lastSuccess time.Time, avgBuildMs int, err error) {
	const maxRecent = 50
	rows, err := s.db.QueryContext(ctx,
		`SELECT ok, built_at FROM feed_builds
		 WHERE feed_key = ?
		 ORDER BY built_at DESC, id DESC
		 LIMIT ?`, feedKey, maxRecent)
	if err != nil {
		return 0, 0, time.Time{}, 0, err
	}
	defer rows.Close()

	streak := 0
	totalRecentCount := 0
	inStreak := true

	for rows.Next() {
		var ok int
		var built int64
		if err := rows.Scan(&ok, &built); err != nil {
			return 0, 0, time.Time{}, 0, err
		}
		totalRecentCount++
		if ok != 0 {
			if inStreak {
				inStreak = false
			}
			if lastSuccess.IsZero() {
				lastSuccess = time.Unix(built, 0)
			}
		} else if inStreak {
			streak++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, time.Time{}, 0, err
	}

	var avgMs int

	// We need a second query to get build durations
	drows, err := s.db.QueryContext(ctx,
		`SELECT built_at FROM feed_builds
		 WHERE feed_key = ? AND ok = 1
		 ORDER BY built_at DESC, id DESC
		 LIMIT ?`, feedKey, maxRecent)
	if err == nil {
		var durations []int64
		var prev time.Time
		for drows.Next() {
			var built int64
			if err := drows.Scan(&built); err != nil {
				break
			}
			t := time.Unix(built, 0)
			if !prev.IsZero() {
				durations = append(durations, prev.Sub(t).Milliseconds())
			}
			prev = t
		}
		drows.Close()
		if len(durations) > 0 {
			var sum int64
			for _, d := range durations {
				sum += d
			}
			avgMs = int(sum / int64(len(durations)))
		}
	}

	return streak, totalRecentCount, lastSuccess, avgMs, nil
}
