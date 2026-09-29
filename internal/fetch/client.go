// Package fetch is the HTTP client layer: rate limiting, robots compliance,
// conditional requests, response caching and SSRF protection.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

// agentToken is the lowercase substring matched against robots.txt groups.
const agentToken = "feedme"

// ErrRobotsDenied is returned when robots.txt disallows a path. The text names
// the two ways out, because "blocked" on its own leaves the reader with nothing
// to do and the block is not always the end of the story: a publisher that
// forbids its own feed path can still be read one host at a time, and an
// operator who accepts that can say so in a config rather than in a flag that
// silences the check everywhere.
var ErrRobotsDenied = errors.New("blocked by robots.txt " +
	"(a site config for that host with respect_robots: false, or -no-robots, will read it anyway)")

// ErrTooLarge is returned when a response exceeds the configured body cap.
var ErrTooLarge = errors.New("response body exceeds size limit")

// ErrPrivateBlocked is returned when a URL resolves to a private address.
var ErrPrivateBlocked = errors.New("URL resolves to a private or reserved address")

// Response is a fetched HTTP response.
type Response struct {
	URL          string // final URL after redirects
	Status       int
	Body         []byte
	ContentType  string
	ETag         string
	LastModified string
	NotModified  bool
	FromCache    bool
	FetchedAt    time.Time
}

// hostLimiter enforces a minimum gap between requests to the same host.
type hostLimiter struct {
	mu    sync.Mutex
	next  map[string]time.Time
	delay time.Duration
}

func newHostLimiter(delay time.Duration) *hostLimiter {
	return &hostLimiter{next: map[string]time.Time{}, delay: delay}
}

// wait blocks until the host may be contacted again, then reserves the slot.
func (h *hostLimiter) wait(ctx context.Context, host string) error {
	if h.delay <= 0 {
		return nil
	}
	h.mu.Lock()
	now := time.Now()
	earliest := h.next[host]
	if earliest.After(now) {
		wait := earliest.Sub(now)
		h.next[host] = earliest.Add(h.delay)
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		return nil
	}
	h.next[host] = now.Add(h.delay)
	h.mu.Unlock()
	return nil
}

// Client fetches URLs on behalf of the rest of the program.
type Client struct {
	hc       *http.Client
	ua       string
	timeout  time.Duration
	maxBody  int64
	sem      chan struct{}
	hosts    *hostLimiter
	robots   *RobotsCache
	respect  bool
	cacheTTL time.Duration
	// robotsFor opts a single host out of the robots.txt check, so that a
	// publisher whose own feed path is forbidden to automated clients can still
	// be read on that one host. Nil means the check applies everywhere.
	robotsFor func(host string) bool

	// onStore is called with every fresh response so the caller can persist it.
	onStore func(ctx context.Context, r *Response) error
	// cacheGet optionally serves a still-fresh cached response without touching
	// the network.
	cacheGet func(ctx context.Context, u string) (*Response, bool)
}

// Options configures a Client.
type Options struct {
	UserAgent      string
	Timeout        time.Duration
	MaxBodyBytes   int64
	GlobalParallel int
	PerHostGap     time.Duration
	RespectRobots  bool
	AllowPrivate   bool
	CacheTTL       time.Duration
	OnStore        func(ctx context.Context, r *Response) error
	CacheGet       func(ctx context.Context, u string) (*Response, bool)
	// RobotsFor overrides the robots.txt decision for one host. Returning false
	// skips the check for that host only; every other host keeps the global
	// RespectRobots setting.
	RobotsFor func(host string) bool
}

