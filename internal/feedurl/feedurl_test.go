package feedurl

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"feedme/internal/feed"
)

func parse(t *testing.T, raw string) Spec {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("test query is not valid: %v", err)
	}
	s, err := Parse(values)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return s
}

func parseErr(t *testing.T, raw string) *Error {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("test query is not valid: %v", err)
	}
	_, err = Parse(values)
	if err == nil {
		t.Fatalf("Parse(%q) should have failed", raw)
	}
	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("error type = %T (%v), want *feedurl.Error", err, err)
	}
	return fe
}

func TestURLIsRequired(t *testing.T) {
	fe := parseErr(t, "max=10")
	if fe.Field != "url" {
		t.Errorf("Field = %q, want url", fe.Field)
	}
}

func TestURLSchemeIsChecked(t *testing.T) {
	// A file:// or javascript: URL here would be a server-side request forgery
	// vector, so it is refused at the boundary rather than at the dialer.
	for _, raw := range []string{
		"url=file%3A%2F%2F%2Fetc%2Fpasswd",
		"url=javascript%3Aalert(1)",
		"url=ftp%3A%2F%2Fe.example%2Fa",
		"url=https%3A%2F%2F",
	} {
		fe := parseErr(t, raw)
		if fe.Field != "url" {
			t.Errorf("%s: Field = %q", raw, fe.Field)
		}
	}
}

func TestDefaults(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2Fnews")
	if s.Format != feed.FormatRSS {
		t.Errorf("Format = %q, want rss", s.Format)
	}
	if s.List.MaxItems != DefaultMaxItems {
		t.Errorf("MaxItems = %d, want %d", s.List.MaxItems, DefaultMaxItems)
	}
	if s.Feed.GUID != feed.GUIDLink {
		t.Errorf("GUID = %q, want link", s.Feed.GUID)
	}
	if !s.HTMLCleanup {
		t.Error("html_cleanup should default to on")
	}
	if s.FullText || s.RenderJS {
		t.Error("fulltext and render_js must default to off")
	}
	if s.Feed.TTL != 0 {
		t.Errorf("TTL = %d, want 0", s.Feed.TTL)
	}
}

func TestFormatSelection(t *testing.T) {
	for _, f := range []string{"rss", "atom", "json", "RSS", "JSON"} {
		s := parse(t, "url=https%3A%2F%2Fe.example%2F&format="+f)
		if !feed.Valid(s.Format) {
			t.Errorf("format %q rejected", f)
		}
	}
	if fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&format=html"); fe.Field != "format" {
		t.Errorf("Field = %q", fe.Field)
	}
}

func TestBooleanParameters(t *testing.T) {
	for _, v := range []string{"1", "true", "yes", "on"} {
		s := parse(t, "url=https%3A%2F%2Fe.example%2F&fulltext="+v)
		if !s.FullText {
			t.Errorf("fulltext=%q should be true", v)
		}
	}
	// Present but empty is not the same as true. fulltext decides whether the
	// server fetches every article body, so a bare "?fulltext=" must not turn
	// it on.
	if s := parse(t, "url=https%3A%2F%2Fe.example%2F&fulltext="); s.FullText {
		t.Error("an empty fulltext value should stay off")
	}
	for _, v := range []string{"0", "false", "no", "off"} {
		s := parse(t, "url=https%3A%2F%2Fe.example%2F&fulltext="+v)
		if s.FullText {
			t.Errorf("fulltext=%q should be false", v)
		}
	}
	// html_cleanup defaults on, so it needs a value to turn off.
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&html_cleanup=0")
	if s.HTMLCleanup {
		t.Error("html_cleanup=0 should disable cleanup")
	}
	s = parse(t, "url=https%3A%2F%2Fe.example%2F")
	if !s.HTMLCleanup {
		t.Error("html_cleanup should be on when absent")
	}
}

