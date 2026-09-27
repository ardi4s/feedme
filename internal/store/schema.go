package store

// Schema is applied on every open; each statement is idempotent.
const Schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sources (
    id                INTEGER PRIMARY KEY,
    kind              TEXT    NOT NULL DEFAULT 'auto',
    input_url         TEXT    NOT NULL UNIQUE,
    resolved_feed_url TEXT,
    site_host         TEXT,
    title             TEXT,
    link              TEXT,
    etag              TEXT,
    last_modified     TEXT,
    last_fetched_at   INTEGER,
    next_fetch_at     INTEGER,
    last_error        TEXT,
    created_at        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS items (
    id           INTEGER PRIMARY KEY,
    source_id    INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    url          TEXT    NOT NULL,
    title        TEXT,
    author       TEXT,
    published_at INTEGER,
    fetched_at   INTEGER,
    content_html TEXT,
    excerpt      TEXT,
    image        TEXT,
    extractor    TEXT,
    word_count   INTEGER,
    content_hash TEXT,
    UNIQUE (source_id, url)
);

CREATE INDEX IF NOT EXISTS items_source_pub ON items (source_id, published_at DESC);
CREATE INDEX IF NOT EXISTS items_published ON items (published_at DESC);

-- URLs we have already walked past. Used to stop pagination when discovery
-- re-enters territory it has already covered.
CREATE TABLE IF NOT EXISTS seen_urls (
    source_id    INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    url          TEXT    NOT NULL,
    first_seen_at INTEGER NOT NULL,
    PRIMARY KEY (source_id, url)
);

CREATE TABLE IF NOT EXISTS http_cache (
    url           TEXT    PRIMARY KEY,
    status        INTEGER NOT NULL,
    etag          TEXT,
    last_modified TEXT,
    final_url     TEXT,
    content_type  TEXT,
    fetched_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    body          BLOB
);

CREATE INDEX IF NOT EXISTS http_cache_expiry ON http_cache (expires_at);
`

// FeedSchema holds the tables that back a stateless feed URL.
//
// The design decision worth stating: a feed is identified by its parameters,
// not by a row. There is no feeds table holding a canonical spec, because the
// spec is the URL and the URL is the only thing a user ever has to keep. What
// is worth persisting is the work: which page a feed was built from, when, and
// the rendered bytes, so a reader's conditional request can be answered without
// touching the source site again.
const FeedSchema = `
-- One row per feed URL that has been built, keyed by the canonical query.
-- key is the identity; it is not a foreign key target for anything that the
-- user must maintain by hand.
CREATE TABLE IF NOT EXISTS feed_cache (
    feed_key      TEXT    PRIMARY KEY,
    source_url    TEXT    NOT NULL,
    format        TEXT    NOT NULL,
    status        INTEGER NOT NULL,
    etag          TEXT,
    item_count    INTEGER NOT NULL DEFAULT 0,
    detector      TEXT,
    confidence    TEXT,
    body          BLOB    NOT NULL,
    fetched_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    last_error    TEXT
);

CREATE INDEX IF NOT EXISTS feed_cache_expiry ON feed_cache (expires_at);

-- The items behind the most recent build of each feed, kept so that a reader
-- can be told what is new without re-parsing the page, and so that a build can
-- be inspected after the fact.
CREATE TABLE IF NOT EXISTS feed_items (
    feed_key     TEXT    NOT NULL,
    position     INTEGER NOT NULL,
    url          TEXT    NOT NULL,
    title        TEXT,
    author       TEXT,
    published_at INTEGER,
    summary      TEXT,
    content_html TEXT,
    image        TEXT,
    PRIMARY KEY (feed_key, url)
);

CREATE INDEX IF NOT EXISTS feed_items_order ON feed_items (feed_key, position);

-- One row per build attempt. This is both the diagnostic trail (the detection
-- result is the part worth remembering: if auto-detection later breaks, this is
-- the evidence of what it used to choose) and, through its newest row per feed,
-- the registry of feeds that exist. A feed is never deleted from here when its
-- cache expires, so a link that was built once can always be listed again.
CREATE TABLE IF NOT EXISTS feed_builds (
    id          INTEGER PRIMARY KEY,
    feed_key    TEXT    NOT NULL,
    source_url  TEXT    NOT NULL,
    ok          INTEGER NOT NULL,
    item_count  INTEGER NOT NULL DEFAULT 0,
    selector    TEXT,
    confidence  TEXT,
    error       TEXT,
    built_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS feed_builds_key ON feed_builds (feed_key, built_at DESC);

-- feed_subs held the opt-in refresh schedule. A feed is now rebuilt when a
-- reader asks for it, and the cache lifetime is the only thing that paces that,
-- so there is no schedule left to keep. The table is dropped rather than left
-- in place, because an unused table is a second source of truth waiting to
-- disagree with the first. The statement is idempotent and runs on every open.
DROP TABLE IF EXISTS feed_subs;
`
