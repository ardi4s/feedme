package domx

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func mustParse(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func TestSelectAllCSSAndXPath(t *testing.T) {
	doc := mustParse(t, `
		<div class="card">
			<a href="/a">A</a>
			<a href="/b">B</a>
		</div>
		<div id="picked">
			<a href="/c">C</a>
		</div>`)

	nodes := SelectAll(doc, []string{".card a", "xpath://div[@id='picked']//a"})
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
	if got := TextOf(nodes[0]); NormSpace(got) != "A" {
		t.Errorf("first node = %q", NormSpace(got))
	}
}

// A hand-written site config with one bad selector must not blank out the
// others: a typo in a single field should not cost the user their whole feed.
func TestSelectAllSkipsUnusableEntries(t *testing.T) {
	doc := mustParse(t, `<a href="/a">A</a><a href="/b">B</a>`)
	nodes := SelectAll(doc, []string{
		"a[",         // invalid CSS
		"xpath://[[", // invalid XPath
		"xpath:",     // prefix with no expression
		"",           // empty
		"0",          // "0" is XPath's "no match" idiom, not a CSS tag
		"  a  ",      // padded but valid
		".does-not-exist",
	})
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
}

func TestSelectOneAndText(t *testing.T) {
	doc := mustParse(t, `<h1 class="t">  Judul   Artikel </h1><p>Isi</p>`)
	if got := SelectText(doc, []string{".t"}); got != "Judul Artikel" {
		t.Errorf("SelectText = %q", got)
	}
	if got := SelectText(doc, []string{".missing"}); got != "" {
		t.Errorf("missing selector should give empty string, got %q", got)
	}
	if n := SelectOne(doc, []string{".missing"}); n != nil {
		t.Error("missing selector should give nil")
	}
}

func TestSelectAttrResolvesRelative(t *testing.T) {
	doc := mustParse(t, `<div class="l"><a href="/news/1">one</a></div>
	                      <div class="l"><a href="https://other.example/x">two</a></div>`)
	got := SelectAttr(doc, []string{".l a"}, "href", "https://e.example/section/")
	if got != "https://e.example/news/1" {
		t.Errorf("SelectAttr = %q", got)
	}
}

// An empty href on the first match must not stop the search, or a placeholder
// link near the top of a card can hide the real one.
func TestSelectAttrSkipsEmptyValues(t *testing.T) {
	doc := mustParse(t, `<a class="l" href="">none</a><a class="l" href="/real">real</a>`)
	if got := SelectAttr(doc, []string{".l"}, "href", "https://e.example/"); got != "https://e.example/real" {
		t.Errorf("SelectAttr = %q", got)
	}
}

func TestSelectAllPreservesSelectorOrder(t *testing.T) {
	doc := mustParse(t, `<a class="b" href="/1">1</a><a class="a" href="/2">2</a>`)
	// Asking for .b then .a must return document order within each selector,
	// and the selectors' order overall.
	nodes := SelectAll(doc, []string{".b", ".a"})
	if len(nodes) != 2 {
		t.Fatalf("got %d", len(nodes))
	}
	if href := Attr(nodes[0], "href"); href != "/1" {
		t.Errorf("expected .b first, got %q", href)
	}
}

func TestNormSpace(t *testing.T) {
	cases := map[string]string{
		"  a   b  ": "a b",
		"a\n\tb":    "a b",
		"&nbsp;a":   "&nbsp;a", // entities are not decoded by NormSpace
		"":          "",
		"   ":       "",
		"a b":       "a b",
	}
	for in, want := range cases {
		if got := NormSpace(in); got != want {
			t.Errorf("NormSpace(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassAttrAndHasAttr(t *testing.T) {
	doc := mustParse(t, `<a class="one two three" href="/x" rel="noopener">t</a>`)
	link := FindElements(doc, "a")[0]
	classes := ClassAttr(link)
	if len(classes) != 3 {
		t.Fatalf("ClassAttr = %v", classes)
	}
	// Substring matching is the classic bug here: class "one" must not match a
	// selector for "on".
	n := FindByClassToken(doc, "one")
	if len(n) != 1 {
		t.Errorf("FindByClassToken(one) = %d nodes", len(n))
	}
	if len(FindByClassToken(doc, "on")) != 0 {
		t.Error("class token matched as a substring")
	}
	if len(FindWithClassLike(doc, "tw")) != 1 {
		t.Error("prefix match should find class 'two'")
	}
	if !HasAttr(link, "rel") {
		t.Error("HasAttr(rel) = false")
	}
	if HasAttr(link, "data-nope") {
		t.Error("HasAttr(data-nope) = true")
	}
}

func TestTextOfSkipsNonText(t *testing.T) {
	doc := mustParse(t, `<div>before<script>var x=1;</script>after</div>`)
	got := NormSpace(TextOf(FindElements(doc, "div")[0]))
	if strings.Contains(got, "var x") {
		t.Errorf("script contents leaked into text: %q", got)
	}
	if got != "beforeafter" {
		t.Errorf("TextOf = %q", got)
	}
}

func TestIsVisible(t *testing.T) {
	doc := mustParse(t, `
		<div id="hidden" style="display:none">x</div>
		<div id="hiddenattr" hidden>x</div>
		<div id="shown">x</div>
		<style>#also-hidden{display:none}</style>`)
	if IsVisible(FindByID(doc, "hidden")) {
		t.Error("display:none should be hidden")
	}
	if IsVisible(FindByID(doc, "hiddenattr")) {
		t.Error("hidden attribute should be hidden")
	}
	if !IsVisible(FindByID(doc, "shown")) {
		t.Error("plain div should be visible")
	}
}

// TextOf and RawTextOf answer different questions and must not be swapped:
// a JSON-LD article body lives inside a <script>, so reading it with TextOf
// finds nothing at all.
func TestTextOfVersusRawTextOf(t *testing.T) {
	doc := mustParse(t, `<div>a<script>var b=1;</script>c</div>`)
	div := FindElements(doc, "div")[0]
	if got := NormSpace(TextOf(div)); got != "ac" {
		t.Errorf("TextOf = %q, want rendered text only", got)
	}
	if got := NormSpace(RawTextOf(div)); got != "avar b=1;c" {
		t.Errorf("RawTextOf = %q, want every text node", got)
	}
	script := FindElements(doc, "script")[0]
	if got := NormSpace(RawTextOf(script)); got != "var b=1;" {
		t.Errorf("RawTextOf(script) = %q", got)
	}
}

func TestWordCountAndTruncateRunes(t *testing.T) {
	if got := WordCount(NormSpace("satu dua tiga")); got != 3 {
		t.Errorf("WordCount = %d", got)
	}
	// CJK text has no spaces, so a naive split reports one huge "word".
	if got := WordCount("これは日本語のテキストです"); got < 4 {
		t.Errorf("CJK WordCount = %d, expected per-character counting", got)
	}
	// The limit counts content runes; the ellipsis is added on top. Truncation
	// must not split a multi-byte rune.
	long := strings.Repeat("a", 100)
	if got := TruncateRunes(long, 10); got != "aaaaaaaaaa…" {
		t.Errorf("TruncateRunes = %q", got)
	}
	emoji := strings.Repeat("\U0001F44D", 20)
	if got := TruncateRunes(emoji, 5); got != strings.Repeat("\U0001F44D", 5)+"…" {
		t.Errorf("TruncateRunes on multibyte = %q", got)
	}
	// A string already within the limit is returned untouched, with no
	// ellipsis, or a short title would silently grow a character.
	if got := TruncateRunes("short", 10); got != "short" {
		t.Errorf("TruncateRunes within limit = %q", got)
	}
	if got := TruncateRunes("anything", 0); got != "anything" {
		t.Errorf("TruncateRunes(0) = %q", got)
	}
}

// TextOf and PlainText answer different questions, and a feed description needs
// the second one.
func TestPlainTextKeepsBlockBoundaries(t *testing.T) {
	doc := mustParse(t, `<h1>Judul</h1><p>Paragraf pertama.</p><p>Paragraf kedua.</p>`)
	if got := PlainText(doc); got != "Judul Paragraf pertama. Paragraf kedua." {
		t.Errorf("PlainText = %q", got)
	}
	if got := TextOf(doc); got != "JudulParagraf pertama.Paragraf kedua." {
		t.Errorf("TextOf = %q", got)
	}
}

func TestPlainTextInlineStaysJoined(t *testing.T) {
	// An inline element is not a line break, so no space is invented.
	doc := mustParse(t, `<p>Ekonomi <b>tumbuh</b> cepat</p>`)
	if got := PlainText(doc); got != "Ekonomi tumbuh cepat" {
		t.Errorf("PlainText = %q", got)
	}
}

func TestPlainTextSkipsInert(t *testing.T) {
	// A script is inline, so it must not invent a space next to it. The
	// paragraph is what supplies the boundary.
	doc := mustParse(t, `<p>Sebelum<script>var a=1</script><style>p{}</style>Sesudah</p>`)
	got := PlainText(doc)
	if strings.Contains(got, "var a=1") || strings.Contains(got, "p{}") {
		t.Errorf("inert content leaked: %q", got)
	}
	if got != "SebelumSesudah" {
		t.Errorf("PlainText = %q", got)
	}
	doc = mustParse(t, `<div>Sebelum<script>var a=1</script></div><div>Sesudah</div>`)
	if got := PlainText(doc); got != " Sebelum Sesudah" && got != "Sebelum Sesudah" {
		t.Errorf("PlainText = %q", got)
	}
}

func TestPlainTextOfNil(t *testing.T) {
	if got := PlainText(nil); got != "" {
		t.Errorf("PlainText(nil) = %q", got)
	}
}

func TestPlainTextBreaksListItems(t *testing.T) {
	doc := mustParse(t, `<ul><li>Satu</li><li>Dua</li><li>Tiga</li></ul>`)
	if got := PlainText(doc); got != "Satu Dua Tiga" {
		t.Errorf("PlainText = %q", got)
	}
}
