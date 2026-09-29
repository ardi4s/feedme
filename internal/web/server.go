// Package web serves feeds over HTTP.
//
// The handler is deliberately thin. Everything that decides what is in a feed
// lives in internal/pipeline, and everything that decides what is in a request
// lives in internal/feedurl. What remains here is the part that is genuinely
// about HTTP: mapping a request onto a spec, picking a status code, and setting
// cache headers that a reader can actually use.
package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"feedme/internal/feed"
	"feedme/internal/feedurl"
	"feedme/internal/fetch"
	"feedme/internal/filter"
	"feedme/internal/listpage"
	"feedme/internal/pipeline"
	"feedme/internal/render"
)

// Options configures a Server.
type Options struct {
	// Pipeline builds the feed. Required.
	Pipeline *pipeline.Pipeline
	// Logger receives one line per request. Nil discards them.
	Logger *slog.Logger
	// Now supplies the time, so a test can pin it.
	Now func() time.Time
	// Cache keeps rendered feeds between requests. Nil disables it, which is
	// correct for a test and wrong for a server.
	Cache FeedCache
	// Recorder notes every build, successful or not. Nil disables it.
	Recorder BuildRecorder
	// Admin backs the /feeds management page. Nil hides the page.
	Admin FeedAdmin
	// ManageToken, when set, is the password the management page asks for. A
	// browser signs in with it once and is handed a session cookie; a script
	// can send it as a bearer token or as the password of a Basic pair. Empty
	// leaves the page open, which is what a single-user instance on localhost
	// wants.
	ManageToken string
}

// CachedFeed is a rendered feed kept between requests.
type CachedFeed struct {
	// Body is the rendered document.
	Body []byte
	// ETag identifies Body.
	ETag string
	// ContentType is the media type of Body.
	ContentType string
	// ItemCount is how many entries the build produced.
	ItemCount int
	// Detector is the selector auto-detection chose, empty when configured.
	Detector string
	// Confidence is the detection's confidence, empty when configured.
	Confidence string
	// PageStatus is the HTTP status of the listing page the feed was built from.
	PageStatus int
	// FetchedAt is when the feed was built.
	FetchedAt time.Time
}

// CachedItem is one entry of a cached feed. It is stored so that a build can be
// inspected after the fact without keeping the rendered document's meaning
// hidden inside it.
type CachedItem struct {
	URL         string
	Title       string
	Author      string
	PublishedAt time.Time
	Summary     string
	ContentHTML string
	Image       string
}

// FeedCache stores rendered feeds between requests.
//
// It is an interface because web must not depend on the storage package. The
// identity of a feed is its canonical URL, which the caller supplies as the key.
type FeedCache interface {
	// Get returns a still-valid feed, or false when there is none or it expired.
	Get(ctx context.Context, key string) (*CachedFeed, bool)
	// Put stores a feed for the given lifetime.
	Put(ctx context.Context, key string, f *CachedFeed, items []CachedItem, lifetime time.Duration) error
}

// BuildRecorder notes each build, so that a feed which has quietly stopped
// producing items can be explained after the fact.
type BuildRecorder interface {
	// Record notes one build attempt.
	Record(ctx context.Context, key, sourceURL string, ok bool, itemCount int, selector, confidence, errMsg string) error
}

// Server answers feed requests.
type Server struct {
	pipe        *pipeline.Pipeline
	log         *slog.Logger
	nowFn       func() time.Time
	cache       FeedCache
	recorder    BuildRecorder
	admin       FeedAdmin
	manageToken string
}

// New builds a Server.
//
// The logger defaults to one that discards output rather than to the standard
// logger: a library must not write to the host's log stream unless asked, and a
// caller that wants logs passes them in.
func New(o Options) *Server {
	s := &Server{
		pipe:        o.Pipeline,
		log:         o.Logger,
		nowFn:       o.Now,
		cache:       o.Cache,
		recorder:    o.Recorder,
		admin:       o.Admin,
		manageToken: o.ManageToken,
	}
	if s.log == nil {
		s.log = slog.New(discardHandler{})
	}
	if s.nowFn == nil {
		s.nowFn = time.Now
	}
	return s
}

// discardHandler drops every record.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (h discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h discardHandler) WithGroup(string) slog.Handler           { return h }

// ServeHTTP routes the two endpoints.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	switch path {
	case "/extract":
		s.handleExtract(w, r)
	case "/check":
		s.handleCheck(w, r)
	case "/preview":
		s.handlePreview(w, r)
	case feedsPath, opmlPath:
		if s.authorizeManage(w, r) {
			if path == opmlPath {
				s.handleFeedsOPML(w, r)
			} else {
				s.handleFeeds(w, r)
			}
		}
	case loginPath:
		s.handleLogin(w, r)
	case "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	case "/feedme.png":
		writeAsset(w, r, logoPNG, "image/png")
	case "/favicon.png":
		writeAsset(w, r, faviconPNG, "image/png")
	case "/favicon.ico":
		writeAsset(w, r, faviconICO, "image/x-icon")
	case "/":
		s.handleIndex(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
	attrs := []any{
		"method", r.Method,
		"path", path,
		"duration", s.now().Sub(start).Round(time.Millisecond),
	}
	if id := r.Header.Get("X-Request-Id"); id != "" {
		attrs = append(attrs, "request_id", id)
	}
	s.log.Info("request", attrs...)
}

// authorizeManage guards the management page. With no token configured the page
// is open; setting one closes it to callers that cannot present the token.
//
// A browser that cannot present it is sent to the sign-in form rather than
// challenged, because the browser's own Basic prompt is what Chromium now
// answers with an error page; login.go tells that story. Everything else gets
// the challenge, which is the answer a script knows how to act on.
func (s *Server) authorizeManage(w http.ResponseWriter, r *http.Request) bool {
	if s.manageToken == "" {
		return true
	}
	if s.credentialOK(r) {
		return true
	}
	if wantsHTML(r) {
		http.Redirect(w, r, loginPath, http.StatusSeeOther)
		return false
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="feedme", charset="UTF-8"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
	return false
}

// handleExtract answers a feed request. This is the endpoint a reader points
// at, so its error handling is the interesting part.
func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// A generated feed is a view of someone else's content, not a page to index.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	spec, err := feedurl.Parse(r.URL.Query())
	if err != nil {
		s.writeParseError(w, r, err)
		return
	}

	// The feed's canonical URL is its identity: it is the cache key, the
	// rel="self" link readers use to rediscover the feed, and the one thing a
	// user has to keep. Deriving it from the spec rather than the raw query
	// means "?url=X" and "?url=X&format=rss" are recognised as one feed.
	key := selfLink(r, spec)
	spec.Feed.SelfLink = key
	spec.Feed.SelfType = contentTypeOf(spec.Format)

	resp, fromFeedCache, err := s.buildFeed(r.Context(), key, spec, truthyParam(r, "refresh"), true)
	if err != nil {
		s.writeRunError(w, r, err)
		return
	}
	s.writeFeed(w, r, resp, fromFeedCache)
}

