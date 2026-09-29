package web

import (
	"feedme/internal/feedurl"
	"net/url"
	"testing"
)

func TestEditURLForInPreview(t *testing.T) {
	q := "url=https%3A%2F%2Fwww.google.com%2Falerts%2Ffeeds%2F15692221179381428575%2F1846844491137540116&fulltext=1&format=atom&title=My+Custom+Feed&max=10"
	values, _ := url.ParseQuery(q)
	spec, err := feedurl.Parse(values)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate what handlePreview does
	key := "http://test/extract?" + q
	spec.Feed.SelfLink = key
	spec.Feed.SelfType = "application/atom+xml; charset=utf-8"

	editHref := editURLFor(spec)
	t.Logf("editHref: %s", editHref)
	if editHref == "" {
		t.Error("editURLFor returned empty")
	}
	if editHref == "/" {
		t.Error("editURLFor returned just /")
	}
}