// New builds a Client.
func New(o Options) *Client {
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Second
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = 5 << 20
	}
	if o.GlobalParallel <= 0 {
		o.GlobalParallel = 8
	}
	if o.UserAgent == "" {
		o.UserAgent = "feedme/0.1 (+self-hosted full-text feed generator)"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           dialControl(dialer, o.AllowPrivate),
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	c := &Client{
		hc: &http.Client{
			Transport: transport,
			Timeout:   o.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("stopped after %d redirects", len(via))
				}
				// The dialer re-validates each hop, so we only need to keep
				// scheme and host changes sane.
				return nil
			},
		},
		ua:        o.UserAgent,
		timeout:   o.Timeout,
		maxBody:   o.MaxBodyBytes,
		sem:       make(chan struct{}, o.GlobalParallel),
		hosts:     newHostLimiter(o.PerHostGap),
		respect:   o.RespectRobots,
		robotsFor: o.RobotsFor,
		cacheTTL:  o.CacheTTL,
		onStore:   o.OnStore,
		cacheGet:  o.CacheGet,
	}
	if c.respect {
		c.robots = NewRobotsCache(c, o.CacheTTL)
	}
	return c
}

// Robots exposes the robots cache so discovery can read Sitemap: directives.
func (c *Client) Robots() *RobotsCache { return c.robots }

// Get fetches a URL. When forceFresh is true the origin is always contacted;
// when it is false and the cache holds a still-fresh copy, that copy answers
// without a network request.
func (c *Client) Get(ctx context.Context, rawURL string, forceFresh bool) (*Response, error) {
	return c.get(ctx, rawURL, forceFresh, false)
}

// get performs a request. skipRobots exists so that fetching robots.txt itself
// does not recurse back into the robots check.
func (c *Client) get(ctx context.Context, rawURL string, forceFresh, skipRobots bool) (*Response, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if err := validateURL(u); err != nil {
		return nil, fmt.Errorf("%q: %w", rawURL, err)
	}
	key := normalizeURL(u)

	if !forceFresh && c.cacheGet != nil {
		if r, ok := c.cacheGet(ctx, key); ok {
			r.FromCache = true
			return r, nil
		}
	}

	if c.respect && !skipRobots && c.robotsApply(u) {
		ok, err := c.robots.Allowed(ctx, u)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%s: %w", key, ErrRobotsDenied)
		}
	}

	// Concurrency: global cap first, then the per-host gap.
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	if err := c.hosts.wait(ctx, u.Hostname()); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, c.ua)

	resp, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("fetch %s: timeout", key)
		}
		return nil, fmt.Errorf("fetch %s: %w", key, err)
	}
	defer resp.Body.Close()

	out := newResponse(key, resp)
	if err := c.readBody(out, resp); err != nil {
		return out, err
	}
	if out.NotModified {
		return out, nil
	}

	// A 2xx status is not proof of a document. Bot-protection layers commonly
	// answer with 202 and a small interstitial, which would otherwise be
	// "successfully" extracted as if it were the article.
	if marker, challenged := detectChallenge(resp, out.Body); challenged {
		return out, &ChallengeError{
			URL: out.URL, Code: out.Status, Size: len(out.Body), Marker: marker,
		}
	}

	if c.onStore != nil && !forceFresh {
		// Only a response a later caller could be served from the cache is
		// worth storing. A forced-fresh fetch is never read back, so storing it
		// would just fill the table with rows nothing consults.
		if err := c.onStore(ctx, out); err != nil {
			// A cache write failure must not fail the fetch.
			_ = err
		}
	}
	return out, nil
}

// Post sends a POST request and reads the response.
//
// It exists for the few endpoints that answer a question rather than serving a
// document — the Google News decoder is the only one — so it is deliberately not
// cached: a stored answer would be a stored redirect decision, and the caller
// asks on every rebuild because links expire. Robots, the size cap, the
// per-host gap and the SSRF check all apply exactly as they do to Get, because
// this request reaches a third party's server exactly as much.
func (c *Client) Post(ctx context.Context, rawURL, contentType string, body []byte) (*Response, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if err := validateURL(u); err != nil {
		return nil, fmt.Errorf("%q: %w", rawURL, err)
	}
	key := normalizeURL(u)

	if c.respect && c.robotsApply(u) {
		ok, err := c.robots.Allowed(ctx, u)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%s: %w", key, ErrRobotsDenied)
		}
	}

	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	if err := c.hosts.wait(ctx, u.Hostname()); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applyHeaders(req, c.ua)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("post %s: timeout", key)
		}
		return nil, fmt.Errorf("post %s: %w", key, err)
	}
	defer resp.Body.Close()

	out := newResponse(key, resp)
	if err := c.readBody(out, resp); err != nil {
		return out, err
	}
	return out, nil
}

