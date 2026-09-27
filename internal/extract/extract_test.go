package extract

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"feedme/internal/domx"
)

func mustNode(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// Regression: unwrapNode re-parented children with InsertBefore while they were
// still attached to their old parent. html.Node panics on that
// ("InsertBefore called for an attached child Node"), which crashed the probe
// on every real news page, because those pages are full of nested <div>s with
// junk classes. The mutation passes also used to walk live sibling lists while
// removing or unwrapping nodes, silently skipping siblings.
func TestSanitizeNestedJunkDoesNotPanic(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<article>`)
	// Interleave junk (removed) with content (kept) at every level, so a
	// non-snapshotting loop would skip content.
	for i := 0; i < 40; i++ {
		b.WriteString(`<div class="advert banner">junk</div>`)
		b.WriteString(`<div><div><p>Paragraph `)
		b.WriteString(strings.Repeat("word ", 40))
		b.WriteString(`</p></div></div>`)
		b.WriteString(`<div class="social-share">share</div>`)
		b.WriteString(`<p>Body text ` + strings.Repeat("alpha ", 40) + `</p>`)
	}
	b.WriteString(`</article>`)

	node := mustNode(t, b.String())
	body := mustNode(t, `<body></body>`)
	_ = body
	articles := domx.FindElements(node, "article")
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}

	// The point of the test: this used to panic.
	out := sanitize(articles[0], sanitizeOptions{BaseURL: "https://example.com/a", Prune: true})

	if strings.Contains(out, "junk") || strings.Contains(out, "advert") ||
		strings.Contains(out, "social-share") {
		t.Errorf("junk survived sanitising: %.400s", out)
	}
	// Every real paragraph must survive: a live-list walk would have dropped
	// most of them.
	if got := strings.Count(out, "<p>"); got != 80 {
		t.Errorf("kept %d paragraphs, want 80 (a non-snapshotting loop drops siblings)", got)
	}
	if !strings.Contains(out, "Body text") {
		t.Error("article body was lost")
	}
}

func TestSanitizeUnwrapKeepsSiblings(t *testing.T) {
	// A <div> holding exactly one element and no text of its own is redundant
	// and gets unwrapped. The siblings around it must be untouched.
	in := `<article>
		<div><div><p>first</p></div></div>
		<div><div><p>second</p></div></div>
		<div><div><p>third</p></div></div>
	</article>`
	arts := domx.FindElements(mustNode(t, in), "article")
	out := sanitize(arts[0], sanitizeOptions{BaseURL: "https://example.com/", Prune: true})
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(out, want) {
			t.Errorf("unwrapping lost sibling %q; got %.300s", want, out)
		}
	}
	if strings.Count(out, "<p>") != 3 {
		t.Errorf("want 3 paragraphs, got %d: %.300s", strings.Count(out, "<p>"), out)
	}
}

func TestSanitizeResolvesRelativeURLs(t *testing.T) {
	in := `<article>
		<p><a href="/sport/football">rel</a>
		   <a href="https://other.example/x">abs</a></p>
		<img src="/img/photo.jpg" srcset="/img/photo.jpg 1x, /img/photo@2x.jpg 2x">
	</article>`
	arts := domx.FindElements(mustNode(t, in), "article")
	out := sanitize(arts[0], sanitizeOptions{BaseURL: "https://site.example/news/story", Prune: true})

	for _, want := range []string{
		`href="https://site.example/sport/football"`,
		`href="https://other.example/x"`,
		// The 2x candidate is the largest, so it is promoted to src.
		`src="https://site.example/img/photo@2x.jpg"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in: %.400s", want, out)
		}
	}
}

func TestCleanTracking(t *testing.T) {
	cases := map[string]string{
		"https://e.com/a?utm_source=x&utm_medium=y": "https://e.com/a",
		"https://e.com/a?fbclid=1":                  "https://e.com/a",
		"https://e.com/a?b=1&fbclid=1&c=2":          "https://e.com/a?b=1&c=2",
		"https://e.com/a#frag":                      "https://e.com/a",
		"https://e.com/a?":                          "https://e.com/a",
		"https://e.com/a?b=1&utm_source=x":          "https://e.com/a?b=1",
	}
	for in, want := range cases {
		if got := cleanTracking(in); got != want {
			t.Errorf("cleanTracking(%q) = %q, want %q", in, got, want)
		}
	}
}

