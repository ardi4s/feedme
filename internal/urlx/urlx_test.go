package urlx

import "testing"

// Resolve takes (href, base). Pinning the argument order matters: it was
// silently reversed during a refactor once, and every relative link in the
// program broke while still compiling.
func TestResolveArgumentOrder(t *testing.T) {
	cases := []struct {
		href, base, want string
	}{
		{"/img/a.jpg", "https://e.example/news/x", "https://e.example/img/a.jpg"},
		{"a.jpg", "https://e.example/news/x", "https://e.example/news/a.jpg"},
		{"../a.jpg", "https://e.example/news/x", "https://e.example/a.jpg"},
		{"https://other.example/a", "https://e.example/", "https://other.example/a"},
		{"//cdn.example/a.png", "https://e.example/", "https://cdn.example/a.png"},
		{"//cdn.example/a.png", "http://e.example/", "http://cdn.example/a.png"},
		{"/a", "", "/a"},
		{"", "https://e.example/", ""},
		{"#frag", "https://e.example/x", "https://e.example/x#frag"},
		{"/a?b=1", "https://e.example/", "https://e.example/a?b=1"},
	}
	for _, tc := range cases {
		if got := Resolve(tc.href, tc.base); got != tc.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", tc.href, tc.base, got, tc.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	// Two links that differ only in tracking must compare equal, or the same
	// article is stored twice.
	a := Normalize("https://e.example/a?utm_source=x&id=5")
	b := Normalize("https://e.example/a?id=5&fbclid=zz")
	if a != b {
		t.Errorf("tracking parameters changed identity:\n  %s\n  %s", a, b)
	}
	// Query order must not matter.
	if Normalize("https://e.example/a?b=2&a=1") != Normalize("https://e.example/a?a=1&b=2") {
		t.Error("query parameter order should not affect normalisation")
	}
	// Significant parameters must survive.
	if got := Normalize("https://e.example/a?id=5"); got != "https://e.example/a?id=5" {
		t.Errorf("a real query parameter was dropped: %q", got)
	}
	// Fragments, default ports and host case are all noise.
	if got := Normalize("https://E.Example:443/a#x"); got != "https://e.example/a" {
		t.Errorf("got %q", got)
	}
	if got := Normalize("http://e.example:80/a"); got != "http://e.example/a" {
		t.Errorf("got %q", got)
	}
}

func TestPatternOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://kontan.co.id/news/2026/9/26/245189/abc-judul-panjang", "kontan.co.id/news/{n}/{n}/{n}/{n}/{slug}"},
		{"https://surabaya.kompas.com/read/2026/09/14/182139778/berita", "kompas.com/read/{n}/{n}/{n}/{n}/{slug}"},
		{"https://www.bbc.com/mundo/articles/cqkgwx3zed53o", "bbc.com/mundo/articles/{slug}"},
		{"https://e.example/tentang", "e.example/tentang"},
		{"https://www.theguardian.com/world/2026/sep/26/some-story", "theguardian.com/world/{n}/{n}/{n}/{slug}"},
		{"https://e.example/a", "e.example/a"},
	}
	for _, tc := range cases {
		p, ok := PatternOf(tc.in)
		if !ok {
			t.Errorf("PatternOf(%q) failed", tc.in)
			continue
		}
		if p.Raw != tc.want {
			t.Errorf("PatternOf(%q) = %q, want %q", tc.in, p.Raw, tc.want)
		}
	}
}

func TestPatternOfRejectsJunk(t *testing.T) {
	for _, in := range []string{"", "not a url", "mailto:a@b.example", "javascript:void(0)"} {
		if _, ok := PatternOf(in); ok {
			t.Errorf("PatternOf(%q) should have failed", in)
		}
	}
}

// A date-shaped segment must collapse to the same placeholder as a plain
// number, otherwise every article on a dated archive page forms its own
// pattern and nothing groups together.
func TestPatternCollapsesDates(t *testing.T) {
	grouped := GroupPatterns([]string{
		"https://e.example/news/2026-09-26/aaa",
		"https://e.example/news/2026-09-25/bbb",
		"https://e.example/news/20260924/ccc",
	})
	if len(grouped) == 0 {
		t.Fatal("no pattern found")
	}
	if grouped[0].Count != 3 {
		t.Errorf("date segments did not group: %+v", grouped[0])
	}
}

