package feed

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"feedme/internal/listpage"
)

var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// testFeed returns a valid feed with no entries, for tests that care only about
// one entry they add themselves.
func testFeed() Feed { return Build(spec()) }

// render serialises a feed and fails the test if it cannot.
func render(t *testing.T, f Feed, format Format) string {
	t.Helper()
	return string(mustBytes(t, f, format))
}

func mustBytes(t *testing.T, f Feed, format Format) []byte {
	t.Helper()
	out, err := Bytes(f, format)
	if err != nil {
		t.Fatalf("render %s: %v", format, err)
	}
	return out
}

func sampleItems() []listpage.Item {
	return []listpage.Item{
		{
			Link:    "https://e.example/2026/09/25/second",
			Title:   "Second & story",
			Summary: "Ringkasan artikel kedua.",
			Date:    time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC),
			Image:   "https://e.example/img/second.jpg",
		},
		{
			Link:    "https://e.example/2026/09/26/first",
			Title:   "First story",
			Summary: "Ringkasan artikel pertama.",
			Date:    time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC),
		},
	}
}

func spec() Spec {
	return Spec{
		Title:       "Contoh Feed",
		Link:        "https://e.example/",
		Description: "Uji coba",
		Language:    "id",
		Items:       sampleItems(),
		Now:         now,
	}
}

func TestValidFormats(t *testing.T) {
	for _, f := range []Format{FormatRSS, FormatAtom, FormatJSON} {
		if !Valid(f) {
			t.Errorf("%q should be valid", f)
		}
	}
	for _, f := range []Format{"", "html", "RSS"} {
		if Valid(f) {
			t.Errorf("%q should not be valid", f)
		}
	}
}

// A title containing an ampersand must not produce a document that every reader
// rejects. This is the single most common way a hand-built feed breaks.
func TestRSSEscapesTitles(t *testing.T) {
	out, err := Bytes(Build(spec()), FormatRSS)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "Second &amp; story") {
		t.Errorf("ampersand not escaped:\n%s", s)
	}
	var doc rss2
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid XML: %v\n%s", err, s)
	}
	// Items are sorted newest first, so the ampersand title is the second one.
	if doc.Channel.Items[1].Title != "Second & story" {
		t.Errorf("round trip lost the title: %q", doc.Channel.Items[1].Title)
	}
}

// A feed whose items are out of date order reads as broken, because readers show
// them in document order.
func TestSortsNewestFirst(t *testing.T) {
	f := Build(spec())
	if len(f.Entries) != 2 {
		t.Fatalf("entries = %d", len(f.Entries))
	}
	if f.Entries[0].Title != "First story" {
		t.Errorf("newest first expected, got %q", f.Entries[0].Title)
	}
}

func TestSortIsStableWhenDatesTie(t *testing.T) {
	s := spec()
	s.Items = []listpage.Item{
		{Link: "https://e.example/a", Title: "A", Date: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)},
		{Link: "https://e.example/b", Title: "B", Date: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)},
	}
	f := Build(s)
	if f.Entries[0].Title != "A" || f.Entries[1].Title != "B" {
		t.Errorf("equal dates must keep input order, got %q then %q", f.Entries[0].Title, f.Entries[1].Title)
	}
}

// A page where nothing has a date must not be shuffled.
func TestNoDatesLeavesOrderAlone(t *testing.T) {
	s := spec()
	s.Items = []listpage.Item{
		{Link: "https://e.example/z", Title: "Z"},
		{Link: "https://e.example/a", Title: "A"},
	}
	f := Build(s)
	if f.Entries[0].Title != "Z" {
		t.Errorf("order changed without dates: %q", f.Entries[0].Title)
	}
}

func TestUnknownDateIsOmittedNotZero(t *testing.T) {
	s := spec()
	s.Items = []listpage.Item{{Link: "https://e.example/a", Title: "Tanpa tanggal"}}
	f := Build(s)
	out, err := Bytes(f, FormatRSS)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "0001") {
		t.Errorf("zero time leaked into the feed:\n%s", out)
	}
}

