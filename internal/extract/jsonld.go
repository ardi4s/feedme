package extract

import (
	"encoding/json"
	"strings"
	"time"

	"golang.org/x/net/html"

	"feedme/internal/domx"
)

// jsonLDResult is whatever a page's structured data tells us.
type jsonLDResult struct {
	Body      string
	Headline  string
	Name      string
	Byline    string
	Published time.Time
	Modified  time.Time
	Image     string
	SiteName  string
	Found     bool
}

// jsonLDTypes are the schema.org types whose articleBody we trust.
var jsonLDTypes = map[string]bool{
	"NewsArticle": true, "Article": true, "Report": true, "AnalysisNewsArticle": true,
	"BackgroundNewsArticle": true, "OpinionNewsArticle": true, "ReportageNewsArticle": true,
	"ReviewNewsArticle": true, "TechArticle": true, "ScholarlyArticle": true,
	"MedicalScholarlyArticle": true, "BlogPosting": true, "LiveBlogPosting": true,
	"SocialMediaPosting": true, "DiscussionForumPosting": true, "WebPage": true,
	"AdvertiserContentArticle": true, "SatiricalArticle": true, "APIReference": true,
}

// extractJSONLD reads <script type="application/ld+json"> blocks. When the page
// supplies articleBody there is no need to guess at content blocks at all, so
// this strategy outranks every heuristic.
func extractJSONLD(doc *html.Node) *jsonLDResult {
	res := &jsonLDResult{}
	for _, script := range domx.FindElements(doc, "script") {
		if !strings.EqualFold(domx.Attr(script, "type"), "application/ld+json") {
			continue
		}
		// The payload is a script tag, so it must be read raw: TextOf skips
		// inert subtrees by design and would return nothing here.
		raw := strings.TrimSpace(domx.RawTextOf(script))
		if raw == "" {
			continue
		}
		var data any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			// Some sites emit trailing commas or stray control characters.
			if fixed, ok := loosenJSON(raw); ok {
				if err2 := json.Unmarshal([]byte(fixed), &data); err2 != nil {
					continue
				}
			} else {
				continue
			}
		}
		walkJSONLD(data, res)
		if res.Body != "" {
			break
		}
	}
	return res
}

// walkJSONLD descends through objects, arrays and @graph containers.
func walkJSONLD(v any, res *jsonLDResult) {
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			walkJSONLD(item, res)
		}
	case map[string]any:
		if graph, ok := t["@graph"]; ok {
			walkJSONLD(graph, res)
		}
		if !mergeJSONLDNode(t, res) {
			// Not an article node: it may still wrap one, e.g. via mainEntity
			// or an array of article entries.
			for _, key := range []string{"mainEntity", "articleBody", "hasPart", "itemListElement"} {
				if child, ok := t[key]; ok {
					walkJSONLD(child, res)
				}
			}
		}
	}
}

// mergeJSONLDNode reads one schema.org node into res. It returns true when the
// node's @type was a recognised article type.
func mergeJSONLDNode(obj map[string]any, res *jsonLDResult) bool {
	types := typeNames(obj)
	recognised := false
	for _, tp := range types {
		if jsonLDTypes[tp] {
			recognised = true
			break
		}
	}
	if !recognised {
		return false
	}
	if res.Body == "" {
		if body := stringField(obj, "articleBody"); body != "" {
			res.Body = body
			res.Found = true
		}
	}
	// headline and name are kept separate: publishers sometimes put a
	// description in headline, so pickTitle needs to weigh both.
	if res.Headline == "" {
		res.Headline = stringField(obj, "headline")
	}
	if res.Name == "" {
		res.Name = stringField(obj, "name")
	}
	if res.Byline == "" {
		if s := authorName(obj["author"]); s != "" {
			res.Byline = s
		} else if s := stringField(obj, "creator"); s != "" {
			res.Byline = s
		}
	}
	if res.Published.IsZero() {
		for _, key := range []string{"datePublished", "dateCreated", "uploadDate", "datePosted"} {
			if s := stringField(obj, key); s != "" {
				if d := parseDate(s); !d.IsZero() {
					res.Published = d
					break
				}
			}
		}
	}
	if res.Modified.IsZero() {
		if s := stringField(obj, "dateModified"); s != "" {
			res.Modified = parseDate(s)
		}
	}
	if res.Image == "" {
		if s := imageURL(obj["image"]); s != "" {
			res.Image = s
		}
	}
	if res.SiteName == "" {
		if p, ok := obj["publisher"].(map[string]any); ok {
			if s := stringField(p, "name"); s != "" {
				res.SiteName = s
			}
		}
	}
	return true
}

// typeNames normalises @type, which may be a string or an array of strings.
func typeNames(obj map[string]any) []string {
	switch t := obj["@type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// stringField reads a string, tolerating values that JSON typed as numbers.
func stringField(obj map[string]any, key string) string {
	switch v := obj[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return ""
	}
	return ""
}

// authorName pulls a display name out of the several shapes schema.org allows:
// a string, an object, or an array of either.
func authorName(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case map[string]any:
		if s := stringField(t, "name"); s != "" {
			return s
		}
		return ""
	case []any:
		for _, item := range t {
			if s := authorName(item); s != "" {
				return s
			}
		}
	}
	return ""
}

// imageURL pulls a URL out of ImageObject, url, or contentUrl shapes.
func imageURL(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case map[string]any:
		for _, key := range []string{"url", "contentUrl"} {
			if s := stringField(t, key); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range t {
			if s := imageURL(item); s != "" {
				return s
			}
		}
	}
	return ""
}

// loosenJSON applies the small repairs that salvage slightly malformed
// structured data: trailing commas, raw newlines inside strings, and
// unescaped tabs.
func loosenJSON(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			case c == '\n' || c == '\t' || c == '\r':
				b.WriteByte(' ')
				continue
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		// Drop trailing commas before a closing brace/bracket.
		if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\n' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String(), true
}