// buildFeed renders a feed, preferring a stored one so that a request within
// the feed's lifetime costs nothing. When useFeedCache is false the feed is
// always built, which is what a preview wants: it must show the page as it is
// now, not a stored rendering. A build made that way is still stored, so the
// /extract that follows is served from the cache.
func (s *Server) buildFeed(ctx context.Context, key string, spec feedurl.Spec, refresh, useFeedCache bool) (feedResponse, bool, error) {
	// The fast path: a stored feed that has not expired answers the request
	// without touching the source site at all. refresh=1 forces a rebuild for a
	// single request; fresh=1 asks for one on every request, and because it is
	// part of the feed's identity it is also never stored below: an entry no
	// request would read is just a row waiting to be pruned.
	bypass := refresh || spec.Fresh
	if useFeedCache && !bypass && s.cache != nil {
		if hit, ok := s.cache.Get(ctx, key); ok {
			return responseFromCache(hit), true, nil
		}
	}

	res, err := s.pipe.Run(ctx, spec)
	if err != nil {
		s.record(ctx, key, spec.URL, false, 0, nil, err)
		return feedResponse{}, false, err
	}

	body, err := feed.Bytes(res.Feed, res.Format)
	if err != nil {
		return feedResponse{}, false, fmt.Errorf("render feed: %w", err)
	}
	resp := feedResponse{
		body:         body,
		etag:         etagOf(body),
		contentType:  contentTypeOf(res.Format),
		itemCount:    len(res.Items),
		fromCache:    res.FromCache,
		lastModified: feedModified(res, s.now()),
		spec:         &spec,
		result:       &res,
		items:        res.Items,
	}
	s.record(ctx, key, spec.URL, true, len(res.Items), res.Detection, nil)
	if s.cache != nil && !spec.Fresh {
		if err := s.cache.Put(ctx, key, asCacheEntry(resp), cachedItems(res.Items), s.lifetime(spec)); err != nil {
			// A cache that cannot be written is a slow server, not a broken one,
			// so the reader still gets their feed.
			s.log.Warn("could not store feed", "key", key, "error", err)
		}
	}
	return resp, false, nil
}

// feedResponse is a rendered feed ready to be written, independent of whether
// it came from the pipeline or from the cache.
type feedResponse struct {
	body        []byte
	etag        string
	contentType string
	itemCount   int
	fromCache   bool
	// lastModified is when this representation was built. It backs the
	// Last-Modified header and the If-Modified-Since check.
	lastModified time.Time
	// spec and result are set only for a freshly built feed, because a cached
	// one has no run behind it. The header helper copes with their absence.
	spec   *feedurl.Spec
	result *pipeline.Result
	// items is the built feed's entries, used by the preview page. It is empty
	// for a response that came from the feed cache.
	items []listpage.Item
}

// feedModified is the last time the representation changed. The listing page's
// fetch time is the honest answer: it is when the source was last read, and it
// is stable across requests that reuse the same cached page.
func feedModified(res pipeline.Result, now time.Time) time.Time {
	if !res.FetchedAt.IsZero() {
		return res.FetchedAt
	}
	return now
}

// asCacheEntry converts a freshly built response into something storable. The
// build time doubles as the last-modified time so that a cache hit reports the
// same validator the build did.
func asCacheEntry(r feedResponse) *CachedFeed {
	fetchedAt := r.lastModified
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	return &CachedFeed{
		Body:        r.body,
		ETag:        r.etag,
		ContentType: r.contentType,
		ItemCount:   r.itemCount,
		Detector:    detectorOf(detectionOf(r.result)),
		Confidence:  confidenceOf(detectionOf(r.result)),
		PageStatus:  pageStatusOf(r.result),
		FetchedAt:   fetchedAt,
	}
}

// pageStatusOf is the listing page's status, for the cache row.
func pageStatusOf(res *pipeline.Result) int {
	if res == nil {
		return 0
	}
	return res.PageStatus
}

// detectionOf unwraps the detection from a run, for the header helpers.
func detectionOf(res *pipeline.Result) *listpage.Result {
	if res == nil {
		return nil
	}
	return res.Detection
}

// responseFromCache converts a stored feed back into a response. There is no run
// behind it, so the full-text, detection and page-cache headers are left off
// rather than guessed at: nothing was fetched, so claiming a page cache hit would
// misdescribe what happened.
func responseFromCache(c *CachedFeed) feedResponse {
	return feedResponse{
		body:         c.Body,
		etag:         c.ETag,
		contentType:  c.ContentType,
		itemCount:    c.ItemCount,
		lastModified: c.FetchedAt,
	}
}

func cachedItems(items []listpage.Item) []CachedItem {
	out := make([]CachedItem, 0, len(items))
	for _, it := range items {
		out = append(out, CachedItem{
			URL:         it.Link,
			Title:       it.Title,
			Author:      it.Author,
			PublishedAt: it.Date,
			Summary:     it.Summary,
			Image:       it.Image,
		})
	}
	return out
}

func detectorOf(det *listpage.Result) string {
	if det == nil {
		return ""
	}
	return det.Selector
}

func confidenceOf(det *listpage.Result) string {
	if det == nil {
		return ""
	}
	return det.Confidence
}

// writeFeed sends a rendered feed, answering a conditional request with 304 when
// the reader already has this exact document.
func (s *Server) writeFeed(w http.ResponseWriter, r *http.Request, resp feedResponse, fromFeedCache bool) {
	cacheControl := s.cacheControl(r)
	lastModified := resp.lastModified.UTC().Truncate(time.Second)
	if !lastModified.IsZero() {
		w.Header().Set("Last-Modified", lastModified.Format(http.TimeFormat))
	}
	if match := r.Header.Get("If-None-Match"); match != "" {
		if match == resp.etag {
			s.writeNotModified(w, resp, cacheControl)
			return
		}
	} else if notModifiedSince(r, lastModified) {
		// The ETag is the stronger validator, so If-Modified-Since is only
		// consulted when the reader sent no If-None-Match. A client that sends
		// both gets the ETag answer, which is what RFC 9110 asks for.
		s.writeNotModified(w, resp, cacheControl)
		return
	}
	w.Header().Set("Content-Type", resp.contentType)
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", resp.etag)
	w.Header().Set("X-Feedme-Items", strconv.Itoa(resp.itemCount))
	if resp.spec != nil {
		if d := detectorOf(detectionOf(resp.result)); d != "" {
			w.Header().Set("X-Feedme-Detection", d)
			w.Header().Set("X-Feedme-Confidence", confidenceOf(detectionOf(resp.result)))
		}
		if resp.spec.FullText && resp.result != nil {
			w.Header().Set("X-Feedme-Fulltext", fmt.Sprintf("%d fetched, %d failed",
				resp.result.FullTextFetched, resp.result.FullTextFailed))
		}
		if len(resp.spec.Feeds) > 0 && resp.result != nil {
			w.Header().Set("X-Feedme-Feeds", fmt.Sprintf("%d fetched, %d failed",
				resp.result.FeedFetched, resp.result.FeedFailed))
		}
	}
	if resp.fromCache {
		w.Header().Set("X-Feedme-Cache", "hit")
	}
	if fromFeedCache {
		// A distinct header, because "the page came from the HTTP cache" and
		// "the whole feed came from storage" mean very different things for load.
		w.Header().Set("X-Feedme-Feedcache", "hit")
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(resp.body)
}

// writeNotModified answers a conditional request. The validators are set again
// because a 304 repeats them, and the body is omitted by definition.
func (s *Server) writeNotModified(w http.ResponseWriter, resp feedResponse, cacheControl string) {
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", resp.etag)
	w.Header().Set("X-Feedme-Items", strconv.Itoa(resp.itemCount))
	w.WriteHeader(http.StatusNotModified)
}

// notModifiedSince reports whether the reader's If-Modified-Since already covers
// the representation. The comparison is inclusive to the second, because HTTP
// dates have no sub-second precision.
func notModifiedSince(r *http.Request, lastModified time.Time) bool {
	if lastModified.IsZero() {
		return false
	}
	ims := r.Header.Get("If-Modified-Since")
	if ims == "" {
		return false
	}
	t, err := http.ParseTime(ims)
	if err != nil {
		return false
	}
	return !lastModified.After(t.UTC().Truncate(time.Second))
}

// requestScheme reports the scheme the caller used. A request that arrived
// through a reverse proxy carries http on the wire, so the proxy's header wins
// when it is there. The session cookie also reads this, to decide whether it may
// be marked Secure.
func requestScheme(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(v, ",")[0]))
	}
	return scheme
}