// robotsApply reports whether the robots.txt check covers this URL. A host that
// opts out through RobotsFor is not checked; everything else is.
func (c *Client) robotsApply(u *url.URL) bool {
	return c.robotsFor == nil || c.robotsFor(u.Hostname())
}

// newResponse builds the Response shell for a completed request, recording the
// URL the response actually came from rather than the one that was asked for.
func newResponse(key string, resp *http.Response) *Response {
	out := &Response{
		URL:          key,
		Status:       resp.StatusCode,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		ContentType:  resp.Header.Get("Content-Type"),
		FetchedAt:    time.Now(),
	}
	if resp.Request != nil && resp.Request.URL != nil {
		out.URL = normalizeURL(resp.Request.URL)
	}
	return out
}

// readBody fills out.Body, turning a conditional hit, a non-2xx status or an
// oversized body into the error that belongs to it.
func (c *Client) readBody(out *Response, resp *http.Response) error {
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a little so the connection can be reused, then report.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return statusError(out.URL, resp.StatusCode, body)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", out.URL, err)
	}
	if int64(len(body)) > c.maxBody {
		return fmt.Errorf("%s: %w (%d bytes)", out.URL, ErrTooLarge, c.maxBody)
	}
	out.Body = body
	return nil
}

// DecodeBody converts a response body to UTF-8.
//
// Precedence matters here. A charset sniffing algorithm applied blindly will
// guess windows-1252 for a page that serves no charset at all, which mangles
// perfectly good UTF-8. So: honour an explicit Content-Type charset, then a
// <meta> declaration, then leave the bytes alone if they are already valid
// UTF-8, and only fall back to sniffing as a last resort.
func DecodeBody(contentType string, body []byte) ([]byte, error) {
	name := charsetFromContentType(contentType)
	if name == "" {
		name = metaCharset(body)
	}
	if name != "" {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "utf-8" || name == "utf8" {
			return body, nil
		}
		if enc, err := htmlindex.Get(name); err == nil {
			if out, err := decodeWith(enc, body); err == nil {
				return out, nil
			}
		}
	}
	// No declaration. Modern pages default to UTF-8, and guessing wrong is
	// worse than trusting the bytes.
	if utf8.Valid(body) {
		return body, nil
	}
	enc, _, _ := charset.DetermineEncoding(body, contentType)
	if enc == nil || enc == encoding.Nop {
		return body, nil
	}
	if out, err := decodeWith(enc, body); err == nil {
		return out, nil
	}
	return body, nil
}

func decodeWith(enc encoding.Encoding, body []byte) ([]byte, error) {
	r := enc.NewDecoder().Reader(bytes.NewReader(body))
	return io.ReadAll(r)
}