func TestGUIDModes(t *testing.T) {
	link := Build(spec())
	if link.Entries[0].GUID != "https://e.example/2026/09/26/first" {
		t.Errorf("link GUID = %q", link.Entries[0].GUID)
	}

	s := spec()
	s.GUID = GUIDStable
	stable := Build(s)
	if !strings.HasPrefix(stable.Entries[0].GUID, "sha256:") {
		t.Errorf("stable GUID = %q", stable.Entries[0].GUID)
	}
	// The same input must always give the same identifier, or a reader treats
	// every rebuild as a fresh feed.
	if Build(s).Entries[0].GUID != stable.Entries[0].GUID {
		t.Error("stable GUID is not deterministic")
	}

	s.GUID = GUIDTitle
	if Build(s).Entries[0].GUID != "First story" {
		t.Errorf("title GUID = %q", Build(s).Entries[0].GUID)
	}
}

// A tracking parameter must not make an article look like a new one.
func TestGUIDNormalisesLinks(t *testing.T) {
	s := spec()
	s.Items = []listpage.Item{{
		Link:  "https://e.example/a?utm_source=twitter",
		Title: "A",
		Date:  time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC),
	}}
	if got := Build(s).Entries[0].GUID; got != "https://e.example/a" {
		t.Errorf("GUID = %q, want the normalised link", got)
	}
}

func TestFullTextBody(t *testing.T) {
	s := spec()
	s.FullText = true
	s.Content = map[string]string{
		"https://e.example/2026/09/26/first": "<p>Paragraf pertama.</p><p>Paragraf kedua.</p>",
	}
	f := Build(s)
	var first *Entry
	for i := range f.Entries {
		if f.Entries[i].Title == "First story" {
			first = &f.Entries[i]
		}
	}
	if first == nil {
		t.Fatal("entry not found")
	}
	if first.ContentHTML == "" {
		t.Fatal("full text body not attached")
	}
	if !strings.Contains(first.Summary, "Paragraf") {
		t.Errorf("summary should be derived from the body, got %q", first.Summary)
	}
	// The item with no fetched body falls back to its teaser rather than
	// becoming blank.
	for _, e := range f.Entries {
		if e.Title == "Second story" && e.Summary == "" {
			t.Error("item without fetched body should keep its teaser")
		}
	}
}

func TestEntryNeverHasEmptyDescription(t *testing.T) {
	s := spec()
	s.Items = []listpage.Item{{Link: "https://e.example/a", Title: "Kosong"}}
	f := Build(s)
	if f.Entries[0].Summary == "" {
		t.Error("an entry with no text at all should still get a description")
	}
}

func TestTitleFallsBackToSlugThenLink(t *testing.T) {
	s := spec()
	s.Items = []listpage.Item{{Link: "https://e.example/2026/09/26/judul-artikel-kosong"}}
	if got := Build(s).Entries[0].Title; got != "judul artikel kosong" {
		t.Errorf("title = %q, want the slug", got)
	}
	s.Items = []listpage.Item{{Link: "https://e.example/"}}
	if got := Build(s).Entries[0].Title; got == "" {
		t.Error("title should fall back to the link")
	}
}

func TestFeedTitleAndDescriptionDefaults(t *testing.T) {
	s := spec()
	s.Title = ""
	s.Description = ""
	f := Build(s)
	if f.Title == "" {
		t.Error("feed title should never be empty")
	}
	out, _ := Bytes(f, FormatRSS)
	if !strings.Contains(string(out), "<description>") {
		t.Errorf("RSS requires a channel description:\n%s", out)
	}
}

func TestUpdatedDerivedFromNewestItem(t *testing.T) {
	f := Build(spec())
	want := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	if !f.Updated.Equal(want) {
		t.Errorf("Updated = %s, want %s", f.Updated, want)
	}
	// With no dated items the build time is used, so the field is never absent.
	s := spec()
	s.Items = []listpage.Item{{Link: "https://e.example/a", Title: "A"}}
	if got := Build(s).Updated; !got.After(time.Time{}) {
		t.Errorf("Updated = %s, want a real time", got)
	}
}