// selfLink builds the canonical URL of a feed request.
//
// The scheme is taken from the request, or from X-Forwarded-Proto when a proxy
// in front of this server set it, because a rel="self" link that says http when
// the reader arrived over https gets rewritten by some clients and dropped by
// others.
func selfLink(r *http.Request, spec feedurl.Spec) string {
	return selfLinkAt(r, spec, r.URL.Path)
}

// selfLinkAt is selfLink for a caller that is not on the feed path itself. The
// preview about to be rendered is for the same feed the reader will ask for, so
// its identity is the /extract URL regardless of which page asked.
func selfLinkAt(r *http.Request, spec feedurl.Spec, path string) string {
	scheme := requestScheme(r)
	q := spec.Query().Encode()
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	if path == "" {
		path = "/"
	}
	if q == "" {
		return scheme + "://" + host + path
	}
	return scheme + "://" + host + path + "?" + q
}

// lifetime is how long a rendered feed may be reused before it is rebuilt.
func (s *Server) lifetime(spec feedurl.Spec) time.Duration {
	if spec.Feed.TTL > 0 {
		return time.Duration(spec.Feed.TTL) * time.Minute
	}
	return defaultMaxAgeSeconds * time.Second
}

// record notes a build attempt for later inspection.
func (s *Server) record(ctx context.Context, key, sourceURL string, ok bool, itemCount int, det *listpage.Result, buildErr error) {
	if s.recorder == nil {
		return
	}
	msg := ""
	if buildErr != nil {
		msg = buildErr.Error()
	}
	if err := s.recorder.Record(ctx, key, sourceURL, ok, itemCount, detectorOf(det), confidenceOf(det), msg); err != nil {
		s.log.Warn("could not record build", "key", key, "error", err)
	}
}

// handleCheck explains what a feed URL would produce, without fetching
// anything. It is the endpoint to open when a feed is empty and the question is
// whether the page has items at all.
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	spec, err := feedurl.Parse(r.URL.Query())
	if err != nil {
		var fe *feedurl.Error
		if errors.As(err, &fe) {
			s.fail(w, r, http.StatusBadRequest, fe.Error())
			return
		}
		s.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, checkPage(spec))
}

// handlePreview builds the feed and renders its items as HTML, so a user can
// see what they are about to subscribe to before pointing a reader at it. It is
// the same build behind /extract, so a preview warms the feed cache and the feed
// a reader then asks for costs nothing extra.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	spec, err := feedurl.Parse(r.URL.Query())
	if err != nil {
		s.writeParseError(w, r, err)
		return
	}
	key := selfLinkAt(r, spec, "/extract")
	spec.Feed.SelfLink = key
	spec.Feed.SelfType = contentTypeOf(spec.Format)

	resp, _, err := s.buildFeed(r.Context(), key, spec, truthyParam(r, "refresh"), false)
	if err != nil && !errors.Is(err, listpage.ErrNoItems) {
		s.writeRunError(w, r, err)
		return
	}
	if errors.Is(err, listpage.ErrNoItems) {
		// A page with nothing to list is a normal answer for a preview: the
		// user needs to see "no items", not a 500. /extract keeps its stricter
		// behaviour, because a feed nobody can use should not look like a feed.
		resp = feedResponse{}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, previewPage(spec, resp, key))
}

// handleIndex is a small form rather than documentation, so that the service
// can be tried without reading anything first.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexPage())
}

// fail writes a plain-text error with a status code, which is what a script
// calling the API wants to see.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if r.Method == http.MethodHead {
		return
	}
	fmt.Fprintln(w, msg)
}

// writeRunError maps a pipeline failure onto a status code.
//
// The split matters to a reader. A 4xx means the feed URL is wrong or the site
// is refusing us, and re-subscribing will not fix it. A 502 or 503 means the
// failure is temporary, and the reader should try again on its next poll
// instead of dropping the subscription.
func (s *Server) writeRunError(w http.ResponseWriter, r *http.Request, err error) {
	var status *fetch.StatusError
	if errors.As(err, &status) {
		// 404, 410 and 429 are checked first. IsBlocked treats 429 as a
		// refusal, and it would otherwise turn "slow down" into "you are not
		// allowed", which tells the reader to give up rather than to wait.
		switch status.Code {
		case http.StatusNotFound, http.StatusGone:
			s.log.Info("page is gone", "url", status.URL, "code", status.Code)
			s.fail(w, r, http.StatusNotFound, err.Error())
		case http.StatusTooManyRequests:
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
			s.log.Warn("site rate limited the fetch", "url", status.URL, "code", status.Code)
			s.fail(w, r, http.StatusServiceUnavailable, err.Error())
		default:
			if status.IsBlocked() {
				// robots.txt said no, or the site answered with a challenge. The
				// operator needs to see this, so it is logged at warning level.
				s.log.Warn("site refused the fetch", "url", status.URL, "code", status.Code, "error", err)
				s.fail(w, r, http.StatusForbidden, err.Error())
				return
			}
			s.log.Warn("site returned an error", "url", status.URL, "code", status.Code)
			s.fail(w, r, http.StatusBadGateway, err.Error())
		}
		return
	}
	var challenge *fetch.ChallengeError
	if errors.As(err, &challenge) {
		s.log.Warn("site served a challenge", "url", challenge.URL, "code", challenge.Code)
		s.fail(w, r, http.StatusForbidden, err.Error())
		return
	}
	// A robots refusal is a policy decision, not a site failure, and the
	// distinction is worth making in the log even though both are 403.
	if errors.Is(err, fetch.ErrRobotsDenied) {
		s.log.Warn("fetch refused", "reason", "robots.txt")
		s.fail(w, r, http.StatusForbidden, err.Error())
		return
	}
	if errors.Is(err, fetch.ErrPrivateBlocked) {
		s.log.Warn("fetch refused", "reason", "private address")
		s.fail(w, r, http.StatusForbidden, err.Error())
		return
	}
	if errors.Is(err, fetch.ErrTooLarge) {
		s.fail(w, r, http.StatusBadGateway, err.Error())
		return
	}
	if errors.Is(err, context.Canceled) {
		// The reader hung up. There is nobody to answer.
		return
	}
	if errors.Is(err, render.ErrNotConfigured) {
		// The request asked for a feature this server does not have. 501 says
		// that more precisely than a generic 500, and it is not the source
		// site's fault.
		s.log.Warn("render_js requested without a browser", "error", err)
		s.fail(w, r, http.StatusNotImplemented, err.Error())
		return
	}
	s.log.Error("request failed", "error", err)
	s.fail(w, r, http.StatusInternalServerError, err.Error())
}

