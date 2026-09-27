package main

import "testing"

// A reader's canonical feed URL is the cache key, so the source page has to come
// back out of it for the row to be readable by hand.
func TestSourceURLOf(t *testing.T) {
	cases := []struct{ key, want string }{
		{"http://h/extract?url=http%3A%2F%2Fe.example%2Fnews", "http://e.example/news"},
		{"http://h/extract?format=atom&url=http%3A%2F%2Fe.example%2Fn", "http://e.example/n"},
		{"http://h/extract?url=https%3A%2F%2Fe.example%2Fa%20b", "https://e.example/a b"},
		{"http://h/extract?url=http%3A%2F%2Fe.example%2Fx%2Fy+z", "http://e.example/x/y z"},
		{"http://h/extract?ttl=15", ""},
		{"http://h/extract", ""},
		{"://bad", ""},
	}
	for _, c := range cases {
		if got := sourceURLOf(c.key); got != c.want {
			t.Errorf("sourceURLOf(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}