func TestRenderAllFormats(t *testing.T) {
	f := Build(spec())
	for _, format := range Formats {
		out, err := Bytes(f, format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if len(out) == 0 {
			t.Errorf("%s produced nothing", format)
		}
		switch format {
		case FormatRSS:
			var doc rss2
			if err := xml.Unmarshal(out, &doc); err != nil {
				t.Errorf("rss is not valid xml: %v", err)
			}
			if doc.Version != "2.0" {
				t.Errorf("version = %q", doc.Version)
			}
			if len(doc.Channel.Items) != 2 {
				t.Errorf("rss items = %d", len(doc.Channel.Items))
			}
		case FormatAtom:
			if !strings.Contains(string(out), `xmlns="http://www.w3.org/2005/Atom"`) {
				t.Errorf("atom namespace missing:\n%s", out)
			}
		case FormatJSON:
			var doc map[string]any
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Errorf("json is not valid: %v", err)
			}
			if doc["version"] != "https://jsonfeed.org/version/1.1" {
				t.Errorf("json version = %v", doc["version"])
			}
			items, ok := doc["items"].([]any)
			if !ok || len(items) != 2 {
				t.Errorf("json items = %v", doc["items"])
			}
		}
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	if _, err := Bytes(Build(spec()), "html"); err == nil {
		t.Error("an unknown format should be an error")
	}
}

func TestJSONItemsIsAlwaysAnArray(t *testing.T) {
	// A feed with no items must serialise "items": [] rather than null, which
	// some readers reject.
	s := spec()
	s.Items = nil
	out, err := Bytes(Build(s), FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"items": []`) {
		t.Errorf("items should be an empty array:\n%s", out)
	}
}

func TestAtomEntryHasRequiredFields(t *testing.T) {
	// Atom rejects an entry without id or updated, and the zero time is not a
	// usable value.
	s := spec()
	s.Items = []listpage.Item{{Link: "https://e.example/a", Title: "Tanpa tanggal"}}
	out, err := Bytes(Build(s), FormatAtom)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if !strings.Contains(body, "<id>https://e.example/a</id>") {
		t.Errorf("entry id missing:\n%s", body)
	}
	// The entry borrows the feed's build time. The Unix epoch is deliberately
	// avoided: it is a real-looking date, and a reader that sorts on it would
	// bury the item permanently.
	if strings.Contains(body, "1970-01-01") {
		t.Errorf("the epoch must not stand in for an unknown date:\n%s", body)
	}
	if !strings.Contains(body, "<updated>2026-09-26T12:00:00Z</updated>") {
		t.Errorf("entry updated should carry the feed's build time:\n%s", body)
	}
}

// An unknown date must be omitted rather than filled in. This is the JSON Feed
// half of the same rule the Atom test above covers.
func TestJSONOmitsUnknownDates(t *testing.T) {
	s := spec()
	s.Items = []listpage.Item{{Link: "https://e.example/a", Title: "Tanpa tanggal"}}
	out, err := Bytes(Build(s), FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if strings.Contains(body, "date_modified") || strings.Contains(body, "date_published") {
		t.Errorf("an unknown date must be absent, not invented:\n%s", body)
	}
	if strings.Contains(body, "1970-01-01") {
		t.Errorf("the epoch must not appear:\n%s", body)
	}
}

func TestJSONKeepsKnownDates(t *testing.T) {
	body := string(mustBytes(t, Build(spec()), FormatJSON))
	if !strings.Contains(body, `"date_published": "2026-09-26T09:00:00Z"`) {
		t.Errorf("a known publication date must be kept:\n%s", body)
	}
}

func TestRSSContentEncodedIsNamespaced(t *testing.T) {
	s := spec()
	s.FullText = true
	s.Content = map[string]string{"https://e.example/2026/09/26/first": "<p>Isi artikel.</p>"}
	out, err := Bytes(Build(s), FormatRSS)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if !strings.Contains(body, "xmlns:content=") {
		t.Errorf("content namespace missing:\n%s", body)
	}
	if !strings.Contains(body, "Isi artikel.") {
		t.Errorf("body missing:\n%s", body)
	}
}

// A hash GUID is not a URL, so it must not be marked as a permalink or readers
// will try to fetch it.
func TestRSSIsPermaLinkOnlyForURLGUIDs(t *testing.T) {
	s := spec()
	s.GUID = GUIDStable
	out, _ := Bytes(Build(s), FormatRSS)
	if strings.Contains(string(out), "isPermaLink") {
		t.Errorf("a hashed GUID must not claim to be a permalink:\n%s", out)
	}
	s.GUID = GUIDLink
	out, _ = Bytes(Build(s), FormatRSS)
	// The attribute must sit on <guid>. Unmarshalling cannot prove this: Go
	// maps an isPermaLink attribute from <item> just as happily as from
	// <guid>, so a document with the attribute on the wrong element parses
	// fine and still feeds the reader nothing. The markup itself is checked.
	if !strings.Contains(string(out), `<guid isPermaLink="true">`) {
		t.Errorf("isPermaLink is not an attribute of <guid>:\n%s", out)
	}
	if strings.Contains(string(out), "<item isPermaLink") {
		t.Errorf("isPermaLink leaked onto <item>:\n%s", out)
	}
	var doc rss2
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Channel.Items[0].GUID.Value != "https://e.example/2026/09/26/first" {
		t.Errorf("guid value = %q", doc.Channel.Items[0].GUID.Value)
	}
}

func TestSummaryTruncation(t *testing.T) {
	s := spec()
	s.SummaryMaxRunes = 20
	s.Items = []listpage.Item{{
		Link:    "https://e.example/a",
		Title:   "Judul",
		Summary: strings.Repeat("kata ", 40),
		Date:    time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC),
	}}
	f := Build(s)
	if len([]rune(f.Entries[0].Summary)) > 21 {
		t.Errorf("summary not truncated: %d runes", len([]rune(f.Entries[0].Summary)))
	}
}

func TestEmptyFeedStillRenders(t *testing.T) {
	s := spec()
	s.Items = nil
	for _, format := range Formats {
		out, err := Bytes(Build(s), format)
		if err != nil {
			t.Errorf("%s: %v", format, err)
		}
		if len(out) == 0 {
			t.Errorf("%s produced nothing for an empty feed", format)
		}
	}
}

// RSS 2.0's <enclosure> needs a length and a MIME type. A listing page gives a
// URL and nothing else, so a text <enclosure> is invalid and a length="0" is a
// lie. The image belongs in Media RSS.
func TestRSSImageUsesMediaRSSNotEnclosure(t *testing.T) {
	f := testFeed()
	f.Entries = []Entry{{
		Title: "Judul", Link: "https://e.example/a", GUID: "https://e.example/a",
		Image: "https://e.example/img/cover.jpg",
	}}
	out := render(t, f, FormatRSS)

	if strings.Contains(out, "<enclosure") {
		t.Errorf("RSS must not use <enclosure> for an image:\n%s", out)
	}
	if !strings.Contains(out, `xmlns:media="`+mediaNS+`"`) {
		t.Errorf("the Media RSS namespace is not declared:\n%s", out)
	}
	want := `<media:thumbnail url="https://e.example/img/cover.jpg" medium="image" type="image/jpeg"></media:thumbnail>`
	if !strings.Contains(out, want) {
		t.Errorf("missing %s in:\n%s", want, out)
	}
}

func TestRSSWithoutImageDeclaresNoMediaNamespace(t *testing.T) {
	f := testFeed()
	f.Entries = []Entry{{Title: "Judul", Link: "https://e.example/a", GUID: "https://e.example/a"}}
	out := render(t, f, FormatRSS)
	if strings.Contains(out, "xmlns:media") {
		t.Errorf("a feed with no images should declare no media namespace:\n%s", out)
	}
	if strings.Contains(out, "media:thumbnail") {
		t.Errorf("unexpected thumbnail:\n%s", out)
	}
}

// Atom's rel="enclosure" requires a type, so a recognisable extension gives a
// core Atom enclosure and an unrecognisable one falls back to Media RSS.
func TestAtomImageEnclosure(t *testing.T) {
	cases := []struct {
		image   string
		want    string
		notWant string
	}{
		{"https://e.example/a.jpg", `<link href="https://e.example/a.jpg" rel="enclosure" type="image/jpeg">`, ""},
		{"https://e.example/a.png?width=800", `<link href="https://e.example/a.png?width=800" rel="enclosure" type="image/png">`, ""},
		{"https://e.example/a.webp", `type="image/webp"`, ""},
		{"https://e.example/photo", "", `<link rel="enclosure"`},
		{"https://e.example/photo.bin", "", `<link rel="enclosure"`},
	}
	for _, c := range cases {
		f := testFeed()
		f.Entries = []Entry{{Title: "J", Link: "https://e.example/a", GUID: "https://e.example/a", Image: c.image}}
		out := render(t, f, FormatAtom)
		if c.want != "" && !strings.Contains(out, c.want) {
			t.Errorf("%s: missing %s in:\n%s", c.image, c.want, out)
		}
		if c.notWant != "" && strings.Contains(out, c.notWant) {
			t.Errorf("%s: must not contain %s in:\n%s", c.image, c.notWant, out)
		}
	}
	// An image of an unknown type still reaches the reader, through the
	// extension, because Media RSS needs no type.
	f := testFeed()
	f.Entries = []Entry{{Title: "J", Link: "https://e.example/a", GUID: "https://e.example/a", Image: "https://e.example/photo"}}
	out := render(t, f, FormatAtom)
	if !strings.Contains(out, `xmlns:media="`+mediaNS+`"`) {
		t.Errorf("the fallback should declare the media namespace:\n%s", out)
	}
	if !strings.Contains(out, `url="https://e.example/photo"`) {
		t.Errorf("the fallback thumbnail is missing:\n%s", out)
	}
}

// The alternate link must survive the move to a link slice.
func TestAtomKeepsAlternateLink(t *testing.T) {
	f := testFeed()
	f.Entries = []Entry{{
		Title: "J", Link: "https://e.example/a", GUID: "https://e.example/a",
		Image: "https://e.example/a.jpg",
	}}
	out := render(t, f, FormatAtom)
	if !strings.Contains(out, `<link href="https://e.example/a" rel="alternate">`) {
		t.Errorf("the alternate link is missing:\n%s", out)
	}
}

func TestImageMediaType(t *testing.T) {
	cases := map[string]string{
		"https://e.example/a.jpg":            "image/jpeg",
		"https://e.example/A.JPEG":           "image/jpeg",
		"https://e.example/a.png?v=2":        "image/png",
		"https://e.example/a.gif#frag":       "image/gif",
		"https://e.example/deep/path/a.avif": "image/avif",
		"https://e.example/a":                "",
		"https://e.example/a.unknown":        "",
		"https://e.example/?format=png":      "",
		"https://e.example/image?format=png": "",
		"https://e.example/a.txt":            "",
		"https://e.example/jpeg":             "",
		"https://e.example/dir.png/file":     "",
	}
	for in, want := range cases {
		if got := imageMediaType(in); got != want {
			t.Errorf("imageMediaType(%q) = %q, want %q", in, got, want)
		}
	}
}

// JSON Feed has a native image field, so it needs no extension at all.
func TestJSONFeedUsesNativeImage(t *testing.T) {
	f := testFeed()
	f.Entries = []Entry{{
		Title: "J", Link: "https://e.example/a", GUID: "https://e.example/a",
		Image: "https://e.example/a.jpg",
	}}
	out := render(t, f, FormatJSON)
	if !strings.Contains(out, `"image": "https://e.example/a.jpg"`) {
		t.Errorf("the JSON image field is missing:\n%s", out)
	}
	if strings.Contains(out, "enclosure") {
		t.Errorf("JSON Feed should not gain an enclosure:\n%s", out)
	}
}

// rel="self" tells a reader where to rediscover the feed. It is written with
// attributes, which is what the Atom link syntax requires.
func TestSelfLink(t *testing.T) {
	s := spec()
	s.SelfLink = "https://feed.example/extract?url=https%3A%2F%2Fe.example%2F"
	s.SelfType = "application/rss+xml; charset=utf-8"

	rss := render(t, Build(s), FormatRSS)
	if !strings.Contains(rss, `<atom:link href="https://feed.example/extract?url=https%3A%2F%2Fe.example%2F" rel="self"`) {
		t.Errorf("RSS is missing a self link:\\n%s", rss)
	}
	atom := render(t, Build(s), FormatAtom)
	if !strings.Contains(atom, `<link href="https://feed.example/extract?url=https%3A%2F%2Fe.example%2F" rel="self"`) {
		t.Errorf("Atom is missing a self link:\\n%s", atom)
	}
}

// A feed with no HTTP layer omits the self link rather than inventing one.
func TestSelfLinkIsOptional(t *testing.T) {
	for _, format := range []Format{FormatRSS, FormatAtom} {
		out := render(t, Build(spec()), format)
		if strings.Contains(out, `rel="self"`) {
			t.Errorf("%s: unexpected self link:\\n%s", format, out)
		}
	}
}
