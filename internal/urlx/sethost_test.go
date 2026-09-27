package urlx

import "testing"

func TestSetHost(t *testing.T) {
	cases := []struct{ in, host, want string }{
		{"https://nasional.kontan.co.id/news/x?source=home", "www.kontan.co.id", "https://www.kontan.co.id/news/x?source=home"},
		{"http://a.example/p/q#frag", "b.example", "http://b.example/p/q#frag"},
		{"https://a.example:8080/x", "b.example", "https://b.example/x"},
		{"https://a.example/x", "", "https://a.example/x"},
		{"not a url", "b.example", "not a url"},
		{"/relative/path", "b.example", "/relative/path"},
	}
	for _, c := range cases {
		if got := SetHost(c.in, c.host); got != c.want {
			t.Errorf("SetHost(%q, %q) = %q, want %q", c.in, c.host, got, c.want)
		}
	}
}

func TestSiteAndPathKey(t *testing.T) {
	cases := []struct{ in, site string }{
		{"https://nasional.kontan.co.id/news/x", "kontan.co.id"},
		{"https://www.kontan.co.id/news/x", "kontan.co.id"},
		{"https://a.b.example.co.uk/p", "example.co.uk"},
		{"http://127.0.0.1:8080/x", "127.0.0.1"},
		{"https://localhost/x", "localhost"},
	}
	for _, c := range cases {
		if got := Site(c.in); got != c.site {
			t.Errorf("Site(%q) = %q, want %q", c.in, got, c.site)
		}
	}
	// Same article from two sections, differing only by a source parameter.
	a := PathKey("https://nasional.kontan.co.id/news/x?source=home_headline")
	b := PathKey("https://www.kontan.co.id/news/x?source=home_terbaru")
	if a != b {
		t.Errorf("PathKey should ignore the query: %q != %q", a, b)
	}
	if PathKey("https://www.kontan.co.id/news/x") == PathKey("https://www.kontan.co.id/news/y") {
		t.Error("different paths must not collide")
	}
}
