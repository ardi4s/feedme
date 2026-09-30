// Package gnews resolves the links that Google News puts in its RSS items.
//
// Since July 2024 those links are opaque. The path segment is not the publisher
// URL under any encoding that can be read, and the page it leads to is a
// JavaScript shell that finishes the redirect in the browser. Google does hand
// the destination to a machine, in two undocumented steps: the article page
// carries a per-article signature, and an internal endpoint exchanges that
// signature for the publisher URL.
//
// Neither step is a contract, and the endpoint has already changed shape twice,
// so every failure here is an ordinary outcome a caller is expected to survive.
// An unresolved Google News link is still a link a reader can follow; it is
// only the full text that is lost.
package gnews

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"feedme/internal/domx"
)

// host is where Google News serves its article pages. Every locale uses it.
const host = "news.google.com"

// paramsPath is the article form that answers a non-browser client. The
// /articles/ and /read/ forms redirect to an apology page instead, which is why
// the id alone is not enough to build the request.
const paramsPath = "https://" + host + "/rss/articles/"

// errNoSignature means the article page did not carry the data the decoder
// needs, which is what a changed page looks like.
var errNoSignature = errors.New("gnews: article page carried no signature")

// ArticleID reports whether rawURL is a Google News article link and returns the
// encoded id its path carries.
func ArticleID(rawURL string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return "", false
	}
	if !strings.EqualFold(u.Hostname(), host) {
		return "", false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	// The id is the last segment and the form name the one before it, which
	// covers /rss/articles/, /articles/, /read/ and the __i/rss/rd/articles/
	// shape the older feeds still use.
	if len(segs) < 2 {
		return "", false
	}
	switch segs[len(segs)-2] {
	case "articles", "read":
	default:
		return "", false
	}
	id := segs[len(segs)-1]
	if id == "" {
		return "", false
	}
	return id, true
}

// signedPrefix marks the id format introduced in July 2024. An id that decodes
// to it is not a publisher URL and cannot be read without asking Google.
const signedPrefix = "AU_yqL"

// DecodeLegacy reads the publisher URL straight out of an id, for the links
// published before July 2024. Those ids are a base64 protobuf whose payload is
// the URL itself, so no request is needed. It reports false for a signed id and
// for anything malformed.
func DecodeLegacy(id string) (string, bool) {
	raw, err := decodeBase64(id)
	if err != nil {
		return "", false
	}
	// Field framing around the payload: 0x08 0x13 0x22 opens it, and
	// 0xd2 0x01 0x00 closes it when it is present at all.
	raw = bytes.TrimPrefix(raw, []byte{0x08, 0x13, 0x22})
	raw = bytes.TrimSuffix(raw, []byte{0xd2, 0x01, 0x00})
	// What follows is a varint length and then that many bytes of URL.
	skip, n := 0, 0
	if len(raw) > 0 {
		if raw[0] >= 0x80 {
			if len(raw) < 2 {
				return "", false
			}
			n = int(raw[0]&0x7f) | int(raw[1])<<7
			skip = 2
		} else {
			n = int(raw[0])
			skip = 1
		}
	}
	if skip == 0 || n <= 0 || skip+n > len(raw) {
		return "", false
	}
	target := string(raw[skip : skip+n])
	if strings.HasPrefix(target, signedPrefix) {
		return "", false
	}
	if !plausibleTarget(target) {
		return "", false
	}
	return target, true
}

// decodeBase64 accepts both padded and unpadded, and both alphabets, because the
// ids have used all four across the formats Google has shipped.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if out, err := enc.DecodeString(s); err == nil {
			return out, nil
		}
	}
	return nil, errors.New("gnews: id is not base64")
}

// plausibleTarget reports whether a decoded string is a URL a request could
// follow, rather than another opaque token.
func plausibleTarget(target string) bool {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	return true
}

// paramsURL builds the article-page request for one id, carrying the locale over
// from the item link so that the page comes back in the same language as the
// feed that named it. Every parameter is optional: without them the page still
// answers with a signature.
func paramsURL(id, itemLink string) string {
	u := paramsPath + id
	q := url.Values{}
	if parsed, err := url.Parse(itemLink); err == nil {
		for _, key := range []string{"hl", "gl", "ceid"} {
			if v := parsed.Query().Get(key); v != "" {
				q.Set(key, v)
			}
		}
	}
	if len(q) == 0 {
		return u
	}
	return u + "?" + q.Encode()
}

