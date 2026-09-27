package feedread

import (
	"strings"
	"testing"
	"time"
)

const rssSample = `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"
     xmlns:media="http://search.yahoo.com/mrss/">
 <channel>
  <title>Contoh RSS</title>
  <link>https://e.example/</link>
  <item>
   <title>Alpha</title>
   <link>https://e.example/2026/09/26/alpha</link>
   <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate>
   <description><![CDATA[<p>Ringkasan <b>alpha</b>.</p>]]></description>
   <author>Andi</author>
   <media:thumbnail url="https://e.example/a.jpg"/>
  </item>
  <item>
   <title>Beta</title>
   <link>/2026/09/25/beta</link>
   <dc:date xmlns:dc="http://purl.org/dc/elements/1.1/">2026-09-25T08:00:00Z</dc:date>
   <content:encoded><![CDATA[<p>Isi penuh beta.</p>]]></content:encoded>
  </item>
 </channel>
</rss>`

func TestParseRSS(t *testing.T) {
	doc, err := Parse([]byte(rssSample), "https://e.example/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Contoh RSS" {
		t.Errorf("title = %q", doc.Title)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(doc.Items))
	}
	a := doc.Items[0]
	if a.Link != "https://e.example/2026/09/26/alpha" {
		t.Errorf("alpha link = %q", a.Link)
	}
	if a.Summary != "Ringkasan alpha." {
		t.Errorf("alpha summary = %q, want the tags stripped", a.Summary)
	}
	if a.Image != "https://e.example/a.jpg" {
		t.Errorf("alpha image = %q", a.Image)
	}
	if a.Author != "Andi" {
		t.Errorf("alpha author = %q", a.Author)
	}
	if a.Date.IsZero() || a.Date.UTC().Format(time.RFC3339) != "2026-09-26T09:00:00Z" {
		t.Errorf("alpha date = %v", a.Date)
	}
	b := doc.Items[1]
	if b.Link != "https://e.example/2026/09/25/beta" {
		t.Errorf("relative link not resolved: %q", b.Link)
	}
	if b.Summary != "Isi penuh beta." {
		t.Errorf("beta summary should fall back to content:encoded: %q", b.Summary)
	}
}

const atomSample = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:media="http://search.yahoo.com/mrss/">
 <title>Contoh Atom</title>
 <link href="https://a.example/" rel="alternate"/>
 <entry>
  <title>Gamma</title>
  <link href="https://a.example/2026/09/26/gamma" rel="alternate"/>
  <published>2026-09-26T10:00:00Z</published>
  <summary>Ringkasan gamma.</summary>
  <author><name>Budi</name></author>
  <media:thumbnail url="https://a.example/g.jpg"/>
 </entry>
</feed>`

func TestParseAtom(t *testing.T) {
	doc, err := Parse([]byte(atomSample), "https://a.example/feed")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Contoh Atom" || doc.Link != "https://a.example/" {
		t.Errorf("feed title/link = %q / %q", doc.Title, doc.Link)
	}
	if len(doc.Items) != 1 {
		t.Fatalf("items = %d", len(doc.Items))
	}
	g := doc.Items[0]
	if g.Link != "https://a.example/2026/09/26/gamma" || g.Author != "Budi" {
		t.Errorf("entry = %+v", g)
	}
	if g.Date.UTC().Format(time.RFC3339) != "2026-09-26T10:00:00Z" {
		t.Errorf("date = %v", g.Date)
	}
	if g.Image != "https://a.example/g.jpg" {
		t.Errorf("image = %q", g.Image)
	}
}

const jsonSample = `{
 "version": "https://jsonfeed.org/version/1.1",
 "title": "Contoh JSON",
 "home_page_url": "https://j.example/",
 "items": [
  {"id": "1", "url": "https://j.example/2026/09/26/delta", "title": "Delta",
   "date_published": "2026-09-26T11:00:00Z",
   "content_html": "<p>Ringkasan <em>delta</em>.</p>",
   "authors": [{"name": "Citra"}],
   "image": "https://j.example/d.jpg"}
 ]
}`

func TestParseJSON(t *testing.T) {
	doc, err := Parse([]byte(jsonSample), "https://j.example/feed.json")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Contoh JSON" || doc.Link != "https://j.example/" {
		t.Errorf("feed title/link = %q / %q", doc.Title, doc.Link)
	}
	if len(doc.Items) != 1 {
		t.Fatalf("items = %d", len(doc.Items))
	}
	d := doc.Items[0]
	if d.Link != "https://j.example/2026/09/26/delta" || d.Title != "Delta" {
		t.Errorf("entry = %+v", d)
	}
	if d.Summary != "Ringkasan delta." {
		t.Errorf("summary = %q", d.Summary)
	}
	if d.Author != "Citra" || d.Image != "https://j.example/d.jpg" {
		t.Errorf("author/image = %q / %q", d.Author, d.Image)
	}
}

func TestParseRejectsNonFeed(t *testing.T) {
	for _, body := range []string{"", "   ", "<html><body>hi</body></html>", "not a feed"} {
		if _, err := Parse([]byte(body), "https://e.example/"); err == nil {
			t.Errorf("Parse(%q) should have failed", body)
		}
	}
}

// A feed whose title is plain text with an ampersand must not lose it or show
// the entity.
func TestTextUnescapesEntities(t *testing.T) {
	body := `<rss version="2.0"><channel><title>A &amp; B</title>
	 <item><title>X &amp; Y</title><link>https://e.example/1</link>
	 <description>Tea &amp; coffee</description></item></channel></rss>`
	doc, err := Parse([]byte(body), "https://e.example/")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "A & B" {
		t.Errorf("title = %q", doc.Title)
	}
	if got := doc.Items[0].Summary; got != "Tea & coffee" {
		t.Errorf("summary = %q", got)
	}
	if !strings.Contains(doc.Items[0].Title, "&") {
		t.Errorf("item title = %q", doc.Items[0].Title)
	}
}
