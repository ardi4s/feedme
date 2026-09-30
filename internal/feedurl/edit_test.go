package feedurl

import (
	"net/url"
	"testing"
)

func TestEditURLFor(t *testing.T) {
	q := "url=https%3A%2F%2Fwww.google.com%2Falerts%2Ffeeds%2F15692221179381428575%2F1846844491137540116&fulltext=1&format=atom&title=My+Custom+Feed&max=10"
	values, _ := url.ParseQuery(q)
	spec, err := Parse(values)
	if err != nil {
		t.Fatal(err)
	}
	result := spec.Query()
	encoded := result.Encode()
	t.Logf("Encoded: %s", encoded)
	if encoded == "" {
		t.Error("Query string is empty")
	}
}