// charsetFromContentType pulls the charset parameter out of a Content-Type.
func charsetFromContentType(ct string) string {
	for _, part := range strings.Split(ct, ";") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "charset="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// metaCharset finds a <meta charset> or <meta http-equiv="content-type">
// declaration in the document head, which many older sites rely on instead of
// setting the header.
func metaCharset(body []byte) string {
	head := body
	if len(head) > 4096 {
		head = head[:4096]
	}
	s := string(head)
	lower := strings.ToLower(s)
	for _, needle := range []string{"charset=", "charset="} {
		if i := strings.Index(lower, needle); i >= 0 {
			rest := s[i+len(needle):]
			rest = strings.TrimLeft(rest, " \t\"'")
			end := strings.IndexAny(rest, "\"' >;\n\t")
			if end < 0 {
				end = len(rest)
			}
			if enc := strings.TrimSpace(rest[:end]); enc != "" {
				return enc
			}
		}
	}
	return ""
}

// StatusError reports a response that arrived but could not be used as a
// document. Blocked sites are the single most common real-world failure for a
// fetcher, so the error says what happened and what to try.
type StatusError struct {
	URL     string
	Code    int
	Snippet string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("fetch %s: HTTP %d %s", e.URL, e.Code, http.StatusText(e.Code))
	if hint := blockedHint(e.Code); hint != "" {
		msg += " — " + hint
	}
	if s := statusSnippet([]byte(e.Snippet)); s != "" {
		msg += fmt.Sprintf(" (server said: %q)", s)
	}
	return msg
}

// IsBlocked reports whether the status looks like anti-bot rejection rather
// than a genuine missing page.
func (e *StatusError) IsBlocked() bool { return blockedHint(e.Code) != "" }

func statusError(url string, code int, body []byte) error {
	return &StatusError{URL: url, Code: code, Snippet: string(body)}
}

func blockedHint(code int) string {
	switch code {
	case http.StatusForbidden:
		return "the site refused this client; it is likely blocking datacenter IPs or non-browser user agents (retry with --user-agent set to a browser UA)"
	case http.StatusMethodNotAllowed:
		return "the site rejected the request outright, which usually means it blocks this client or IP for every path"
	case http.StatusNotAcceptable, http.StatusTeapot:
		return "the site is deliberately rejecting the request signature"
	case http.StatusTooManyRequests:
		return "rate limited; back off and retry later"
	case http.StatusServiceUnavailable:
		return "the site is throttling or blocking; retry later"
	}
	return ""
}

// statusSnippet compresses an error body into a short single-line hint.
func statusSnippet(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	const max = 120
	if len(s) > max {
		s = strings.TrimSpace(s[:max]) + "…"
	}
	return s
}

// challengeMarkers are the strings that identify an anti-bot interstitial.
// They are deliberately specific: a false positive would turn a readable
// article into an error, which is worse than a missed challenge.
var challengeMarkers = []string{
	"just a moment",
	"checking your browser",
	"cf-browser-verification",
	"cf_chl_opt",
	"enable javascript and cookies to continue",
	"ddos-guard",
	"data-dome",
	"captcha-delivery",
	"px-captcha",
	"perimeterx",
	"verifying you are human",
	"请开启javascript", // zh: please enable JavaScript
	"press and hold",
	"are you a robot",
	"unusual traffic from your computer",
	"request unsuccessful. incapsula",
	"access denied. reference",
}

// maxChallengeScan caps how much of a body is inspected for markers, so a
// real long article is never scanned in full.
const maxChallengeScan = 96 << 10

// detectChallenge reports whether a successful-looking response is really a
// bot-protection interstitial. Only small bodies are considered: a large
// response with article-shaped content is not a challenge page.
func detectChallenge(resp *http.Response, body []byte) (marker string, challenged bool) {
	if len(body) > maxChallengeScan {
		return "", false
	}
	// A plain-text body, or a body with no HTML at all, is never an article.
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	lower := strings.ToLower(string(body))
	for _, m := range challengeMarkers {
		if strings.Contains(lower, m) {
			cty := strings.TrimSpace(strings.ToLower(strings.SplitN(ct, ";", 2)[0]))
			switch cty {
			case "text/html", "application/xhtml+xml", "":
				return m, true
			}
		}
	}
	// 202/204 with a tiny HTML body and no <article>/<p> content is a
	// challenge response even without a recognisable marker.
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		if len(body) < 8<<10 && !strings.Contains(lower, "<article") && strings.Count(lower, "<p") <= 2 {
			return fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)), true
		}
	}
	return "", false
}

// ChallengeError reports that a site answered with a bot-protection
// interstitial rather than the requested document.
type ChallengeError struct {
	URL    string
	Code   int
	Size   int
	Marker string
}

func (e *ChallengeError) Error() string {
	return fmt.Sprintf(
		"fetch %s: bot-protection challenge (HTTP %d, %s, marker %q) — the site served an interstitial instead of the page; feedme does not execute JavaScript, so retry with --user-agent set to a browser UA, or the site is blocking this IP",
		e.URL, e.Code, humanBytes(e.Size), e.Marker)
}

// IsBlocked reports that this failure is anti-bot rejection.
func (e *ChallengeError) IsBlocked() bool { return true }

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