func contentTypeOf(f feed.Format) string {
	switch f {
	case feed.FormatAtom:
		return "application/atom+xml; charset=utf-8"
	case feed.FormatJSON:
		return "application/feed+json; charset=utf-8"
	default:
		return "application/rss+xml; charset=utf-8"
	}
}

// cacheControl states how long a reader may reuse the response, preferring the
// feed's own ttl because that is what the publisher asked for.
func (s *Server) cacheControl(r *http.Request) string {
	if truthyParam(r, "refresh") || truthyParam(r, "fresh") {
		return "no-store"
	}
	if n := ttlFrom(r); n > 0 {
		return fmt.Sprintf("public, max-age=%d", n*60)
	}
	return defaultMaxAge
}

// defaultMaxAge is fifteen minutes, a compromise between a reader polling often
// enough to feel current and this server not becoming the site's main source of
// traffic.
const defaultMaxAge = "public, max-age=900"

// defaultMaxAgeSeconds is the same window expressed for the cache.
const defaultMaxAgeSeconds = 900

// ttlFrom reads the feed's own caching hint, which the publisher chose and which
// therefore wins over the default.
func ttlFrom(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("ttl")); err == nil && n > 0 {
		return n
	}
	return 0
}

// retryAfterSeconds is what a rate-limited reader is told to wait. The site's
// own Retry-After header is not plumbed through the fetcher, so this is a
// conservative value rather than a guess at the site's real limit.
const retryAfterSeconds = 60

func etagOf(b []byte) string {
	return fmt.Sprintf("%q", shortHash(b))
}

func shortHash(b []byte) string {
	// FNV-1a, 64 bit, as a lowercase hex string. A feed body is not a security
	// boundary, so a cheap hash is the right trade.
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, c := range b {
		h ^= uint64(c)
		h *= prime
	}
	return strconv.FormatUint(h, 16)
}

