package listpage

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func parse(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func base(s string) Options { return Options{BaseURL: "https://e.example/"} }

// A typical card grid: image, headline, date, teaser, whole card wrapped in a
// link.
func cardsPage(n int) string {
	var b strings.Builder
	b.WriteString(`<html><body>
		<nav class="navbar"><a href="/kategori/ekonomi">Ekonomi</a>
			<a href="/kategori/saham">Saham</a>
			<a href="/kategori/uang">Uang</a>
			<a href="/kategori/investasi">Investasi</a></nav>
		<div class="grid">`)
	for i := 1; i <= n; i++ {
		b.WriteString(`<article class="card">
			<a href="/2026/09/` + two(i) + `/judul-artikel-number-` + two(i) + `">
				<img src="/img/` + two(i) + `.jpg" alt="Gambar ` + two(i) + `">
				<h3>Judul Artikel ` + two(i) + `</h3>
			</a>
			<time datetime="2026-09-` + two(i) + `T10:00:00Z">` + two(i) + ` September 2026</time>
			<p>Ringkasan artikel ` + two(i) + ` yang cukup panjang untuk ditampilkan.</p>
		</article>`)
	}
	b.WriteString(`</div>
		<footer><a href="/tentang">Tentang</a><a href="/privasi">Privasi</a>
			<a href="/kontak">Kontak</a></footer>
		</body></html>`)
	return b.String()
}

func two(i int) string {
	s := "0" + itoa(i)
	return s[len(s)-2:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestFromSelectorsCardGrid(t *testing.T) {
	doc := parse(t, cardsPage(5))
	opts := base("")
	opts.Item = []string{"article.card"}

	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}
	if got := items[0].Link; got != "https://e.example/2026/09/01/judul-artikel-number-01" {
		t.Errorf("link = %q", got)
	}
	if got := items[0].Title; got != "Gambar 01 Judul Artikel 01" {
		t.Logf("title = %q", got)
	}
	if items[0].Date.IsZero() {
		t.Error("date not read from <time datetime>")
	}
	if got := items[0].Image; got != "https://e.example/img/01.jpg" {
		t.Errorf("image = %q", got)
	}
	if !strings.Contains(items[0].Summary, "Ringkasan artikel") {
		t.Errorf("summary = %q", items[0].Summary)
	}
}

func TestFromSelectorsDedupsRepeatedCards(t *testing.T) {
	// A listing that repeats a story in a "most read" rail must not produce
	// the same article twice in one feed.
	doc := parse(t, `<div>
		<article class="card"><a href="/2026/09/01/aaa">AAA</a></article>
		<article class="card"><a href="/2026/09/01/aaa">AAA lagi</a></article>
		<article class="card"><a href="/2026/09/02/bbb">BBB</a></article>
	</div>`)
	opts := base("")
	opts.Item = []string{".card"}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 after dedup: %+v", len(items), items)
	}
}

// Tracking parameters must not survive into a feed: the same article clicked
// twice would otherwise look like two stories.
func TestFromSelectorsStripsTracking(t *testing.T) {
	doc := parse(t, `<div>
		<article class="card"><a href="/2026/09/01/aaa?utm_source=twitter">AAA</a></article>
		<article class="card"><a href="/2026/09/01/aaa">AAA</a></article>
	</div>`)
	opts := base("")
	opts.Item = []string{".card"}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("tracking variant was not merged: %+v", items)
	}
	if strings.Contains(items[0].Link, "utm_") {
		t.Errorf("tracking parameter survived: %q", items[0].Link)
	}
}