// Repeated keys arrive in two spellings depending on which tool wrote the URL.
func TestRepeatedParameterSpellings(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&url_contains%5B%5D=a&url_contains%5B%5D=b")
	if len(s.Rules.URLContains) != 2 {
		t.Errorf("url_contains = %v", s.Rules.URLContains)
	}
	s = parse(t, "url=https%3A%2F%2Fe.example%2F&url_contains=a&url_contains=b")
	if len(s.Rules.URLContains) != 2 {
		t.Errorf("bare url_contains = %v", s.Rules.URLContains)
	}
	s = parse(t, "url=https%3A%2F%2Fe.example%2F&url_contains%5B%5D=a&url_contains=b")
	if len(s.Rules.URLContains) != 2 {
		t.Errorf("mixed spellings = %v", s.Rules.URLContains)
	}
}

func TestIDOrClassBecomesSelector(t *testing.T) {
	cases := map[string]string{
		"kartu":        ".kartu",
		"12345":        "#12345",
		".kartu":       ".kartu",
		"#kartu":       "#kartu",
		"div.kartu":    "div.kartu",
		"ul > li":      "ul > li",
		"article.post": "article.post",
	}
	for in, want := range cases {
		s := parse(t, "url=https%3A%2F%2Fe.example%2F&id_or_class="+url.QueryEscape(in))
		if len(s.List.Item) != 1 || s.List.Item[0] != want {
			t.Errorf("id_or_class(%q) = %v, want [%q]", in, s.List.Item, want)
		}
	}
}

func TestIDOrClassConflictsWithItem(t *testing.T) {
	fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&item=.card&id_or_class=card")
	if fe.Field != "id_or_class" {
		t.Errorf("Field = %q", fe.Field)
	}
}

func TestSelectorParametersLandInTheRightPlace(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F"+
		"&item=.card"+
		"&item_url=a.headline"+
		"&item_title=h3"+
		"&item_date=time"+
		"&item_image=img.thumb"+
		"&item_desc=p.teaser"+
		"&remove=.ad"+
		"&strip_css=.strapline")
	checks := []struct {
		name string
		got  []string
		want string
	}{
		{"Item", s.List.Item, ".card"},
		{"URL", s.List.URL, "a.headline"},
		{"Title", s.List.Title, "h3"},
		{"Date", s.List.Date, "time"},
		{"Image", s.List.Image, "img.thumb"},
		{"Summary", s.List.Summary, "p.teaser"},
		{"Exclude", s.List.Exclude, ".ad"},
		{"StripCSS", s.List.StripCSS, ".strapline"},
	}
	for _, c := range checks {
		if len(c.got) != 1 || c.got[0] != c.want {
			t.Errorf("%s = %v, want [%q]", c.name, c.got, c.want)
		}
	}
}

func TestFilterParameters(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F"+
		"&url_contains%5B%5D=kontan"+
		"&strip_if_url%5B%5D=%2Ftag%2F"+
		"&uri_matches%5B%5D=%5Cd%2B"+
		"&text_contains%5B%5D=emas"+
		"&strip_if_text%5B%5D=iklan"+
		"&domain%5B%5D=kontan.co.id")
	if len(s.Rules.URLContains) != 1 || s.Rules.URLContains[0] != "kontan" {
		t.Errorf("URLContains = %v", s.Rules.URLContains)
	}
	if len(s.Rules.URLNotContains) != 1 {
		t.Errorf("URLNotContains = %v", s.Rules.URLNotContains)
	}
	if len(s.Rules.URIMatches) != 1 {
		t.Errorf("URIMatches = %v", s.Rules.URIMatches)
	}
	if len(s.Rules.TextContains) != 1 {
		t.Errorf("TextContains = %v", s.Rules.TextContains)
	}
	if len(s.Rules.TextNotContains) != 1 {
		t.Errorf("TextNotContains = %v", s.Rules.TextNotContains)
	}
	if len(s.Rules.Domain) != 1 {
		t.Errorf("Domain = %v", s.Rules.Domain)
	}
}