func truthyParam(r *http.Request, key string) bool {
	switch strings.ToLower(r.URL.Query().Get(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (s *Server) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

const indexHTMLHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>feedme</title>
<link rel="icon" href="/favicon.ico" sizes="any">
<link rel="icon" type="image/png" href="/favicon.png">
<link rel="apple-touch-icon" href="/feedme.png">
<style>
 * { box-sizing: border-box; }
 :root {
  --bg: #fff; --fg: #111; --muted: #666; --line: #e6e6e6; --line-strong: #d4d4d4; --hover: #f5f5f5;
  --font: "Helvetica Neue", Helvetica, Arial, "Liberation Sans", sans-serif;
  --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  /* One type ramp for every page, so no rule has to invent its own size. */
  --text-xs: .8rem; /* hints, notes, small labels */
  --text-sm: .875rem; /* labels, checkboxes, tabs, group titles */
  --text-md: .9375rem; /* body copy and form controls */
 }
 body {
  margin: 0; background: var(--bg); color: var(--fg);
  font-family: var(--font);
  font-size: var(--text-md); line-height: 1.5; -webkit-font-smoothing: antialiased;
 }
 .wrap { max-width: 44rem; margin: 0 auto; padding: 1.25rem 1rem 4rem; }
 .top { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; }
 .brand {
  display: inline-flex; align-items: center; gap: .45rem;
  font-weight: 600; font-size: 1.05rem; letter-spacing: -.015em;
  text-decoration: none; color: var(--fg);
 }
 .brand img { width: 1.6rem; height: 1.6rem; display: block; border-radius: 5px; }
 .top nav a { color: var(--muted); text-decoration: none; margin-left: 1rem; font-size: var(--text-xs); }
 .top nav a:hover { color: var(--fg); }
 h1 { font-size: 1.35rem; font-weight: 600; letter-spacing: -.02em; margin: 0 0 .25rem; }
 .lede { color: var(--muted); font-size: var(--text-md); margin: 0 0 1.4rem; max-width: 34rem; }
 .tabs { display: flex; gap: .2rem; margin: 0 0 1rem; }
 .tabs button {
  background: none; border: 0; border-radius: 6px; padding: .3rem .7rem;
  font: inherit; font-size: var(--text-sm); color: var(--muted); cursor: pointer;
 }
 .tabs button:hover { color: var(--fg); }
 .tabs button.active { background: var(--hover); color: var(--fg); }
 form { margin: 0; }
 fieldset { border: 1px solid var(--line); border-radius: 10px; margin: 0 0 .75rem; padding: .7rem .8rem .8rem; }
 legend { font-size: var(--text-sm); font-weight: 600; color: var(--fg); padding: 0 .3rem; }
 /* A collapsed group is a details whose summary reads like a legend but is
    clickable. Its note says what is inside, so a collapsed group is still
    scannable, and the chevron is drawn in CSS rather than with an icon font. */
 details.group { border: 1px solid var(--line); border-radius: 10px; margin: 0 0 .75rem; }
 details.group > summary {
  display: flex; align-items: center; gap: .5rem; list-style: none;
  padding: .6rem .8rem; border-radius: 10px; cursor: pointer;
  font-size: var(--text-sm); font-weight: 600; color: var(--fg);
 }
 details.group > summary::-webkit-details-marker { display: none; }
 details.group > summary:hover { background: var(--hover); }
 details.group > summary .group-note { font-weight: 400; color: var(--muted); }
 details.group > summary::after {
  content: ""; width: .36rem; height: .36rem; margin-left: auto;
  border-right: 1.5px solid var(--muted); border-bottom: 1.5px solid var(--muted);
  transform: rotate(45deg); transition: transform .15s ease;
 }
 details.group[open] > summary { border-bottom: 1px solid var(--line); border-radius: 10px 10px 0 0; }
 details.group[open] > summary::after { transform: rotate(-135deg); }
 details.group > .group-body { padding: .7rem .8rem .8rem; }
 .field { display: grid; gap: .2rem; margin: .5rem 0; }
 label { font-size: var(--text-sm); color: #444; }
 .hint { font-size: var(--text-xs); color: var(--muted); line-height: 1.5; }
 input, select, textarea {
  padding: .4rem .5rem; border: 1px solid var(--line-strong); border-radius: 6px;
  font: inherit; font-size: var(--text-md); width: 100%; background: var(--bg); color: var(--fg);
 }
 input:focus, select:focus, textarea:focus {
  outline: none; border-color: var(--fg); box-shadow: 0 0 0 3px rgba(17, 17, 17, .08);
 }
 textarea { min-height: 3.5rem; resize: vertical; }
 .row { display: flex; gap: .5rem; }
 .row > * { flex: 1; }
 .check { display: flex; align-items: center; gap: .4rem; font-size: var(--text-sm); color: #444; margin: .55rem 0 .15rem; }
 .check input { width: auto; }
 .actions { display: flex; gap: .4rem; flex-wrap: wrap; margin-top: 1rem; }
 button { font: inherit; font-size: var(--text-md); border-radius: 6px; cursor: pointer; padding: .38rem .75rem; }
 button.primary { border: 1px solid var(--fg); background: var(--fg); color: #fff; }
 button.primary:hover { opacity: .88; }
 button.ghost { border: 1px solid var(--line-strong); background: var(--bg); color: var(--fg); }
 button.ghost:hover { background: var(--hover); }
 code { font-family: var(--mono); background: var(--hover); padding: .08rem .3rem; border-radius: 4px; font-size: .9em; }
 .panel[hidden] { display: none; }
 .note { font-size: var(--text-xs); color: var(--muted); margin: .8rem 0 0; }
 .note a { color: var(--fg); }
</style>
</head>
<body>
<div class="wrap">
`

// indexHTMLBody is the builder page, after the header every page shares.
const indexHTMLBody = `<h1>Build a feed</h1>
<p class="lede">Paste a listing page and get a feed. Every option becomes part of the
feed URL, so a feed is a URL you can edit, share, and keep.</p>

<div class="tabs" role="tablist">
 <button type="button" role="tab" data-mode="auto" aria-selected="true">Automatic</button>
 <button type="button" role="tab" data-mode="simple" aria-selected="false">Simple</button>
 <button type="button" role="tab" data-mode="advanced" aria-selected="false">Advanced</button>
 <button type="button" role="tab" data-mode="merge" aria-selected="false">Merge</button>
</div>

<form method="get" action="/extract" id="feed-form">
 <details class="group" open>
  <summary>Source</summary>
  <div class="group-body">
   <div class="field">
    <label for="url">Listing page URL</label>
    <input id="url" name="url" type="url" placeholder="https://example.com/news">
    <span class="hint">Optional when you merge existing feeds below.</span>
   </div>
   <div class="row">
    <div class="field">
     <label for="format">Format</label>
     <select id="format" name="format">
      <option value="">RSS 2.0 (default)</option>
      <option value="atom">Atom 1.0</option>
      <option value="json">JSON Feed 1.1</option>
     </select>
    </div>
    <div class="field">
     <label for="max">Max items</label>
     <input id="max" name="max" type="number" min="1" max="200" placeholder="50">
    </div>
   </div>
  </div>
 </details>

 <div class="panel" data-panel="auto">
  <p class="hint">The item list is detected automatically; there is nothing else to
  fill in. Use <strong>Preview</strong> to build the feed and see its items, then
  <strong>Build feed</strong> to subscribe.</p>
 </div>

 <div class="panel" data-panel="simple" hidden>
  <details class="group" open>
   <summary>Item container</summary>
   <div class="group-body">
    <div class="field">
     <label for="id_or_class">Element id or class</label>
     <input id="id_or_class" name="id_or_class" type="text" placeholder="news-item">
     <span class="hint">A bare id or class name, read straight off the page. It cannot
     be combined with the advanced <code>item</code> selector.</span>
    </div>
   </div>
  </details>
 </div>

 <div class="panel" data-panel="merge" hidden>
  <details class="group" open>
   <summary>Existing feeds to merge</summary>
   <div class="group-body">
    <div class="field">
     <label for="feeds">Feed URLs</label>
     <textarea id="feeds" name="feeds" data-list="1" placeholder="https://a.example/rss&#10;https://b.example/feed.xml"></textarea>
     <span class="hint">One RSS, Atom, or JSON Feed URL per line. Items are merged,
     deduplicated, and sorted. Add a listing page URL above to merge both, and open
     Full text below to upgrade every item to the article body.</span>
    </div>
   </div>
  </details>
 </div>

 <div class="panel" data-panel="advanced" hidden>
  <details class="group">
   <summary>Extraction<span class="group-note">CSS or XPath</span></summary>
   <div class="group-body">
    <p class="hint">Selectors are CSS. Prefix one with <code>xpath:</code> to give it
    as XPath instead.</p>
    <div class="field"><label for="item">Item container</label>
     <input id="item" name="item" type="text" placeholder="article.card"></div>
    <div class="field"><label for="item_url">Item link</label>
     <input id="item_url" name="item_url" type="text" placeholder="h3 a"></div>
    <div class="field"><label for="item_title">Item title</label>
     <input id="item_title" name="item_title" type="text" placeholder="h3"></div>
    <div class="field"><label for="item_date">Item date</label>
     <input id="item_date" name="item_date" type="text" placeholder="time"></div>
    <div class="field"><label for="item_desc">Item summary</label>
     <input id="item_desc" name="item_desc" type="text" placeholder="p"></div>
    <div class="field"><label for="item_image">Item image</label>
     <input id="item_image" name="item_image" type="text" placeholder="img"></div>
   </div>
  </details>

  <details class="group">
   <summary>Filtering<span class="group-note">Keep or drop items</span></summary>
   <div class="group-body">
    <div class="field"><label for="remove">Remove (whole subtrees)</label>
     <textarea id="remove" name="remove" data-list="1" placeholder="nav&#10;.ads&#10;footer"></textarea>
     <span class="hint">One selector per line.</span></div>
    <div class="field"><label for="strip_css">Strip text (keep the wrapper)</label>
     <textarea id="strip_css" name="strip_css" data-list="1" placeholder=".read-more"></textarea></div>
    <div class="field"><label for="url_contains">Keep URLs containing</label>
     <textarea id="url_contains" name="url_contains" data-list="1" placeholder="/2026/"></textarea></div>
    <div class="field"><label for="strip_if_url">Drop URLs containing</label>
     <textarea id="strip_if_url" name="strip_if_url" data-list="1" placeholder="/tag/"></textarea></div>
    <div class="field"><label for="uri_matches">Keep URLs matching (regex)</label>
     <input id="uri_matches" name="uri_matches" type="text" placeholder="/20\d\d/"></div>
    <div class="field"><label for="text_contains">Keep text containing</label>
     <textarea id="text_contains" name="text_contains" data-list="1"></textarea></div>
    <div class="field"><label for="strip_if_text">Drop text containing</label>
     <textarea id="strip_if_text" name="strip_if_text" data-list="1"></textarea></div>
    <div class="field"><label for="domain">Keep domains</label>
     <textarea id="domain" name="domain" data-list="1" placeholder="example.com"></textarea></div>
    <div class="row">
     <div class="field"><label for="date_after">On or after</label>
      <input id="date_after" name="date_after" type="text" placeholder="2026-01-01"></div>
     <div class="field"><label for="date_before">Before</label>
      <input id="date_before" name="date_before" type="text" placeholder="2026-12-31"></div>
    </div>
   </div>
  </details>

  <details class="group">
   <summary>Fetching<span class="group-note">Headers, cache, JavaScript</span></summary>
   <div class="group-body">
    <div class="field"><label for="user_agent">User-Agent</label>
     <input id="user_agent" name="user_agent" type="text" placeholder="server default"></div>
    <div class="field"><label for="referer">Referer</label>
     <input id="referer" name="referer" type="text" placeholder="listing page for article fetches"></div>
    <div class="row">
     <div class="field"><label for="html_cleanup">HTML cleanup</label>
      <select id="html_cleanup" name="html_cleanup">
       <option value="">Default (on)</option>
       <option value="1">On</option>
       <option value="0">Off</option>
      </select></div>
     <div class="field"><label for="render_js">Render JavaScript</label>
      <select id="render_js" name="render_js">
       <option value="">Off (default)</option>
       <option value="1">On</option>
      </select>
      <span class="hint">Falls back to the server's headless browser only when the plain
      page yields nothing. Needs a browser configured on the server; a JS-only page
      without one returns 501.</span></div>
    </div>
    <label class="check"><input type="checkbox" id="fresh" name="fresh" value="1">
     Always fetch fresh</label>
    <span class="hint">Skip feedme's own cache and re-query the source on every
    request. The feed also tells readers not to cache, so each poll reaches the site.</span>
   </div>
  </details>

  <details class="group">
   <summary>Hosts<span class="group-note">Rewrite item links</span></summary>
   <div class="group-body">
    <div class="field"><label for="force_host">Force host</label>
     <input id="force_host" name="force_host" type="text" placeholder="www.example.com">
     <span class="hint">Rewrite every item link and image onto this host, keeping the
     scheme, path, and query. Useful when a site links its articles on a host that
     refuses this server.</span></div>
    <label class="check"><input type="checkbox" id="allow_cross_host" name="allow_cross_host" value="1">
     Keep links to a different site</label>
    <span class="hint">Off by default, or social and partner links would join the feed.
    Links to a site's own subdomains are always kept.</span>
   </div>
  </details>

  <details class="group">
   <summary>Output<span class="group-note">Title, dates, limits</span></summary>
   <div class="group-body">
    <div class="field"><label for="description">Feed description</label>
     <input id="description" name="description" type="text"></div>
    <div class="row">
     <div class="field"><label for="lang">Language</label>
      <input id="lang" name="lang" type="text" placeholder="en"></div>
     <div class="field"><label for="ttl">TTL (minutes)</label>
      <input id="ttl" name="ttl" type="number" min="0" max="10080" placeholder="0"></div>
    </div>
    <div class="field"><label for="guid">Item identity</label>
     <select id="guid" name="guid">
      <option value="">link (default)</option>
      <option value="link">link</option>
      <option value="stable">stable</option>
      <option value="title">title</option>
     </select></div>
    <div class="row">
     <div class="field"><label for="summary_chars">Summary limit</label>
      <input id="summary_chars" name="summary_chars" type="number" min="0" placeholder="no limit"></div>
     <div class="field"><label for="fulltext_chars">Full-text limit</label>
      <input id="fulltext_chars" name="fulltext_chars" type="number" min="0" placeholder="no limit"></div>
    </div>
    <div class="field"><label for="keep_qs_params">Keep query params</label>
     <input id="keep_qs_params" name="keep_qs_params" type="text" placeholder="1, 0, or comma-separated names"></div>
   </div>
  </details>
 </div>

 <details class="group">
  <summary>Full text<span class="group-note">Fetch article bodies</span></summary>
  <div class="group-body">
   <div class="row">
    <div class="field">
     <label for="fulltext">Fetch each article</label>
     <select id="fulltext" name="fulltext">
      <option value="">No (use the listing summary)</option>
      <option value="1">Yes (fetch article bodies)</option>
     </select>
     <span class="hint">One fetch per item, so keep this feed short.</span>
    </div>
    <div class="field">
     <label for="fulltext_max">Articles to fetch</label>
     <input id="fulltext_max" name="fulltext_max" type="number" min="1" max="200" placeholder="20">
     <span class="hint">Default 20, up to 200. This also caps feed length.</span>
    </div>
   </div>
   <div class="field">
    <label for="title">Feed title</label>
    <input id="title" name="title" type="text" placeholder="My news feed">
   </div>
  </div>
 </details>

 <div class="actions">
  <button class="primary" type="submit">Build feed</button>
  <button class="ghost" type="button" id="preview">Preview</button>
 </div>
 <p class="note">Preview builds the feed and shows its items, so you can check the
 result before subscribing. Every feed you build stays listed on <a href="/feeds">Manage
 feeds</a>.</p>
</form>

<noscript><p class="hint">JavaScript is off. The form still submits to
<code>/extract</code>; use one selector per field.</p></noscript>
</div>

<script>
(function () {
 var form = document.getElementById('feed-form');
 var tabs = document.querySelectorAll('.tabs [data-mode]');
 var panels = document.querySelectorAll('.panel[data-panel]');

 function setMode(name) {
  tabs.forEach(function (t) {
   var on = t.getAttribute('data-mode') === name;
   t.classList.toggle('active', on);
   t.setAttribute('aria-selected', on ? 'true' : 'false');
  });
  panels.forEach(function (p) {
   p.hidden = p.getAttribute('data-panel') !== name;
  });
 }

 function collect() {
  var params = new URLSearchParams();
  form.querySelectorAll('[name]').forEach(function (el) {
   var panel = el.closest ? el.closest('.panel') : null;
   if (panel && panel.hidden) return;
   if (el.type === 'checkbox' && !el.checked) return;
   var value = (el.value || '').trim();
   if (!value) return;
   if (el.dataset.list === '1') {
    value.split(/\r?\n/).forEach(function (line) {
     line = line.trim();
     if (line) params.append(el.name, line);
    });
   } else {
    params.append(el.name, value);
   }
  });
  return params;
 }

 function needsSource(params) {
  return !params.get('url') && !params.has('feeds');
 }

 form.addEventListener('submit', function (e) {
  e.preventDefault();
  var params = collect();
  if (needsSource(params)) {
   window.alert('Give a listing page URL or at least one feed URL.');
   return;
  }
  window.location.href = '/extract?' + params.toString();
 });

 document.getElementById('preview').addEventListener('click', function () {
  var params = collect();
  if (needsSource(params)) {
   window.alert('Give a listing page URL or at least one feed URL.');
   return;
  }
  window.open('/preview?' + params.toString(), '_blank');
 });

 setMode('auto');
 tabs.forEach(function (t) {
  t.addEventListener('click', function () { setMode(t.getAttribute('data-mode')); });
 });
})();
</script>
</body>
</html>`

func indexPage() string { return indexHTMLHead + brandHeader(navManage) + indexHTMLBody }

func checkPage(spec feedurl.Spec) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<title>feedme check</title>`)
	b.WriteString(iconLinksHTML)
	b.WriteString(`<style>
 * { box-sizing: border-box; }
 :root { --bg: #fff; --fg: #111; --muted: #666; --line: #e6e6e6; --line-strong: #d4d4d4; --hover: #f5f5f5;
  --font: "Helvetica Neue", Helvetica, Arial, "Liberation Sans", sans-serif;
  --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  /* One type ramp for every page, so no rule has to invent its own size. */
  --text-xs: .8rem; /* hints, notes, small labels */
  --text-sm: .875rem; /* labels, checkboxes, tabs, group titles */
  --text-md: .9375rem; /* body copy and form controls */
 }
 body { margin: 0; background: var(--bg); color: var(--fg);
  font-family: var(--font);
  font-size: var(--text-md); line-height: 1.5; -webkit-font-smoothing: antialiased; }
 main { max-width: 44rem; margin: 0 auto; padding: 1.25rem 1rem 4rem; }
 h1 { font-size: 1.35rem; font-weight: 600; letter-spacing: -.02em; margin: 0 0 .25rem; }
 .lede { color: var(--muted); font-size: var(--text-md); margin: 0 0 1.25rem; }
 dl { border: 1px solid var(--line); border-radius: 10px; padding: .5rem .8rem .8rem; margin: 0 0 1.25rem; }
 dt { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin-top: .7rem; }
 dd { margin: .1rem 0 0; font-size: var(--text-sm); }
 code { font-family: var(--mono); background: var(--hover); padding: .08rem .3rem; border-radius: 4px; font-size: .9em; }
 .no { color: #8a2b12; } .yes { color: #1f6f3f; }
 .actions { display: flex; gap: .4rem; flex-wrap: wrap; }
 .btn { text-decoration: none; border: 1px solid var(--line-strong); border-radius: 6px; padding: .38rem .75rem; color: var(--fg); background: var(--bg); font-size: var(--text-md); }
 .btn.primary { background: var(--fg); border-color: var(--fg); color: #fff; }
 .btn:hover { background: var(--hover); }
 .btn.primary:hover { opacity: .88; }
 </style></head><body><main><h1>Request check</h1><p class="lede">No page was fetched. This is what
 this URL resolves to:</p><dl>`)

	row := func(term, value string) {
		b.WriteString("<dt>" + html.EscapeString(term) + "</dt><dd><code>")
		b.WriteString(html.EscapeString(value))
		b.WriteString("</code></dd>")
	}
	list := func(term string, values []string) {
		if len(values) == 0 {
			return
		}
		row(term, strings.Join(values, ", "))
	}
	flag := func(term string, on bool) {
		mark, text := "no", "off"
		if on {
			mark, text = "yes", "on"
		}
		b.WriteString(`<dt>` + html.EscapeString(term) + `</dt><dd class="` + mark + `">` + text + `</dd>`)
	}

	if spec.URL != "" {
		row("url", spec.URL)
	}
	list("feeds", spec.Feeds)
	row("format", string(spec.Format))
	flag("html_cleanup", spec.HTMLCleanup)
	flag("fulltext", spec.FullText)
	if spec.FullText {
		row("fulltext_max", fmt.Sprint(spec.FullTextMax))
	}
	flag("render_js", spec.RenderJS)
	flag("fresh", spec.Fresh)
	flag("allow_cross_host", spec.List.AllowCrossHost)
	if spec.List.ForceHost != "" {
		row("force_host", spec.List.ForceHost)
	}
	if spec.Fetch.UserAgent != "" {
		row("user_agent", spec.Fetch.UserAgent)
	}
	if spec.Fetch.Referer != "" {
		row("referer", spec.Fetch.Referer)
	}
	if spec.List.MaxItems > 0 {
		row("max", fmt.Sprint(spec.List.MaxItems))
	}
	if spec.List.KeepQueryParams != "" {
		row("keep_qs_params", spec.List.KeepQueryParams)
	}
	row("guid", string(spec.Feed.GUID))
	row("ttl", fmt.Sprint(spec.Feed.TTL))
	if spec.Feed.Title != "" {
		row("title", spec.Feed.Title)
	}
	if spec.Feed.Description != "" {
		row("description", spec.Feed.Description)
	}
	if spec.Feed.Language != "" {
		row("lang", spec.Feed.Language)
	}
	if spec.Feed.SummaryMaxRunes > 0 {
		row("summary_chars", fmt.Sprint(spec.Feed.SummaryMaxRunes))
	}
	if spec.Feed.FullTextMaxRunes > 0 {
		row("fulltext_chars", fmt.Sprint(spec.Feed.FullTextMaxRunes))
	}
	if spec.Rules.DateAfter != "" {
		row("date_after", spec.Rules.DateAfter)
	}
	if spec.Rules.DateBefore != "" {
		row("date_before", spec.Rules.DateBefore)
	}
	list("item", spec.List.Item)
	list("item_url", spec.List.URL)
	list("item_title", spec.List.Title)
	list("item_desc", spec.List.Summary)
	list("item_date", spec.List.Date)
	list("item_image", spec.List.Image)
	list("remove", spec.List.Exclude)
	list("strip_css", spec.List.StripCSS)
	list("url_contains", spec.Rules.URLContains)
	list("strip_if_url", spec.Rules.URLNotContains)
	list("uri_matches", spec.Rules.URIMatches)
	list("text_contains", spec.Rules.TextContains)
	list("strip_if_text", spec.Rules.TextNotContains)
	list("domain", spec.Rules.Domain)

	b.WriteString(`</dl><div class="actions"><a class="btn primary" href="/extract`)
	b.WriteString(html.EscapeString(feedURLForCheck(spec)))
	b.WriteString(`">Build this feed</a><a class="btn" href="/preview`)
	b.WriteString(html.EscapeString(feedURLForCheck(spec)))
	b.WriteString(`">Preview items</a><a class="btn" href="/">Start over</a></div>`)
	b.WriteString(`</main></body></html>`)
	return b.String()
}

// feedURLForCheck rebuilds a link to /extract from the spec, so the check page
// ends in a button that does what the reader just described.
func feedURLForCheck(spec feedurl.Spec) string {
	values := spec.Query()
	q := values.Encode()
	if q == "" {
		return ""
	}
	return "?" + q
}

// previewPage renders the items a feed currently contains. It is the answer to
// "did I get this right?" and exists so that a user can judge a feed before
// committing a reader to it. Everything shown comes from the same build the
// reader would receive, so the page cannot drift from the feed.
func previewPage(spec feedurl.Spec, resp feedResponse, feedURL string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>feedme - preview</title>`)
	b.WriteString(iconLinksHTML)
	b.WriteString(previewStyle)
	b.WriteString(`</head><body>`)
	b.WriteString(brandHeader(navBoth))
	b.WriteString(`<main><h1>Preview</h1>`)
	b.WriteString(`<p class="lede">This is what the feed contains right now, before you subscribe.</p>`)

	b.WriteString(`<div class="stats">`)
	stat := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(`<div class="stat"><span class="k">` + html.EscapeString(k) + `</span><span class="v">` + html.EscapeString(v) + `</span></div>`)
	}
	stat("items", strconv.Itoa(len(resp.items)))
	stat("format", string(spec.Format))
	if spec.URL != "" {
		stat("source", spec.URL)
	}
	if len(spec.Feeds) > 0 {
		stat("feeds merged", strconv.Itoa(len(spec.Feeds)))
	}
	if resp.result != nil {
		if d := resp.result.Detection; d != nil {
			stat("detected by", d.Selector)
			stat("confidence", d.Confidence)
		}
		if spec.FullText {
			stat("full text", fmt.Sprintf("%d fetched, %d failed", resp.result.FullTextFetched, resp.result.FullTextFailed))
		}
		if len(spec.Feeds) > 0 {
			stat("feeds", fmt.Sprintf("%d fetched, %d failed", resp.result.FeedFetched, resp.result.FeedFailed))
		}
		if resp.result.FromCache {
			stat("listing", "from cache")
		}
		// The filter counts are the answer to the most common complaint: an
		// empty feed with nothing to explain it. A rule that dropped everything
		// is named here rather than left for the user to guess at.
		if f := resp.result.Filtered; f.Examined > 0 {
			if f.TotalDropped() > 0 {
				stat("examined", strconv.Itoa(f.Examined))
				stat("dropped", dropSummary(f))
			} else if f.Examined != len(resp.items) {
				stat("examined", strconv.Itoa(f.Examined))
			}
		}
	}
	b.WriteString(`</div>`)

	if len(resp.items) == 0 {
		if resp.result != nil && resp.result.Filtered.TotalDropped() > 0 {
			b.WriteString(`<p class="warn">Every item was filtered out: ` +
				html.EscapeString(dropSummary(resp.result.Filtered)) +
				`. Loosen the rule or check its spelling.</p>`)
		} else {
			b.WriteString(`<p class="warn">No items were found. Check the URL, or set the item selector in Advanced.</p>`)
		}
	}

	b.WriteString(`<ol class="items">`)
	for _, it := range resp.items {
		b.WriteString(`<li>`)
		title := it.Title
		if title == "" {
			title = it.Link
		}
		b.WriteString(`<a class="title" href="` + html.EscapeString(it.Link) + `" rel="noopener noreferrer">` + html.EscapeString(title) + `</a>`)
		if !it.Date.IsZero() {
			b.WriteString(`<time>` + html.EscapeString(it.Date.Format("2 Jan 2006 15:04")) + `</time>`)
		}
		if s := plainSnippet(it.Summary, 240); s != "" {
			b.WriteString(`<p class="snip">` + html.EscapeString(s) + `</p>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ol>`)

	b.WriteString(`<div class="actions">`)
	b.WriteString(`<a class="btn primary" href="` + html.EscapeString(feedURL) + `">Open feed</a>`)
	b.WriteString(`<a class="btn" href="/check`)
	b.WriteString(html.EscapeString(feedURLForCheck(spec)))
	b.WriteString(`">Resolved parameters</a><a class="btn" href="/">Edit</a></div>`)
	b.WriteString(`</main></body></html>`)
	return b.String()
}

// dropSummary names how many items each filter rule removed, in a stable
// order, as one line for the preview's stats block.
func dropSummary(r filter.Result) string {
	reasons := r.Reasons()
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, fmt.Sprintf("%s %d", reason, r.Dropped[reason]))
	}
	return strings.Join(parts, ", ")
}

// tagPattern matches an HTML tag, which plainSnippet drops so a summary reads as
// one line of text rather than a fragment of markup.
var tagPattern = regexp.MustCompile(`<[^>]*>`)

// plainSnippet flattens a summary to a single plain-text line and cuts it to n
// runes, so the preview stays scannable whatever the source markup looks like.
func plainSnippet(s string, n int) string {
	s = tagPattern.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

const previewStyle = `<style>
 * { box-sizing: border-box; }
 :root { --bg: #fff; --fg: #111; --muted: #666; --line: #e6e6e6; --line-strong: #d4d4d4; --hover: #f5f5f5;
  --font: "Helvetica Neue", Helvetica, Arial, "Liberation Sans", sans-serif;
  --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  /* One type ramp for every page, so no rule has to invent its own size. */
  --text-xs: .8rem; /* hints, notes, small labels */
  --text-sm: .875rem; /* labels, checkboxes, tabs, group titles */
  --text-md: .9375rem; /* body copy and form controls */
 }
 body { margin: 0; background: var(--bg); color: var(--fg);
  font-family: var(--font);
  font-size: var(--text-md); line-height: 1.5; -webkit-font-smoothing: antialiased; }
 .top { display: flex; align-items: center; justify-content: space-between; max-width: 48rem; margin: 0 auto; padding: .9rem 1rem 0; }
 .brand {
  display: inline-flex; align-items: center; gap: .45rem;
  font-weight: 600; font-size: 1.05rem; letter-spacing: -.015em;
  text-decoration: none; color: var(--fg);
 }
 .brand img { width: 1.6rem; height: 1.6rem; display: block; border-radius: 5px; }
 .top nav a { color: var(--muted); text-decoration: none; margin-left: 1rem; font-size: var(--text-xs); }
 .top nav a:hover { color: var(--fg); }
 main { max-width: 48rem; margin: 0 auto; padding: 1.25rem 1rem 4rem; }
 h1 { font-size: 1.35rem; font-weight: 600; letter-spacing: -.02em; margin: 0 0 .25rem; }
 .lede { color: var(--muted); font-size: var(--text-md); margin: 0 0 1.25rem; }
 .stats { display: flex; flex-wrap: wrap; gap: .4rem; margin-bottom: 1.25rem; }
 .stat { border: 1px solid var(--line); border-radius: 6px; padding: .3rem .55rem; }
 .stat .k { display: block; font-size: var(--text-xs); text-transform: uppercase; letter-spacing: .06em; color: var(--muted); }
 .stat .v { display: block; font-size: var(--text-sm); margin-top: .05rem; word-break: break-word; }
 .warn { border: 1px solid #f0cdc2; color: #8a2b12; padding: .5rem .7rem; border-radius: 6px; font-size: var(--text-sm); }
 .items { list-style: none; margin: 0; padding: 0; border-top: 1px solid var(--line); }
 .items li { border-bottom: 1px solid var(--line); padding: .65rem 0; }
 .items .title { font-weight: 500; color: var(--fg); text-decoration: none; }
 .items .title:hover { text-decoration: underline; }
 .items time { display: block; font-size: var(--text-xs); color: var(--muted); margin-top: .1rem; }
 .items .snip { margin: .25rem 0 0; color: #555; font-size: var(--text-sm); }
 .actions { display: flex; flex-wrap: wrap; gap: .4rem; margin-top: 1.5rem; }
 .btn { text-decoration: none; border: 1px solid var(--line-strong); border-radius: 6px; padding: .38rem .75rem; color: var(--fg); background: var(--bg); font-size: var(--text-md); }
 .btn.primary { background: var(--fg); border-color: var(--fg); color: #fff; }
 .btn:hover { background: var(--hover); }
 .btn.primary:hover { opacity: .88; }
</style>`

// writeParseError reports a rejected request. Every parameter problem is the
// caller's, so it is always a 400 with the offending field named.
func (s *Server) writeParseError(w http.ResponseWriter, r *http.Request, err error) {
	var fe *feedurl.Error
	if errors.As(err, &fe) {
		s.fail(w, r, http.StatusBadRequest, fe.Error())
		return
	}
	s.fail(w, r, http.StatusBadRequest, err.Error())
}