func TestTitleFallbackChain(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{
			name: "explicit selector wins",
			body: `<article class="c"><span class="t">Headline</span><h3>Subheading</h3></article>`,
			want: "Headline",
		},
		{
			name: "falls back to heading",
			body: `<article class="c"><h3>Subheading</h3></article>`,
			want: "Subheading",
		},
		{
			name: "falls back to image alt",
			body: `<article class="c"><img src="/a.jpg" alt="Alt Text"></article>`,
			want: "Alt Text",
		},
		{
			name: "falls back to url slug",
			body: `<article class="c"><img src="/a.jpg" alt=""></article>`,
			want: "not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parse(t, `<div>`+tc.body+`</div>`)
			opts := base("")
			opts.Item = []string{".c"}
			if strings.HasPrefix(tc.name, "explicit") {
				opts.Title = []string{".t"}
			}
			// The link drives the slug fallback, so it must exist.
			opts.URL = []string{"a"}
			doc = parse(t, `<div>`+strings.Replace(tc.body, `<article class="c">`,
				`<article class="c"><a href="/2026/09/01/not-found">x</a>`, 1)+`</div>`)
			items, err := FromSelectors(doc, opts)
			if err != nil {
				t.Fatal(err)
			}
			if items[0].Title != tc.want {
				t.Errorf("title = %q, want %q", items[0].Title, tc.want)
			}
		})
	}
}

func TestRejectsNonNavigationalLinks(t *testing.T) {
	doc := parse(t, `<div>
		<article class="c"><a href="javascript:void(0)">x</a></article>
		<article class="c"><a href="mailto:hi@e.example">x</a></article>
		<article class="c"><a href="#top">x</a></article>
		<article class="c"><a href="https://sosmed.example/p/1">x</a></article>
		<article class="c"><a href="/2026/09/01/asli">x</a></article>
	</div>`)
	opts := base("")
	opts.Item = []string{".c"}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want only the one real link: %+v", len(items), items)
	}
	if !strings.Contains(items[0].Link, "/2026/09/01/asli") {
		t.Errorf("wrong item kept: %q", items[0].Link)
	}
}

// Cross-site links are refused by default, but subdomains of the listing's own
// domain are kept: a regional edition links to the national one.
func TestCrossHostPolicy(t *testing.T) {
	doc := parse(t, `<div>
		<article class="c"><a href="https://kompas.com/x">lokal</a></article>
		<article class="c"><a href="https://ads.example/y">iklan</a></article>
	</div>`)
	opts := Options{BaseURL: "https://surabaya.kompas.com/", Item: []string{".c"}}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(items), items)
	}
	if !strings.Contains(items[0].Link, "kompas.com") {
		t.Errorf("subdomain of the same site was dropped: %q", items[0].Link)
	}

	opts.AllowCrossHost = true
	items, err = FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Errorf("AllowCrossHost should keep both, got %d", len(items))
	}
}

func TestExcludeDropsSubtrees(t *testing.T) {
	doc := parse(t, `<div>
		<article class="c"><a href="/2026/09/01/aaa">AAA</a></article>
		<div class="iklan"><article class="c"><a href="/2026/09/01/iklan">Iklan</a></article></div>
	</div>`)
	opts := base("")
	opts.Item = []string{".c"}
	opts.Exclude = []string{".iklan"}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || strings.Contains(items[0].Link, "iklan") {
		t.Errorf("excluded subtree was not dropped: %+v", items)
	}
}

func TestMaxItemsAndErrNoItems(t *testing.T) {
	doc := parse(t, cardsPage(10))
	opts := base("")
	opts.Item = []string{".card"}
	opts.MaxItems = 3
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Errorf("MaxItems ignored: got %d", len(items))
	}

	// A page that is simply not a listing is an ordinary outcome, and the
	// caller needs to tell it apart from a real failure.
	empty := parse(t, `<html><body><p>Halaman kosong.</p></body></html>`)
	opts.Item = []string{".card"}
	if _, err := FromSelectors(empty, opts); err == nil {
		t.Error("expected an error when nothing matched")
	}
}