// urlx ranks purely by count, then by depth. It does not know which links sit
// in a navigation menu; suppressing those is the list page layer's job, and
// asserting it here would only test a guarantee this package does not make.
func TestGroupPatternsRanking(t *testing.T) {
	urls := []string{
		"https://e.example/kategori/ekonomi",
		"https://e.example/kategori/saham",
		"https://e.example/kategori/uang",
		"https://e.example/kategori/investasi",
		"https://e.example/news/2026/09/26/aaa",
		"https://e.example/news/2026/09/25/bbb",
		"https://e.example/news/2026/09/24/ccc",
	}
	groups := GroupPatterns(urls)
	if len(groups) < 2 {
		t.Fatalf("expected at least two groups, got %d", len(groups))
	}
	// The four-link group outranks the three-link group on count alone.
	if groups[0].Count != 4 {
		t.Errorf("expected the 4-link group to rank first, got %+v", groups[0])
	}
	// With counts equal, the deeper path wins.
	tie := GroupPatterns([]string{
		"https://e.example/x/one",
		"https://e.example/x/two",
		"https://e.example/news/2026/09/26/aaa",
		"https://e.example/news/2026/09/25/bbb",
	})
	if tie[0].Count != tie[1].Count {
		t.Fatalf("test needs two groups with equal counts, got %+v", tie)
	}
	if tie[0].Depth <= tie[1].Depth {
		t.Errorf("depth should break the tie, got %+v then %+v", tie[0], tie[1])
	}
}

// GroupPatterns must be deterministic: the winner cannot depend on map
// iteration order, or the same page would produce different feeds on different
// runs.
func TestGroupPatternsIsDeterministic(t *testing.T) {
	urls := []string{
		"https://e.example/a/1/one",
		"https://e.example/a/2/two",
		"https://e.example/b/1/three",
		"https://e.example/b/2/four",
	}
	first := GroupPatterns(urls)[0].Raw
	for i := 0; i < 25; i++ {
		if got := GroupPatterns(urls)[0].Raw; got != first {
			t.Fatalf("winner changed between runs: %q then %q", first, got)
		}
	}
}

func TestGroupPatternsKeepsSlugs(t *testing.T) {
	groups := GroupPatterns([]string{
		"https://e.example/news/2026/09/26/aaa",
		"https://e.example/news/2026/09/25/bbb",
	})
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	if len(groups[0].Slugs) != 2 {
		t.Errorf("slugs = %v, want two distinct slugs", groups[0].Slugs)
	}
}

func TestKeepQueryParams(t *testing.T) {
	cases := []struct {
		raw, mode, want string
	}{
		{"https://e.example/a?id=5&cat=x", "1", "https://e.example/a?id=5&cat=x"},
		{"https://e.example/a?z=1&b=2", "1", "https://e.example/a?z=1&b=2"},
		{"https://e.example/a?id=5&cat=x", "0", "https://e.example/a"},
		{"https://e.example/a?id=5&cat=x", "id", "https://e.example/a?id=5"},
		{"https://e.example/a?id=5&cat=x&t=9", "id,cat", "https://e.example/a?id=5&cat=x"},
		{"https://e.example/a?utm_source=z&id=5", "1", "https://e.example/a?id=5"},
		{"https://e.example/a?id=5#frag", "1", "https://e.example/a?id=5"},
	}
	for _, tc := range cases {
		if got := KeepQueryParams(tc.raw, tc.mode); got != tc.want {
			t.Errorf("KeepQueryParams(%q, %q) = %q, want %q", tc.raw, tc.mode, got, tc.want)
		}
	}
}

func TestSameHost(t *testing.T) {
	if !SameHost("https://www.e.example/a", "http://www.e.example/b") {
		t.Error("same host should match across schemes")
	}
	if SameHost("https://e.example/a", "https://other.example/b") {
		t.Error("different hosts should not match")
	}
	if SameHost("not a url", "https://e.example/") {
		t.Error("a malformed URL should not match")
	}
}

func TestSlugOf(t *testing.T) {
	cases := map[string]string{
		"https://e.example/news/2026/09/26/judul-artikel": "judul-artikel",
		"https://e.example/":                              "",
	}
	for in, want := range cases {
		if got := SlugOf(in); got != want {
			t.Errorf("SlugOf(%q) = %q, want %q", in, got, want)
		}
	}
}