// Wikipedia puts a description in JSON-LD `headline` and the real title in
// `name`, while og:title and <title> carry a " - Wikipedia" suffix. Trusting
// headline returned the description as the article title.
func TestPickTitlePrefersRealTitleOverDescription(t *testing.T) {
	cands := []titleCandidate{
		{value: "News aggregator - Wikipedia", source: "og"},
		{value: "News aggregator - Wikipedia", source: "title"},
		{value: "client software that aggregates syndicated web content", source: "jsonld-headline"},
		{value: "News aggregator", source: "jsonld-name"},
	}
	got := pickTitle(cands, "Wikimedia Foundation, Inc.")
	if got != "News aggregator" {
		t.Errorf("pickTitle = %q, want %q", got, "News aggregator")
	}
}

func TestPickTitleRejectsSentenceDescriptions(t *testing.T) {
	cands := []titleCandidate{
		{value: "This article is about the history of the feed reader.", source: "meta"},
		{value: "Feed reader history", source: "jsonld-name"},
	}
	if got := pickTitle(cands, ""); got != "Feed reader history" {
		t.Errorf("a full-sentence description won: %q", got)
	}
}

func TestPickTitleEmpty(t *testing.T) {
	if got := pickTitle(nil, "Example"); got != "" {
		t.Errorf("expected empty title, got %q", got)
	}
	if got := pickTitle([]titleCandidate{{value: "   "}}, "Example"); got != "" {
		t.Errorf("expected empty title, got %q", got)
	}
}

