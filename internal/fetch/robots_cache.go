package fetch

import (
	"context"
	"net/url"
	"sync"
	"time"
)

// RobotsCache fetches and caches robots.txt per host.
type RobotsCache struct {
	client *Client
	ttl    time.Duration

	mu     sync.RWMutex
	byHost map[string]*robotsEntry
	// inflight collapses concurrent lookups for the same host onto one request.
	inflight map[string]*sync.Once
}

type robotsEntry struct {
	robots    *Robots
	fetchedAt time.Time
}

// NewRobotsCache builds a cache. ttl of 0 means 24 hours.
func NewRobotsCache(c *Client, ttl time.Duration) *RobotsCache {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &RobotsCache{
		client:   c,
		ttl:      ttl,
		byHost:   map[string]*robotsEntry{},
		inflight: map[string]*sync.Once{},
	}
}

// hostKey builds the robots.txt origin for a URL.
func robotsOrigin(u *url.URL) string {
	scheme := u.Scheme
	if scheme == "" {
		scheme = "http"
	}
	host := u.Hostname()
	return scheme + "://" + host + "/robots.txt"
}

// Allowed reports whether u may be fetched, loading robots.txt if needed.
func (rc *RobotsCache) Allowed(ctx context.Context, u *url.URL) (bool, error) {
	r, err := rc.Get(ctx, u)
	if err != nil || r == nil {
		// If robots.txt itself cannot be read, do not block the fetch. The
		// spec's usual reading is that unreachable robots means no rules.
		return true, nil
	}
	return r.Allowed(u.Path), nil
}

// Get returns the parsed robots.txt for a URL's host, loading it if stale.
func (rc *RobotsCache) Get(ctx context.Context, u *url.URL) (*Robots, error) {
	host := u.Hostname()
	rc.mu.RLock()
	entry, ok := rc.byHost[host]
	rc.mu.RUnlock()
	if ok && time.Since(entry.fetchedAt) < rc.ttl {
		return entry.robots, nil
	}

	rc.mu.Lock()
	once, running := rc.inflight[host]
	if !running {
		once = &sync.Once{}
		rc.inflight[host] = once
	}
	rc.mu.Unlock()

	once.Do(func() {
		resp, err := rc.client.get(ctx, robotsOrigin(u), false, true)
		var parsed *Robots
		if err != nil {
			parsed = &Robots{Found: false}
		} else {
			body, derr := DecodeBody(resp.ContentType, resp.Body)
			if derr != nil {
				body = resp.Body
			}
			parsed = ParseRobots(string(body))
		}
		rc.mu.Lock()
		rc.byHost[host] = &robotsEntry{robots: parsed, fetchedAt: time.Now()}
		delete(rc.inflight, host)
		rc.mu.Unlock()
	})

	rc.mu.RLock()
	entry, ok = rc.byHost[host]
	rc.mu.RUnlock()
	if !ok {
		return &Robots{}, nil
	}
	return entry.robots, nil
}

// Sitemaps returns the Sitemap: URLs declared for a host's robots.txt.
func (rc *RobotsCache) Sitemaps(ctx context.Context, u *url.URL) []string {
	if rc == nil {
		return nil
	}
	r, err := rc.Get(ctx, u)
	if err != nil || r == nil {
		return nil
	}
	return r.Sitemaps
}