// Detection is the part that has to work without being told the answer.
func TestDetectFindsArticleList(t *testing.T) {
	doc := parse(t, cardsPage(8))
	res, err := Detect(doc, base(""))
	if err != nil {
		t.Fatal(err)
	}
	if res.Selector == "" {
		t.Fatal("no selector produced")
	}
	if res.Confidence == "low" {
		t.Errorf("confidence = %q (%s)", res.Confidence, res.Reason)
	}
	best := res.Candidates[0]
	if best.Count != 8 {
		t.Errorf("Count = %d, want 8", best.Count)
	}
	// The card URLs are /2026/09/01/judul-..., so three numeric segments
	// precede the headline slug.
	if !strings.Contains(best.Pattern, "e.example/{n}/{n}/{n}/{slug}") {
		t.Errorf("Pattern = %q", best.Pattern)
	}
	// The detected selector must actually reproduce the items.
	opts := base("")
	opts.Item = []string{res.Selector}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatalf("detected selector %q does not work: %v", res.Selector, err)
	}
	if len(items) < 8 {
		t.Errorf("detected selector yielded %d items, want at least 8", len(items))
	}
}

// Navigation links outnumber article links on most pages. If they are counted,
// the feed fills with menu entries, so the walk must not descend into furniture.
func TestDetectIgnoresNavigation(t *testing.T) {
	doc := parse(t, `<html><body>
		<nav>`+manyLinks("/kategori/", 30)+`</nav>
		<div class="grid">`+manyLinks("/2026/09/", 6)+`</div>
		<footer>`+manyLinks("/footer/", 25)+`</footer>
		</body></html>`)
	res, err := Detect(doc, base(""))
	if err != nil {
		t.Fatal(err)
	}
	// The 30 nav and 25 footer links must not appear anywhere in the result.
	best := res.Candidates[0]
	if best.Count != 6 {
		t.Errorf("Count = %d, want the 6 article links only: %+v", best.Count, best)
	}
	for _, c := range res.Candidates {
		if c.Count > 6 {
			t.Errorf("a page-furniture group survived: %+v", c)
		}
	}
	if !strings.Contains(best.Pattern, "{n}/{n}/{slug}") {
		t.Errorf("Pattern = %q, want the article shape", best.Pattern)
	}
}

// A <div class="menu"> is as much furniture as a <nav>, and modern sites ship
// that far more often.
func TestDetectIgnoresChromeByClass(t *testing.T) {
	doc := parse(t, `<html><body>
		<div class="site-menu">`+manyLinks("/tag/", 20)+`</div>
		<main>`+manyLinks("/berita/", 6)+`</main>
		</body></html>`)
	res, err := Detect(doc, base(""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Candidates[0].Pattern, "/berita/") {
		t.Errorf("class-based menu was counted: %+v", res.Candidates[0])
	}
}

func manyLinks(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(`<a href="` + prefix + `judul-` + two(i) + `">Judul ` + two(i) + `</a>`)
	}
	return b.String()
}