func TestStripSiteSuffix(t *testing.T) {
	cases := map[string]string{
		"Real Headline - Example": "Real Headline",
		"Real Headline | Example": "Real Headline",
		"Real Headline — Example": "Real Headline",
		"Real Headline":           "Real Headline",
		"Example - Example":       "Example",
		"Story about Example Inc": "Story about Example Inc",
	}
	for in, want := range cases {
		if got := stripSiteSuffix(in, "Example"); got != want {
			t.Errorf("stripSiteSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

// Pages embed markup fragments inside inline scripts and templates, and those
// fragments contain literal <title> and <meta> tags for social widgets. tirto.id
// ships a <title>Instagram</title> template, which was being used as the
// article title because metadata was read from the whole document.
func TestReadMetadataIgnoresMarkupInsideScripts(t *testing.T) {
	in := `<html lang="id"><head>
		<title>Tirto.ID - Jernih Mengalir Mencerahkan</title>
		<meta property="og:title" content="Tirto.ID - Jernih Mengalir Mencerahkan">
		</head><body>
		<script>
			var tpl = '<div class="widget"><title>Instagram</title>' +
				'<meta property="og:title" content="Instagram"></div>';
			var x = "<title>Facebook</title>";
		</script>
		<template><title>Twitter</title><meta name="description" content="tmpl desc"></template>
		</body></html>`
	m := readMetadata(mustNode(t, in))

	for _, bad := range []string{"Instagram", "Facebook", "Twitter", "tmpl desc"} {
		for _, c := range m.TitleCandidates() {
			if strings.Contains(c.value, bad) {
				t.Errorf("picked up %q from inside script/template: %+v", bad, m.TitleCandidates())
			}
		}
	}
	if m.Description == "tmpl desc" {
		t.Error("description came from inside a <template>")
	}
	if m.Lang != "id" {
		t.Errorf("lang = %q, want id (read from <html>, not <head>)", m.Lang)
	}
	if len(m.TitleCandidates()) == 0 {
		t.Error("no title candidates found at all")
	}
}

func TestFromHTMLFullArticle(t *testing.T) {
	body := strings.Repeat("<p>Kalimat berita yang cukup panjang untuk dihitung.</p>", 20)
	in := `<!doctype html><html lang="id"><head>
		<title>Judul Artikel Uji | Situs Berita</title>
		<meta property="og:title" content="Judul Artikel Uji">
		<meta property="og:site_name" content="Situs Berita">
		<meta name="author" content="Redaksi">
		<meta property="article:published_time" content="2026-03-04T05:06:07Z">
		<link rel="canonical" href="https://berita.example/artikel/1">
		<meta property="og:image" content="/img/cover.jpg">
		</head><body>
		<nav><a href="/a">menu</a></nav>
		<main><article><h1>Judul Artikel Uji</h1>` + body + `</article></main>
		<footer>copyright</footer>
		</body></html>`

	art, err := FromHTML([]byte(in), Options{PageURL: "https://berita.example/artikel/1", Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if art.Title != "Judul Artikel Uji" {
		t.Errorf("title = %q", art.Title)
	}
	if art.SiteName != "Situs Berita" {
		t.Errorf("site = %q", art.SiteName)
	}
	if art.Canonical != "https://berita.example/artikel/1" {
		t.Errorf("canonical = %q", art.Canonical)
	}
	if art.Lang != "id" {
		t.Errorf("lang = %q", art.Lang)
	}
	if art.Image != "https://berita.example/img/cover.jpg" {
		t.Errorf("image = %q", art.Image)
	}
	if art.Published.IsZero() {
		t.Error("published date was not parsed")
	} else if y := art.Published.UTC().Year(); y != 2026 {
		t.Errorf("published year = %d, want 2026", y)
	}
	if art.Strategy == "" {
		t.Error("no strategy recorded")
	}
	if art.WordCount < 100 {
		t.Errorf("word count = %d, want the body to be counted", art.WordCount)
	}
	if strings.Contains(art.TextContent, "copyright") || strings.Contains(art.TextContent, "menu") {
		t.Error("page chrome leaked into the article body")
	}
}

func TestFromHTMLJSONLDBody(t *testing.T) {
	ld := `{"@context":"https://schema.org","@type":"NewsArticle",
		"headline":"JSON-LD Headline","datePublished":"2026-01-02T03:04:05Z",
		"author":{"@type":"Person","name":"Someone"},
		"articleBody":"<p>` + strings.Repeat("Isi artikel dari JSON-LD. ", 30) + `</p>"}`
	in := `<html><head><title>x</title>
		<script type="application/ld+json">` + ld + `</script></head>
		<body><div>nothing useful here</div></body></html>`

	art, err := FromHTML([]byte(in), Options{PageURL: "https://e.example/a"})
	if err != nil {
		t.Fatal(err)
	}
	if art.Title != "JSON-LD Headline" {
		t.Errorf("title = %q", art.Title)
	}
	if art.Strategy != "jsonld" {
		t.Errorf("strategy = %q, want jsonld", art.Strategy)
	}
	if art.WordCount < 100 {
		t.Errorf("word count = %d, JSON-LD body was not used", art.WordCount)
	}
}

func TestFromHTMLRejectsNavigationOnlyPage(t *testing.T) {
	in := `<html><body><nav><a href="/1">one</a> <a href="/2">two</a></nav></body></html>`
	_, err := FromHTML([]byte(in), Options{PageURL: "https://e.example/"})
	if err == nil {
		t.Fatal("expected ErrNoContent for a navigation-only page")
	}
}

func TestFromHTMLRejectsFutureDate(t *testing.T) {
	// A template bug that reports a year 3000 must not be published.
	in := `<html><head><meta property="article:published_time" content="3000-01-01T00:00:00Z"></head>
		<body><article><p>` + strings.Repeat("x ", 200) + `</p></article></body></html>`
	art, err := FromHTML([]byte(in), Options{PageURL: "https://e.example/a", Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if !art.Published.IsZero() {
		t.Errorf("published = %v, want zero for an impossible future date", art.Published)
	}
}

func TestWordCountCountsCJK(t *testing.T) {
	// CJK text has no spaces, so whitespace splitting returns zero.
	if got := domx.WordCount("这是一段中文新闻内容用于测试分词功能是否正常工作"); got == 0 {
		t.Error("CJK text counted as zero words")
	}
}

var spaceRe = regexp.MustCompile(`\s+`)

func TestNormSpace(t *testing.T) {
	if got := domx.NormSpace("  a \n\t b  "); got != "a b" {
		t.Errorf("normSpace = %q, want %q", got, "a b")
	}
	if got := domx.NormSpace(spaceRe.ReplaceAllString("\u00a0a\u00a0", " ")); got != "a" {
		t.Errorf("normSpace should collapse non-breaking spaces, got %q", got)
	}
}

func TestPublishedFutureRejectedOnlyWhenAbsurd(t *testing.T) {
	// A date a day in the future is normal clock skew and must be kept.
	soon := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)
	in := `<html><head><meta property="article:published_time" content="` + soon + `"></head>
		<body><article><p>` + strings.Repeat("y ", 200) + `</p></article></body></html>`
	art, err := FromHTML([]byte(in), Options{PageURL: "https://e.example/a", Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if art.Published.IsZero() {
		t.Error("a near-future date should be kept, not discarded")
	}
}
