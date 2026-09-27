package filter

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"feedme/internal/listpage"
)

func item(link, title string, day string) listpage.Item {
	it := listpage.Item{Link: link, Title: title, Summary: "Ringkasan " + title}
	if day != "" {
		t, err := time.Parse(dayFormat, day)
		if err != nil {
			panic(err)
		}
		it.Date = t
	}
	return it
}

var sample = []listpage.Item{
	item("https://kontan.co.id/news/2026/9/26/245189/harga-emas-naik", "Harga emas naik", "2026-09-26"),
	item("https://kontan.co.id/news/2026/9/25/245100/rupiah-melemah", "Rupiah melemah", "2026-09-25"),
	item("https://surabaya.kompas.com/baca/2026/09/24/500/manifesto", "Manifesto kota", "2026-09-24"),
	item("https://www.bbc.com/mundo/articles/cqkgwx3zed53o", "Mundo article", "2026-09-20"),
	item("https://kontan.co.id/tag/uang", "Tag uang", ""),
}

func titles(items []listpage.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Title)
	}
	return out
}

func TestEmptyRulesKeepEverything(t *testing.T) {
	res, err := Apply(sample, Rules{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != len(sample) {
		t.Errorf("kept %d of %d", len(res.Items), len(sample))
	}
	if res.TotalDropped() != 0 {
		t.Errorf("unexpected drops: %v", res.Dropped)
	}
}

func TestURLContains(t *testing.T) {
	res, err := Apply(sample, Rules{URLContains: []string{"kontan.co.id"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %v", titles(res.Items))
	}
	// A subdomain of the named host must match too, or a rule for
	// kompas.com would drop every regional edition.
	res, err = Apply(sample, Rules{URLContains: []string{"kompas.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Errorf("subdomain should match: %v", titles(res.Items))
	}
}

func TestURLContainsIsCaseInsensitive(t *testing.T) {
	res, _ := Apply(sample, Rules{URLContains: []string{"KONTAN.CO.ID"}})
	if len(res.Items) != 3 {
		t.Errorf("case should not matter: %v", titles(res.Items))
	}
}

// Several values for one key are alternatives, not a conjunction: a user
// listing two hosts means "either".
func TestURLContainsIsAlternatives(t *testing.T) {
	res, _ := Apply(sample, Rules{URLContains: []string{"kompas.com", "bbc.com"}})
	if len(res.Items) != 2 {
		t.Errorf("items = %v", titles(res.Items))
	}
}

func TestURLNotContains(t *testing.T) {
	res, _ := Apply(sample, Rules{URLNotContains: []string{"/tag/"}})
	if len(res.Items) != 4 {
		t.Errorf("items = %v", titles(res.Items))
	}
	if res.Dropped[ReasonURLNotContains] != 1 {
		t.Errorf("drop count = %v", res.Dropped)
	}
}

// Keep rules are applied before drop rules, so a user who writes both gets the
// narrower set rather than an accidental union.
func TestKeepRunsBeforeDrop(t *testing.T) {
	res, _ := Apply(sample, Rules{
		URLContains:    []string{"kontan.co.id"},
		URLNotContains: []string{"/tag/"},
	})
	for _, it := range res.Items {
		if strings.Contains(it.Link, "/tag/") {
			t.Errorf("drop rule was not applied after the keep rule: %v", titles(res.Items))
		}
	}
}

func TestURIMatches(t *testing.T) {
	res, err := Apply(sample, Rules{URIMatches: []string{`/\d{4}/\d{1,2}/\d{1,2}/\d+/`}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %v", titles(res.Items))
	}
}

func TestURIMatchesReportsBadPattern(t *testing.T) {
	_, err := Apply(sample, Rules{URIMatches: []string{"("}})
	if err == nil {
		t.Fatal("a broken pattern must be reported, not ignored")
	}
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error type = %T, want *FieldError", err)
	}
	if fe.Field != "uri_matches" {
		t.Errorf("Field = %q, want uri_matches", fe.Field)
	}
}

func TestTextContains(t *testing.T) {
	res, _ := Apply(sample, Rules{TextContains: []string{"rupiah"}})
	if len(res.Items) != 1 || res.Items[0].Title != "Rupiah melemah" {
		t.Errorf("items = %v", titles(res.Items))
	}
	if res.Dropped[ReasonTextContains] != 4 {
		t.Errorf("drop count = %v", res.Dropped)
	}
}

func TestTextNotContains(t *testing.T) {
	res, _ := Apply(sample, Rules{TextNotContains: []string{"emas", "rupiah"}})
	if len(res.Items) != 3 {
		t.Errorf("items = %v", titles(res.Items))
	}
}

func TestDomain(t *testing.T) {
	res, _ := Apply(sample, Rules{Domain: []string{"kontan.co.id"}})
	if len(res.Items) != 3 {
		t.Errorf("items = %v", titles(res.Items))
	}
	res, _ = Apply(sample, Rules{Domain: []string{"kompas.com"}})
	if len(res.Items) != 1 {
		t.Errorf("subdomain rule should match the regional edition: %v", titles(res.Items))
	}
}

func TestDateRange(t *testing.T) {
	res, _ := Apply(sample, Rules{DateAfter: "2026-09-24"})
	if len(res.Items) != 3 {
		t.Errorf("items = %v", titles(res.Items))
	}
	// Boundaries are inclusive at both ends, so a user asking for one day gets
	// that day rather than nothing.
	res, _ = Apply(sample, Rules{DateAfter: "2026-09-24", DateBefore: "2026-09-24"})
	if len(res.Items) != 1 || res.Items[0].Title != "Manifesto kota" {
		t.Errorf("single-day range = %v", titles(res.Items))
	}
}

// An undated item cannot satisfy a date window. Keeping it would make a
// date-filtered feed look correct while containing items from any date.
func TestDateRangeDropsUndated(t *testing.T) {
	res, _ := Apply(sample, Rules{DateAfter: "2020-01-01"})
	for _, it := range res.Items {
		if it.Date.IsZero() {
			t.Errorf("undated item survived a date filter: %+v", it)
		}
	}
	if res.Dropped[ReasonDate] != 1 {
		t.Errorf("drop count = %v", res.Dropped)
	}
}

func TestDateBoundaryAcceptsTimestamp(t *testing.T) {
	// A date picker often submits a full timestamp.
	res, err := Apply(sample, Rules{DateAfter: "2026-09-26T08:30:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Errorf("items = %v", titles(res.Items))
	}
}

func TestDateBoundaryAcceptsUnambiguousAlternative(t *testing.T) {
	// 26 cannot be a month, so day-first is the only reading and accepting it
	// spares the user from retyping a date they got right.
	res, err := Apply(sample, Rules{DateAfter: "26-09-2026"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Errorf("items = %v", titles(res.Items))
	}
}

func TestDateBoundaryRejectsAmbiguous(t *testing.T) {
	// 05-09-2026 is either 5 May or 5 September. Silently picking one leaves the
	// user with a feed that is missing months of items and no clue why, so the
	// bound is refused and the field is named.
	_, err := Apply(sample, Rules{DateAfter: "05-09-2026"})
	if err == nil {
		t.Fatal("an ambiguous date bound must be reported, not guessed")
	}
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "date_after" {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(fe.Error(), "YYYY-MM-DD") {
		t.Errorf("error should say the expected format: %v", fe)
	}
}

func TestMaxItems(t *testing.T) {
	res, _ := Apply(sample, Rules{MaxItems: 2})
	if len(res.Items) != 2 {
		t.Errorf("items = %v", titles(res.Items))
	}
	// MaxItems is a cap, not a filter: it must not be counted as a drop reason.
	if res.TotalDropped() != 0 {
		t.Errorf("MaxItems should not count as drops: %v", res.Dropped)
	}
}

// Order must survive filtering: a feed that is not in publication order looks
// broken to every reader.
func TestOrderIsPreserved(t *testing.T) {
	res, _ := Apply(sample, Rules{URLContains: []string{"."}})
	want := titles(sample)
	got := titles(res.Items)
	if len(got) != len(want) {
		t.Fatalf("length %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order changed: %v != %v", got, want)
		}
	}
}

func TestReasonsAreStable(t *testing.T) {
	res, _ := Apply(sample, Rules{
		URLContains:    []string{"kontan"},
		URLNotContains: []string{"/tag/"},
		TextContains:   []string{"tidak-ada-sama-sekali"},
	})
	got := res.Reasons()
	if len(got) == 0 {
		t.Fatal("expected some drop reasons")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("reasons not sorted: %v", got)
		}
	}
}

func TestEncodeQueryPreservesOrder(t *testing.T) {
	got := EncodeQuery([]Param{
		{Key: "url", Value: "https://e.example/a?b=1"},
		{Key: "url_contains[]", Value: "kontan"},
		{Key: "url_contains[]", Value: "kompas"},
		{Key: "max", Value: "20"},
	})
	want := "url=https%3A%2F%2Fe.example%2Fa%3Fb%3D1&url_contains%5B%5D=kontan&url_contains%5B%5D=kompas&max=20"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// Repeated keys are the whole point of url_contains[], so the round trip has to
// keep them.
func TestEncodeQueryRoundTrip(t *testing.T) {
	q := EncodeQuery([]Param{
		{Key: "url_contains[]", Value: "a b"},
		{Key: "url_contains[]", Value: "c&d"},
	})
	values, err := url.ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	got := values["url_contains[]"]
	if len(got) != 2 || got[0] != "a b" || got[1] != "c&d" {
		t.Errorf("round trip = %q", got)
	}
}

func TestHasParam(t *testing.T) {
	cases := []struct {
		query, key string
		want       bool
	}{
		{"url_contains[]=a", "url_contains[]", true},
		{"url=https%3A%2F%2Fe.example", "url", true},
		{"max=20", "url_contains[]", false},
		{"", "url", false},
		{"max=", "max", true},
		{"url_contains%5B%5D=a", "url_contains[]", true},
	}
	for _, tc := range cases {
		if got := HasParam(tc.query, tc.key); got != tc.want {
			t.Errorf("HasParam(%q, %q) = %v, want %v", tc.query, tc.key, got, tc.want)
		}
	}
}
