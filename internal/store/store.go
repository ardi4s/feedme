// Package store is the SQLite persistence layer.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	// Busy timeout avoids "database is locked" under concurrent refreshes.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc's driver is safe for concurrent use, but a small pool keeps
	// write contention predictable.
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(Schema + FeedSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for callers that need their own queries.
func (s *Store) DB() *sql.DB { return s.db }

// Source is a tracked site or feed.
type Source struct {
	ID              int64
	Kind            string
	InputURL        string
	ResolvedFeedURL string
	SiteHost        string
	Title           string
	Link            string
	ETag            string
	LastModified    string
	LastFetchedAt   time.Time
	NextFetchAt     time.Time
	LastError       string
	CreatedAt       time.Time
}

// Item is one extracted article.
type Item struct {
	ID          int64
	SourceID    int64
	URL         string
	Title       string
	Author      string
	PublishedAt time.Time
	FetchedAt   time.Time
	ContentHTML string
	Excerpt     string
	Image       string
	Extractor   string
	WordCount   int
	ContentHash string
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func timeFromNull(ns sql.NullInt64) time.Time {
	if !ns.Valid {
		return time.Time{}
	}
	return time.Unix(ns.Int64, 0)
}

// CreateSource inserts a new source, returning its id. An existing source with
// the same input URL is returned unchanged.
func (s *Store) CreateSource(ctx context.Context, inputURL, kind string) (*Source, error) {
	if kind == "" {
		kind = "auto"
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sources (kind, input_url, created_at, next_fetch_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(input_url) DO NOTHING`,
		kind, inputURL, now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// Conflict: an identical source is already tracked.
		return s.SourceByURL(ctx, inputURL)
	}
	return &Source{ID: id, Kind: kind, InputURL: inputURL, CreatedAt: now}, nil
}

const sourceCols = `id, kind, input_url, COALESCE(resolved_feed_url,''), COALESCE(site_host,''),
	COALESCE(title,''), COALESCE(link,''), COALESCE(etag,''), COALESCE(last_modified,''),
	COALESCE(last_fetched_at,0), COALESCE(next_fetch_at,0), COALESCE(last_error,'')`

func scanSource(sc interface{ Scan(...any) error }) (*Source, error) {
	var src Source
	var lastFetched, nextFetch int64
	err := sc.Scan(&src.ID, &src.Kind, &src.InputURL, &src.ResolvedFeedURL, &src.SiteHost,
		&src.Title, &src.Link, &src.ETag, &src.LastModified, &lastFetched, &nextFetch, &src.LastError)
	if err != nil {
		return nil, err
	}
	src.LastFetchedAt = timeFromNull(sql.NullInt64{Int64: lastFetched, Valid: lastFetched > 0})
	src.NextFetchAt = timeFromNull(sql.NullInt64{Int64: nextFetch, Valid: nextFetch > 0})
	return &src, nil
}

// Sources lists all tracked sources.
func (s *Store) Sources(ctx context.Context) ([]*Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceCols+` FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Source
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// SourceByID looks up one source.
func (s *Store) SourceByID(ctx context.Context, id int64) (*Source, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM sources WHERE id = ?`, id)
	src, err := scanSource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return src, err
}

// SourceByURL looks up a source by its input URL.
func (s *Store) SourceByURL(ctx context.Context, u string) (*Source, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM sources WHERE input_url = ?`, u)
	src, err := scanSource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return src, err
}

// UpdateSource persists the mutable fields of a source.
func (s *Store) UpdateSource(ctx context.Context, src *Source) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sources SET kind=?, resolved_feed_url=?, site_host=?, title=?, link=?,
		 etag=?, last_modified=?, last_fetched_at=?, next_fetch_at=?, last_error=?
		 WHERE id=?`,
		src.Kind, src.ResolvedFeedURL, src.SiteHost, src.Title, src.Link,
		src.ETag, src.LastModified, nullTime(src.LastFetchedAt), nullTime(src.NextFetchAt),
		src.LastError, src.ID)
	return err
}

// DeleteSource removes a source and, by cascade, its items.
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id)
	return err
}

// UpsertItem inserts an item, or updates it when the URL already exists for this
// source. It reports whether the row is new.
// UpsertItem stores an item, inserting it if the URL is new for the source and
// updating it otherwise. It reports whether the stored content changed.
//
// Re-polling a source must not make every item look new, so fetched_at is only
// advanced when something actually changed. Values that the new extraction
// left empty do not overwrite what is already stored: a page that fails to
// extract on one run must not wipe a good copy.
func (s *Store) UpsertItem(ctx context.Context, it *Item) (bool, error) {
	now := time.Now()
	var (
		existing     int64
		existingHash string
		existingHTML string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(content_hash,''), COALESCE(content_html,'') FROM items
		 WHERE source_id = ? AND url = ?`, it.SourceID, it.URL).
		Scan(&existing, &existingHash, &existingHTML)
	switch {
	case err == nil:
		if existingHash == it.ContentHash && existingHTML == it.ContentHTML &&
			existingHash != "" {
			// Nothing new to say: leave fetched_at alone.
			it.ID = existing
			return false, nil
		}
		_, err = s.db.ExecContext(ctx,
			`UPDATE items SET
				title=CASE WHEN ?='' THEN title ELSE ? END,
			 author=CASE WHEN ?='' THEN author ELSE ? END,
				published_at=COALESCE(?, published_at),
				fetched_at=?,
				content_html=CASE WHEN ?='' THEN content_html ELSE ? END,
				excerpt=CASE WHEN ?='' THEN excerpt ELSE ? END,
				image=CASE WHEN ?='' THEN image ELSE ? END,
				extractor=CASE WHEN ?='' THEN extractor ELSE ? END,
				word_count=CASE WHEN ?=0 THEN word_count ELSE ? END,
				content_hash=CASE WHEN ?='' THEN content_hash ELSE ? END
			 WHERE id=?`,
			it.Title, it.Title,
			it.Author, it.Author,
			nullTime(it.PublishedAt), now.Unix(),
			it.ContentHTML, it.ContentHTML,
			it.Excerpt, it.Excerpt,
			it.Image, it.Image,
			it.Extractor, it.Extractor,
			it.WordCount, it.WordCount,
			it.ContentHash, it.ContentHash,
			existing)
		if err != nil {
			return false, err
		}
		it.ID = existing
		return true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO items (source_id, url, title, author, published_at, fetched_at,
		 content_html, excerpt, image, extractor, word_count, content_hash)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.SourceID, it.URL, it.Title, it.Author, nullTime(it.PublishedAt), now.Unix(),
		it.ContentHTML, it.Excerpt, it.Image, it.Extractor, it.WordCount, it.ContentHash)
	if err != nil {
		return false, err
	}
	it.ID, _ = res.LastInsertId()
	return true, nil
}

const itemCols = `id, source_id, url, COALESCE(title,''), COALESCE(author,''),
	COALESCE(published_at,0), COALESCE(fetched_at,0), COALESCE(content_html,''),
	COALESCE(excerpt,''), COALESCE(image,''), COALESCE(extractor,''), COALESCE(word_count,0),
	COALESCE(content_hash,'')`

func scanItem(sc interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var pub, fetched int64
	err := sc.Scan(&it.ID, &it.SourceID, &it.URL, &it.Title, &it.Author, &pub, &fetched,
		&it.ContentHTML, &it.Excerpt, &it.Image, &it.Extractor, &it.WordCount, &it.ContentHash)
	if err != nil {
		return nil, err
	}
	it.PublishedAt = timeFromNull(sql.NullInt64{Int64: pub, Valid: pub > 0})
	it.FetchedAt = timeFromNull(sql.NullInt64{Int64: fetched, Valid: fetched > 0})
	return &it, nil
}

// Items lists a source's stored items, newest first.
func (s *Store) Items(ctx context.Context, sourceID int64, limit int) ([]*Item, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+itemCols+` FROM items WHERE source_id = ?
		 ORDER BY COALESCE(published_at, fetched_at) DESC, id DESC LIMIT ?`,
		sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkSeen records a URL as visited for a source. It reports whether the URL
// had already been seen, which is how pagination detects that it has wrapped
// around into known territory.
// MarkSeen records a URL as seen for a source. It reports whether the URL had
// already been recorded, so the true value means "not new".
func (s *Store) MarkSeen(ctx context.Context, sourceID int64, url string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO seen_urls (source_id, url, first_seen_at) VALUES (?,?,?)
		 ON CONFLICT(source_id, url) DO NOTHING`,
		sourceID, url, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 0, nil
}

// CachedResponse is a stored HTTP response.
type CachedResponse struct {
	URL          string
	Status       int
	ETag         string
	LastModified string
	FinalURL     string
	ContentType  string
	FetchedAt    time.Time
	ExpiresAt    time.Time
	Body         []byte
}

// CacheGet returns a cached response that has not expired. Expired rows are
// treated as a miss so a stale body is never served, and they are left for
// CachePrune to collect.
func (s *Store) CacheGet(ctx context.Context, url string) (*CachedResponse, error) {
	var c CachedResponse
	var fetched, expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT url, status, COALESCE(etag,''), COALESCE(last_modified,''), COALESCE(final_url,''),
		 COALESCE(content_type,''), COALESCE(fetched_at,0), COALESCE(expires_at,0), body
		 FROM http_cache WHERE url = ? AND (expires_at = 0 OR expires_at >= ?)`,
		url, time.Now().Unix()).
		Scan(&c.URL, &c.Status, &c.ETag, &c.LastModified, &c.FinalURL, &c.ContentType,
			&fetched, &expires, &c.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.FetchedAt = timeFromNull(sql.NullInt64{Int64: fetched, Valid: fetched > 0})
	c.ExpiresAt = timeFromNull(sql.NullInt64{Int64: expires, Valid: expires > 0})
	return &c, nil
}

// CachePut stores a response.
func (s *Store) CachePut(ctx context.Context, c *CachedResponse) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO http_cache (url, status, etag, last_modified, final_url, content_type,
		 fetched_at, expires_at, body) VALUES (?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(url) DO UPDATE SET
			status=excluded.status, etag=excluded.etag, last_modified=excluded.last_modified,
			final_url=excluded.final_url, content_type=excluded.content_type,
			fetched_at=excluded.fetched_at, expires_at=excluded.expires_at, body=excluded.body`,
		c.URL, c.Status, c.ETag, c.LastModified, c.FinalURL, c.ContentType,
		c.FetchedAt.Unix(), c.ExpiresAt.Unix(), c.Body)
	return err
}

// CachePrune deletes expired cache rows and reports how many went away.
func (s *Store) CachePrune(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM http_cache WHERE expires_at < ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