// The two data attributes the article page carries for the decoder. Google
// renames internal attributes as often as it changes the endpoint, so they are
// named once here rather than inline.
const (
	attrSignature = "data-n-a-sg"
	attrTimestamp = "data-n-a-ts"
)

// signature is the pair the decoder endpoint expects.
type signature struct {
	value     string
	timestamp int64
	rawTime   string
}

// parseSignature reads the signature and timestamp out of an article page.
func parseSignature(body []byte) (signature, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return signature{}, fmt.Errorf("gnews: parse article page: %w", err)
	}
	var found signature
	ok := false
	domx.WalkElements(doc, func(n *html.Node) bool {
		sg := domx.Attr(n, attrSignature)
		ts := domx.Attr(n, attrTimestamp)
		if sg == "" || ts == "" {
			return true
		}
		found = signature{value: sg, timestamp: timestampValue(ts), rawTime: ts}
		ok = true
		return false
	})
	if !ok {
		return signature{}, errNoSignature
	}
	return found, nil
}

func timestampValue(raw string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return n
}

// batchexecute is the undocumented endpoint that trades a signature for a URL.
const batchexecute = "https://" + host + "/_/DotsSplashUi/data/batchexecute"

// The three names the endpoint is written against: the RPC the request is filed
// under, the name the request payload carries, and the name the answer comes
// back under. They are constants of an undocumented protocol rather than of
// this program, so they are named in one place where a change is visible.
const (
	rpcID        = "Fbv4je"
	requestName  = "garturlreq"
	responseName = "garturlres"
)

// requestContext is the fixed locale block Google's own page sends with the
// request. The endpoint reads it but does not vary its answer by it, so it is
// reproduced rather than reconstructed.
var requestContext = []any{
	[]any{"X", "X", []any{"X", "X"}, nil, nil, 1, 1, "US:en", nil, 1,
		nil, nil, nil, nil, nil, 0, 1},
	"X", "X", 1, []any{1, 1, 1}, 1, 1, nil, 0, 0, nil, 0,
}

// buildRequest assembles the form body the endpoint expects. The inner payload
// travels as a JSON string inside the JSON, which is the shape Google's own
// client uses and the one the endpoint is written against.
func buildRequest(id string, sig signature) ([]byte, error) {
	inner := []any{requestName, requestContext, id, timestampArg(sig), sig.value}
	encoded, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	envelope := []any{rpcID, string(encoded), nil, "0"}
	outer, err := json.Marshal([][]any{{envelope}})
	if err != nil {
		return nil, err
	}
	return []byte("f.req=" + url.QueryEscape(string(outer))), nil
}

// timestampArg sends a number when the attribute held one, and the raw string
// otherwise, since the endpoint rejects a quoted timestamp.
func timestampArg(sig signature) any {
	if sig.timestamp != 0 {
		return sig.timestamp
	}
	return sig.rawTime
}

// parseAnswer reads the publisher URL out of the endpoint's response. The body
// is a batch of JSON rows, each of which is a string that is itself JSON,
// preceded by an anti-hijacking prefix the endpoint requires.
func parseAnswer(body []byte) (string, error) {
	rows, err := decodeRows(string(body))
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if len(row) < 3 || row[1] != rpcID {
			continue
		}
		payload, ok := row[2].(string)
		if !ok {
			continue
		}
		var decoded []any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			continue
		}
		if len(decoded) < 2 || decoded[0] != responseName {
			continue
		}
		target, _ := decoded[1].(string)
		if plausibleTarget(target) {
			return target, nil
		}
	}
	return "", errors.New("gnews: no URL in the decoder response")
}

// decodeRows finds the rows in a batch response and unmarshals them. The
// response is wrapped in an anti-hijacking prefix and a length or two, so rather
// than guess where the JSON begins, each line start is tried in turn and the
// first one that parses wins. A body that is not JSON at all never parses and is
// reported as such.
func decodeRows(text string) ([][]any, error) {
	for start := 0; start < len(text); {
		var rows [][]any
		if err := json.Unmarshal([]byte(text[start:]), &rows); err == nil {
			return rows, nil
		}
		next := strings.IndexByte(text[start:], '\n')
		if next < 0 {
			break
		}
		start += next + 1
	}
	return nil, errors.New("gnews: decoder response was not JSON")
}