// A typo in a date bound must be reported at the door, not turn into a feed
// that is silently unfiltered.
func TestDateBoundsAreValidated(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&date_after=2026-09-01")
	if s.Rules.DateAfter != "2026-09-01" {
		t.Errorf("DateAfter = %q", s.Rules.DateAfter)
	}
	fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&date_after=nonsense")
	if fe.Field != "date_after" {
		t.Errorf("Field = %q", fe.Field)
	}
	fe = parseErr(t, "url=https%3A%2F%2Fe.example%2F&date_before=05-09-2026")
	if fe.Field != "date_before" {
		t.Errorf("Field = %q, want the ambiguous bound reported", fe.Field)
	}
	// A full timestamp is fine and truncated to its day.
	s = parse(t, "url=https%3A%2F%2Fe.example%2F&date_after=2026-09-01T08%3A00%3A00Z")
	if s.Rules.DateAfter != "2026-09-01" {
		t.Errorf("DateAfter = %q, want truncation to the day", s.Rules.DateAfter)
	}
}

func TestMaxIsChecked(t *testing.T) {
	if s := parse(t, "url=https%3A%2F%2Fe.example%2F&max=25"); s.List.MaxItems != 25 {
		t.Errorf("MaxItems = %d", s.List.MaxItems)
	}
	for _, raw := range []string{"max=abc", "max=0", "max=-5", "max=99999"} {
		fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&"+raw)
		if fe.Field != "max" {
			t.Errorf("%s: Field = %q", raw, fe.Field)
		}
	}
}

// One URL asking for 200 full articles is a crawl, not a feed load.
func TestFullTextLowersTheItemCap(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=200")
	if s.List.MaxItems > fullTextItemLimit {
		t.Errorf("MaxItems = %d, want at most %d for a full-text feed", s.List.MaxItems, fullTextItemLimit)
	}
	if !s.FullText {
		t.Error("FullText should be on")
	}
	// Without full text the larger cap stands.
	s = parse(t, "url=https%3A%2F%2Fe.example%2F&max=200")
	if s.List.MaxItems != 200 {
		t.Errorf("MaxItems = %d, want 200", s.List.MaxItems)
	}
}