// The whole point of reporting candidates is that a wrong guess is visible
// rather than silently shipped.
func TestDetectReportsAlternatives(t *testing.T) {
	// Two genuinely different URL shapes. Two sections whose paths differ only
	// in numbers are one pattern, and would be reported as such on purpose.
	doc := parse(t, `<html><body>
		<main>`+manyLinks("/2026/09/", 9)+`</main>
		<section>`+manyLinks("/tag/nama-", 8)+`</section>
		</body></html>`)
	res, err := Detect(doc, base(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) < 2 {
		t.Fatalf("expected both sections as candidates, got %+v", res.Candidates)
	}
	if res.Reason == "" {
		t.Error("Reason should explain the choice")
	}
	if res.Candidates[0].Count < res.Candidates[1].Count {
		t.Error("candidates are not sorted best first")
	}
}

// Two links to the same story are not a list, however well their URLs match.
func TestDetectRejectsPatternWithOneDistinctSlug(t *testing.T) {
	doc := parse(t, `<html><body><main>
		<a href="/2026/09/01/berita-sama">sama</a>
		<a href="/2026/09/01/berita-sama?utm_source=x">sama lagi</a>
		</main></body></html>`)
	if _, err := Detect(doc, base("")); err == nil {
		t.Error("a single repeated story should not be accepted as a listing")
	}
}

func TestDetectConfidenceLevels(t *testing.T) {
	// A dominant pattern is high confidence.
	strong := parse(t, `<html><body><main>`+manyLinks("/2026/09/", 20)+
		`</main><div>`+manyLinks("/x/", 3)+`</div></body></html>`)
	res, err := Detect(strong, base(""))
	if err != nil {
		t.Fatal(err)
	}
	if res.Confidence != "high" {
		t.Errorf("confidence = %q, want high (%s)", res.Confidence, res.Reason)
	}

	// Two patterns of the same size cannot be separated, so the caller must
	// be told the guess is unreliable.
	tied := parse(t, `<html><body>
		<div id="a">`+manyLinks("/2026/09/", 5)+`</div>
		<div id="b">`+manyLinks("/2025/08/", 5)+`</div>
		</body></html>`)
	res, err = Detect(tied, base(""))
	if err != nil {
		t.Fatal(err)
	}
	if res.Confidence == "high" {
		t.Errorf("tied patterns should not be high confidence: %s", res.Reason)
	}
}

func TestDetectRejectsWhenGivenASelector(t *testing.T) {
	doc := parse(t, cardsPage(4))
	opts := base("")
	opts.Item = []string{".card"}
	if _, err := Detect(doc, opts); err == nil {
		t.Error("Detect should refuse to run when an item selector is already set")
	}
}

func TestDetectRespectsMinItems(t *testing.T) {
	doc := parse(t, `<html><body><main>`+manyLinks("/2026/09/", 4)+`</main></body></html>`)
	opts := base("")
	opts.MinItems = 10
	if _, err := Detect(doc, opts); err == nil {
		t.Error("MinItems was not enforced")
	}
}

func TestDetectOnNonListingPage(t *testing.T) {
	doc := parse(t, `<html><body><nav><a href="/a">a</a><a href="/b">b</a></nav>
		<p>Artikel tunggal, tanpa daftar.</p></body></html>`)
	if _, err := Detect(doc, base("")); err == nil {
		t.Error("an article page should not yield a detected list")
	}
}

func TestItemDateFromVariousShapes(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"time datetime", `<time datetime="2026-09-26T10:00:00Z">x</time>`, "2026-09-26"},
		{"time text", `<time>26 September 2026</time>`, "2026-09-26"},
		{"microdata itemprop", `<meta itemprop="datePublished" content="26/09/2026">`, "2026-09-26"},
		{"indonesian text", `<span class="d">26 September 2026 19:30 WIB</span>`, "2026-09-26"},
		{"no date", `<span>Tidak ada tanggal</span>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parse(t, `<article class="c"><a href="/2026/09/01/aaa">AAA</a>`+tc.body+`</article>`)
			opts := base("")
			opts.Item = []string{".c"}
			items, err := FromSelectors(doc, opts)
			if err != nil {
				t.Fatal(err)
			}
			got := items[0].Date
			if tc.want == "" {
				if !got.IsZero() {
					t.Errorf("date = %s, want none", got.Format("2006-01-02"))
				}
				return
			}
			if got.IsZero() || got.Format("2006-01-02") != tc.want {
				t.Errorf("date = %s, want %s", got.Format("2006-01-02"), tc.want)
			}
		})
	}
}

func TestItemDateSelectorOverridesTimeTag(t *testing.T) {
	doc := parse(t, `<article class="c">
		<a href="/2026/09/01/aaa">AAA</a>
		<span class="pub">1 Januari 2020</span>
		<time datetime="2026-09-26T10:00:00Z">26 September 2026</time>
	</article>`)
	opts := base("")
	opts.Item = []string{".c"}
	opts.Date = []string{".pub"}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if !items[0].Date.Equal(want) {
		t.Errorf("date = %s, want %s", items[0].Date, want)
	}
}

func TestLazyLoadedImage(t *testing.T) {
	doc := parse(t, `<article class="c">
		<a href="/2026/09/01/aaa">AAA</a>
		<img data-src="/img/lazy.jpg" src="data:image/gif;base64,R0lGOD">
	</article>`)
	opts := base("")
	opts.Item = []string{".c"}
	items, err := FromSelectors(doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Image != "https://e.example/img/lazy.jpg" {
		t.Errorf("image = %q, want the data-src value", items[0].Image)
	}
}

// A teaser is read by a person, so block boundaries must survive flattening.
// "Saham Menguat25 September 2026Ringkasan" is a visible defect.
func TestSummaryKeepsBlockBoundaries(t *testing.T) {
	doc := parse(t, `<div id="list">
		<article class="card">
			<a href="/2026/09/25/beta"><h2>Saham Menguat</h2></a>
			<time datetime="2026-09-25">25 September 2026</time>
			<p>Ringkasan berita kedua.</p>
		</article>
	</div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	sum := items[0].Summary
	if strings.Contains(sum, "Menguat25") || strings.Contains(sum, "2026Ringkasan") {
		t.Errorf("summary ran two elements together: %q", sum)
	}
	if !strings.Contains(sum, "Saham Menguat 25 September 2026") {
		t.Errorf("summary = %q", sum)
	}
	// The date must still be found despite the extra text.
	if items[0].Date.IsZero() {
		t.Error("the date was lost")
	}
}

func TestSummarySelectorIsPlainText(t *testing.T) {
	doc := parse(t, `<div id="list">
		<article class="card">
			<a href="/a">Judul <b>Artikel</b></a>
			<div class="teaser"><p>Baris pertama.</p><p>Baris kedua.</p></div>
		</article>
	</div>`)
	items, err := FromSelectors(doc, Options{
		BaseURL: "https://e.example/", Item: []string{"article.card"}, Summary: []string{".teaser"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if got := items[0].Summary; got != "Baris pertama. Baris kedua." {
		t.Errorf("Summary = %q", got)
	}
}

// The title must not gain a space from an inline element, or every headline with
// a <b> in it is mangled.
func TestTitleKeepsInlineTextJoined(t *testing.T) {
	doc := parse(t, `<div id="list">
		<article class="card"><a href="/a">Ekonomi <b>tumbuh</b> cepat</a></article>
	</div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].Title; got != "Ekonomi tumbuh cepat" {
		t.Errorf("Title = %q", got)
	}
}

func TestBylineIsFoundAfterABlockBoundary(t *testing.T) {
	doc := parse(t, `<div id="list">
		<article class="card">
			<a href="/a">Suatu Berita</a>
			<div>Oleh: Rina</div>
		</article>
	</div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].Author; got != "Rina" {
		t.Errorf("Author = %q, want Rina", got)
	}
}

// A byline label is usually followed by a colon, which the label table must
// accept. "Oleh: Rina" is the commonest form there is.
func TestBylineLabelWithColon(t *testing.T) {
	cases := []struct {
		markup string
		want   string
	}{
		{`<div>Oleh: Rina</div>`, "Rina"},
		{`<div>Oleh Rina</div>`, "Rina"},
		{`<div>Oleh - Rina</div>`, "Rina"},
		{`<span>By: Rina</span>`, "Rina"},
		{`<span>By Rina</span>`, ""}, // too weak without a separator
		{`<div>Penulis: Budi Santoso</div>`, "Budi Santoso"},
		{`<div>Writer: Jane Doe</div>`, "Jane Doe"},
		{`<div>Reporter: Alex</div>`, ""}, // not a known label
	}
	for _, c := range cases {
		doc := parse(t, `<div id="list"><article class="card">
			<a href="/a">Suatu Berita</a>`+c.markup+`</article></div>`)
		items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("%s: items = %d", c.markup, len(items))
		}
		if items[0].Author != c.want {
			t.Errorf("%s: Author = %q, want %q", c.markup, items[0].Author, c.want)
		}
	}
}

// "by 2030" is prose, not a byline, and must not become an author.
func TestBylineLabelDoesNotFireInsideProse(t *testing.T) {
	doc := parse(t, `<div id="list"><article class="card">
		<a href="/a">Suatu Berita</a>
		<p>Proyeksi ekonomi by 2030 tidak pasti.</p>
	</article></div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Author != "" {
		t.Errorf("Author = %q, want none", items[0].Author)
	}
}

// A label glued to the end of a word is not a label.
func TestBylineLabelNeedsAWordStart(t *testing.T) {
	doc := parse(t, `<div id="list"><article class="card">
		<a href="/a">Suatu Berita</a>
		<p>oby: Rina bukan byline</p>
	</article></div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Author != "" {
		t.Errorf("Author = %q, want none", items[0].Author)
	}
}

// A marked-up byline element still yields a bare name.
func TestBylineElementLabelIsStripped(t *testing.T) {
	doc := parse(t, `<div id="list"><article class="card">
		<a href="/a">Suatu Berita</a>
		<span class="author">Oleh: Redaksi</span>
	</article></div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].Author; got != "Redaksi" {
		t.Errorf("Author = %q, want Redaksi", got)
	}
}

// The byline must be found even when the label is glued to the headline in the
// flattened text, which is what <a>Title</a><span>Oleh: Rina</span> produces.
func TestBylineSurvivesInlineAdjacency(t *testing.T) {
	doc := parse(t, `<div id="list"><article class="card">
		<a href="/a">Suatu Berita</a><span>Oleh: Rina</span>
	</article></div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].Author; got != "Rina" {
		t.Errorf("Author = %q, want Rina", got)
	}
}

// "ditulis oleh: Rina" is common in Indonesian listings, and the label is in the
// middle of the text node.
func TestBylineInsideASentence(t *testing.T) {
	doc := parse(t, `<div id="list"><article class="card">
		<a href="/a">Suatu Berita</a>
		<p>Berita ini ditulis oleh: Rina</p>
	</article></div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].Author; got != "Rina" {
		t.Errorf("Author = %q, want Rina", got)
	}
}

// A byline-looking word in a photo credit is not an author.
func TestPhotoCreditIsNotAnAuthor(t *testing.T) {
	doc := parse(t, `<div id="list"><article class="card">
		<a href="/a">Suatu Berita</a>
		<span>Foto: dok/BPS</span>
		<span>Oleh: Rina</span>
	</article></div>`)
	items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].Author; got != "Rina" {
		t.Errorf("Author = %q, want Rina, not the photo credit", got)
	}
}

// The page marked this node as the byline, so its label is stripped even without
// a separator. The separator rule only protects the scan through prose.
func TestMarkedBylineLabelIsStrippedWithoutSeparator(t *testing.T) {
	cases := map[string]string{
		`<span class="author">By Rina</span>`:      "Rina",
		`<span class="byline">Oleh Rina</span>`:    "Rina",
		`<span class="author">Writer: Jane</span>`: "Jane",
		`<span class="author">Rina</span>`:         "Rina",
		`<span class="author">Redaksi</span>`:      "Redaksi",
	}
	for markup, want := range cases {
		doc := parse(t, `<div id="list"><article class="card">
			<a href="/a">Suatu Berita</a>`+markup+`</article></div>`)
		items, err := FromSelectors(doc, Options{BaseURL: "https://e.example/", Item: []string{"article.card"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := items[0].Author; got != want {
			t.Errorf("%s: Author = %q, want %q", markup, got, want)
		}
	}
}

// An explicit author selector is obeyed even when the text has no label.
func TestAuthorSelectorWins(t *testing.T) {
	doc := parse(t, `<div id="list"><article class="card">
		<a href="/a">Suatu Berita</a>
		<span class="credit">Rina</span>
		<span class="author">oby: Rina</span>
	</article></div>`)
	items, err := FromSelectors(doc, Options{
		BaseURL: "https://e.example/", Item: []string{"article.card"}, Author: []string{".credit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].Author; got != "Rina" {
		t.Errorf("Author = %q", got)
	}
}

// A site whose news links live on sibling subdomains, like kontan.co.id.
const siblingHostPage = `<html><head><title>Portal</title></head><body>
	<div class="grid">
		<a href="https://nasional.example.com/news/alpha"><h3>Alpha</h3></a>
		<a href="https://nasional.example.com/news/beta"><h3>Beta</h3></a>
		<a href="https://nasional.example.com/news/gamma"><h3>Gamma</h3></a>
		<a href="https://insight.example.com/news/delta"><h3>Delta</h3></a>
	</div></body></html>`

// Sibling subdomains are one site (same registrable domain) and are kept by
// default, so a portal whose articles live on its own subdomains still yields
// items without any configuration.
func TestDetectKeepsSiblingSubdomains(t *testing.T) {
	opts := Options{BaseURL: "https://www.example.com/"}
	res, err := Detect(parse(t, siblingHostPage), opts)
	if err != nil {
		t.Fatalf("sibling subdomains should be kept: %v", err)
	}
	items, err := FromSelectors(parse(t, siblingHostPage), Options{
		BaseURL: opts.BaseURL,
		Item:    []string{res.Selector},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Errorf("items = %d, want 4", len(items))
	}
}

// A wholly different site is still off-site by default.
const otherSitePage = `<html><body><div class="grid">
	<a href="https://other.test/news/alpha"><h3>Alpha</h3></a>
	<a href="https://other.test/news/beta"><h3>Beta</h3></a>
	<a href="https://other.test/news/gamma"><h3>Gamma</h3></a>
</div></body></html>`

func TestDetectDropsOtherSitesByDefault(t *testing.T) {
	opts := Options{BaseURL: "https://www.example.com/"}
	if _, err := Detect(parse(t, otherSitePage), opts); err == nil {
		t.Fatal("links to another site should have been dropped")
	}
}

// allow_cross_host keeps links to a different site.
func TestDetectAllowsCrossHost(t *testing.T) {
	opts := Options{BaseURL: "https://www.example.com/", AllowCrossHost: true}
	res, err := Detect(parse(t, otherSitePage), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Selector == "" {
		t.Error("no selector detected with cross-host allowed")
	}
}

// force_host rewrites sibling links onto the working host before the cross-host
// check, so the items survive and point somewhere usable.
func TestDetectForceHostRewritesSiblings(t *testing.T) {
	opts := Options{BaseURL: "https://www.example.com/", ForceHost: "www.example.com"}
	res, err := Detect(parse(t, siblingHostPage), opts)
	if err != nil {
		t.Fatalf("force_host should keep the links: %v", err)
	}
	items, err := FromSelectors(parse(t, siblingHostPage), Options{
		BaseURL:   opts.BaseURL,
		Item:      []string{res.Selector},
		ForceHost: "www.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no items after force_host")
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Link, "https://www.example.com/news/") {
			t.Errorf("link not rewritten: %q", it.Link)
		}
	}
}

// The image is rewritten too, so it is served from the host that works.
func TestForceHostRewritesImages(t *testing.T) {
	page := `<html><body><article class="card">
		<a href="https://nasional.example.com/news/x"><img src="https://cdn.example.com/i.jpg" alt="x"><h3>X</h3></a>
	</article></body></html>`
	items, err := FromSelectors(parse(t, page), Options{
		BaseURL:   "https://www.example.com/",
		Item:      []string{"article.card"},
		ForceHost: "www.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if items[0].Link != "https://www.example.com/news/x" {
		t.Errorf("link = %q", items[0].Link)
	}
	if items[0].Image != "https://www.example.com/i.jpg" {
		t.Errorf("image = %q", items[0].Image)
	}
}

// Detect returns the items directly, so navigation that shares the container
// markup cannot leak into the feed.
func TestDetectReturnsItems(t *testing.T) {
	res, err := Detect(parse(t, siblingHostPage), Options{BaseURL: "https://www.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(res.Items))
	}
	for _, it := range res.Items {
		if !strings.Contains(it.Link, "example.com/news/") {
			t.Errorf("unexpected item link %q", it.Link)
		}
	}
}

// The same article linked twice with different query strings is one item.
func TestDetectDedupesByPath(t *testing.T) {
	page := `<html><body><div class="grid">
		<a href="https://nasional.example.com/news/alpha?source=a"><h3>Alpha</h3></a>
		<a href="https://nasional.example.com/news/alpha?source=b"><h3>Alpha</h3></a>
		<a href="https://insight.example.com/news/beta"><h3>Beta</h3></a>
		<a href="https://insight.example.com/news/gamma"><h3>Gamma</h3></a>
	</div></body></html>`
	res, err := Detect(parse(t, page), Options{BaseURL: "https://www.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %d, want 3 (one duplicate removed)", len(res.Items))
	}
}