// fulltext_max lets a caller pay for more than the default twenty articles, up
// to the same ceiling as max.
func TestFullTextMaxRaisesTheCap(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=200&fulltext_max=200")
	if s.List.MaxItems != 200 {
		t.Errorf("MaxItems = %d, want 200", s.List.MaxItems)
	}
	if s.FullTextMax != 200 {
		t.Errorf("FullTextMax = %d, want 200", s.FullTextMax)
	}
	// It cannot raise the feed past max.
	s = parse(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=30&fulltext_max=200")
	if s.List.MaxItems != 30 {
		t.Errorf("MaxItems = %d, want 30", s.List.MaxItems)
	}
	// The canonical query carries the raised cap, so a shared feed URL keeps it.
	q := s.Query()
	if q.Get("fulltext_max") != "200" {
		t.Errorf("Query lost fulltext_max: %v", q)
	}
	back, err := Parse(q)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if back.List.MaxItems != s.List.MaxItems {
		t.Errorf("round-trip changed MaxItems: %d -> %d", s.List.MaxItems, back.List.MaxItems)
	}
}

func TestFullTextMaxIsValidated(t *testing.T) {
	for _, bad := range []string{"0", "201", "-1", "abc", "1.5"} {
		values := url.Values{"url": {"https://e.example/"}, "fulltext_max": {bad}}
		if _, err := Parse(values); err == nil {
			t.Errorf("fulltext_max %q should be rejected", bad)
		}
	}
	// Absent means the default, not "no limit".
	if s := parse(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1"); s.FullTextMax != fullTextItemLimit {
		t.Errorf("FullTextMax = %d, want %d", s.FullTextMax, fullTextItemLimit)
	}
}

// fresh=1 is a persistent request for an uncached feed, so it must survive a
// round trip through Query: the canonical URL is the feed's identity, and a
// reader that subscribes to a fresh feed would otherwise silently lose it.
func TestFreshIsParsedAndRoundTrips(t *testing.T) {
	if s := parse(t, "url=https%3A%2F%2Fe.example%2F"); s.Fresh {
		t.Error("fresh defaults to off")
	}
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&fresh=1")
	if !s.Fresh {
		t.Fatal("fresh=1 was not parsed")
	}
	q := s.Query()
	if q.Get("fresh") != "1" {
		t.Errorf("Query lost fresh: %v", q)
	}
	back, err := Parse(q)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !back.Fresh {
		t.Error("round-trip dropped fresh")
	}
	// "fresh=" means false, like every other boolean flag.
	if s := parse(t, "url=https%3A%2F%2Fe.example%2F&fresh="); s.Fresh {
		t.Error("an empty fresh must not turn the flag on")
	}
}

func TestGUIDModes(t *testing.T) {
	for in, want := range map[string]feed.GUIDMode{
		"":       feed.GUIDLink,
		"link":   feed.GUIDLink,
		"stable": feed.GUIDStable,
		"title":  feed.GUIDTitle,
	} {
		s := parse(t, "url=https%3A%2F%2Fe.example%2F&guid="+in)
		if s.Feed.GUID != want {
			t.Errorf("guid=%q gave %q, want %q", in, s.Feed.GUID, want)
		}
	}
	fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&guid=whatever")
	if fe.Field != "guid" {
		t.Errorf("Field = %q", fe.Field)
	}
}

func TestKeepQueryParamsValidation(t *testing.T) {
	for _, v := range []string{"0", "1", "id", "id,cat"} {
		if s := parse(t, "url=https%3A%2F%2Fe.example%2F&keep_qs_params="+url.QueryEscape(v)); s.List.KeepQueryParams != v {
			t.Errorf("keep_qs_params=%q rejected", v)
		}
	}
	fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&keep_qs_params=a%3Db")
	if fe.Field != "keep_qs_params" {
		t.Errorf("Field = %q", fe.Field)
	}
}

// A blank parameter value is discarded rather than reported. A user editing a
// saved feed URL often leaves an empty field behind, and failing the whole
// request over one of them is hostile.
func TestBlankValuesAreDiscarded(t *testing.T) {
	values := url.Values{}
	values.Set("url", "https://e.example/")
	values["item"] = []string{"   ", ".card", ""}
	values["url_contains[]"] = []string{"", "kontan"}
	s, err := Parse(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List.Item) != 1 || s.List.Item[0] != ".card" {
		t.Errorf("Item = %v, want only the real selector", s.List.Item)
	}
	if len(s.Rules.URLContains) != 1 {
		t.Errorf("URLContains = %v", s.Rules.URLContains)
	}
}

func TestFetchOverrides(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&user_agent=MyReader%2F1.0&referer=https%3A%2F%2Fe.example%2F")
	if s.Fetch.UserAgent != "MyReader/1.0" {
		t.Errorf("UserAgent = %q", s.Fetch.UserAgent)
	}
	if s.Fetch.Referer != "https://e.example/" {
		t.Errorf("Referer = %q", s.Fetch.Referer)
	}
}

func TestFeedMetadata(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&title=Berita&description=Cuplikan&lang=id&ttl=60")
	if s.Feed.Title != "Berita" {
		t.Errorf("Title = %q", s.Feed.Title)
	}
	if s.Feed.Description != "Cuplikan" {
		t.Errorf("Description = %q", s.Feed.Description)
	}
	if s.Feed.Language != "id" {
		t.Errorf("Language = %q", s.Feed.Language)
	}
	if s.Feed.TTL != 60 {
		t.Errorf("TTL = %d", s.Feed.TTL)
	}
	fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&ttl=99999")
	if fe.Field != "ttl" {
		t.Errorf("Field = %q", fe.Field)
	}
}

func TestMaxAgeUsesTTLThenFallback(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fe.example%2F&ttl=30")
	if got := s.MaxAge(time.Hour); got != 30*time.Minute {
		t.Errorf("MaxAge = %s, want 30m", got)
	}
	s = parse(t, "url=https%3A%2F%2Fe.example%2F")
	if got := s.MaxAge(time.Hour); got != time.Hour {
		t.Errorf("MaxAge = %s, want the fallback", got)
	}
}

func TestErrorMessageNamesTheField(t *testing.T) {
	fe := parseErr(t, "url=https%3A%2F%2Fe.example%2F&max=0")
	msg := fe.Error()
	if !strings.Contains(msg, "max") {
		t.Errorf("error should name the field: %q", msg)
	}
	if !strings.Contains(msg, `"0"`) {
		t.Errorf("error should show the value: %q", msg)
	}
	if fe.FieldName() != "max" {
		t.Errorf("FieldName = %q", fe.FieldName())
	}
}

func TestParseAllowCrossHostAndForceHost(t *testing.T) {
	s := parse(t, "url=https%3A%2F%2Fwww.kontan.co.id%2F&allow_cross_host=1&force_host=www.kontan.co.id")
	if !s.List.AllowCrossHost {
		t.Error("AllowCrossHost should be true")
	}
	if s.List.ForceHost != "www.kontan.co.id" {
		t.Errorf("ForceHost = %q", s.List.ForceHost)
	}
	q := s.Query()
	if q.Get("allow_cross_host") != "1" {
		t.Errorf("Query lost allow_cross_host: %v", q)
	}
	if q.Get("force_host") != "www.kontan.co.id" {
		t.Errorf("Query lost force_host: %v", q)
	}
}

func TestForceHostIsOptionalAndValidated(t *testing.T) {
	// Absent is fine.
	if s := parse(t, "url=https%3A%2F%2Fe.example%2F"); s.List.ForceHost != "" {
		t.Errorf("ForceHost = %q, want empty", s.List.ForceHost)
	}
	for _, bad := range []string{"https://x.example", "x.example/path", "a b", "x.example?q=1"} {
		values := url.Values{"url": {"https://e.example/"}, "force_host": {bad}}
		if _, err := Parse(values); err == nil {
			t.Errorf("force_host %q should be rejected", bad)
		}
	}
}

// Indexed arrays, as FiveFilter and Feed Creator write them, must parse too, so
// an existing feed URL can be pasted in unchanged.
func TestIndexedArrayParameters(t *testing.T) {
	values, err := url.ParseQuery("url=https%3A%2F%2Fe.example%2F" +
		"&url_contains%5B0%5D=%2Fa%2F&url_contains%5B1%5D=%2Fb%2F" +
		"&remove%5B0%5D=nav&remove%5B1%5D=footer")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(values)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Rules.URLContains; len(got) != 2 || got[0] != "/a/" || got[1] != "/b/" {
		t.Errorf("URLContains = %v, want [/a/ /b/]", got)
	}
	if got := s.List.Exclude; len(got) != 2 || got[0] != "nav" || got[1] != "footer" {
		t.Errorf("Exclude = %v, want [nav footer]", got)
	}
}

// Indices are honoured out of order, and mix with the bare and [] spellings.
func TestIndexedArrayOrdering(t *testing.T) {
	values, _ := url.ParseQuery("url=https%3A%2F%2Fe.example%2F" +
		"&url_contains%5B2%5D=%2Fc%2F&url_contains=%2Ffirst%2F" +
		"&url_contains%5B0%5D=%2Fa%2F&url_contains%5B1%5D=%2Fb%2F")
	s, err := Parse(values)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/first/", "/a/", "/b/", "/c/"}
	got := s.Rules.URLContains
	if len(got) != len(want) {
		t.Fatalf("URLContains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("URLContains = %v, want %v", got, want)
		}
	}
}
