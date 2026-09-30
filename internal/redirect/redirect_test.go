package redirect

import "testing"

func TestUnwrap(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			name: "a direct link is left alone",
			in:   "https://www.liputan6.com/tekno/read/8301723/top-3-tekno",
			want: "https://www.liputan6.com/tekno/read/8301723/top-3-tekno",
			ok:   false,
		},
		{
			// The Bing News form, with the real parameter layout taken from a
			// live Indonesian news query.
			name: "bing news apiclick",
			in: "http://www.bing.com/news/apiclick.aspx?ref=FexRss&aid=&tid=6abb43858df543afa5f269345ca9e949" +
				"&url=https%3a%2f%2fwww.liputan6.com%2ftekno%2fread%2f8301723",
			want: "https://www.liputan6.com/tekno/read/8301723",
			ok:   true,
		},
		{
			name: "google alerts click-through",
			in: "https://www.google.com/url?q=https%3A%2F%2Fpluang.com%2Fnews-feed%2Fagen-ai" +
				"&sa=t&usg=ALkJrhg",
			want: "https://pluang.com/news-feed/agen-ai",
			ok:   true,
		},
		{
			// The form a real Google Alerts feed publishes, with the full
			// parameter set it sends and the target under "url" rather than
			// "q". Guessing "q" alone would have missed every Alerts item.
			name: "google alerts as it is actually published",
			in: "https://www.google.com/url?rct=j&sa=t" +
				"&url=https%3A%2F%2Fwww.businessinsider.com%2Finstinct-ai-agent-plan-travel-dull-mistake-vacation-work-trip-2026-9" +
				"&ct=ga&cd=CAIyHGEzNTgxZjI4YTA0Yzk2ZmY6Y29tOmp2OlVTOkw&usg=AOvVaw03uvNcgz5nPygshcuH3kcC",
			want: "https://www.businessinsider.com/instinct-ai-agent-plan-travel-dull-mistake-vacation-work-trip-2026-9",
			ok:   true,
		},
		{
			// Both names in one link: the unusable one is skipped rather than
			// taken, so a host that mixes the two still resolves.
			name: "an unusable url falls through to q",
			in:   "https://www.google.com/url?url=not-a-url&q=https%3A%2F%2Fexample.com%2Fa&sa=t",
			want: "https://example.com/a",
			ok:   true,
		},
		{
			name: "a country domain is still google",
			in:   "https://www.google.co.id/url?q=https%3A%2F%2Fexample.com%2Fa&sa=t",
			want: "https://example.com/a",
			ok:   true,
		},
		{
			name: "q on another host is not a wrapper",
			in:   "https://example.com/url?q=https%3A%2F%2Felsewhere.test%2Fa",
			want: "https://example.com/url?q=https%3A%2F%2Felsewhere.test%2Fa",
			ok:   false,
		},
		{
			name: "apiclick on another host is not a wrapper",
			in:   "https://example.com/news/apiclick.aspx?url=https%3A%2F%2Felsewhere.test%2Fa",
			want: "https://example.com/news/apiclick.aspx?url=https%3A%2F%2Felsewhere.test%2Fa",
			ok:   false,
		},
		{
			name: "a lookalike host is not a wrapper",
			in:   "https://notgoogle.com/url?q=https%3A%2F%2Fexample.com%2Fa",
			want: "https://notgoogle.com/url?q=https%3A%2F%2Fexample.com%2Fa",
			ok:   false,
		},
		{
			name: "an unwrapped target that is not a URL is refused",
			in:   "https://www.google.com/url?q=not-a-url&sa=t",
			want: "https://www.google.com/url?q=not-a-url&sa=t",
			ok:   false,
		},
		{
			name: "a non-http target is refused",
			in:   "https://www.google.com/url?q=javascript%3Aalert(1)&sa=t",
			want: "https://www.google.com/url?q=javascript%3Aalert(1)&sa=t",
			ok:   false,
		},
		{
			name: "a target pointing back at the wrapper is refused",
			in:   "https://www.google.com/url?q=https%3A%2F%2Fwww.google.com%2Furl%3Fq%3Dx&sa=t",
			want: "https://www.google.com/url?q=https%3A%2F%2Fwww.google.com%2Furl%3Fq%3Dx&sa=t",
			ok:   false,
		},
		{
			name: "two layers are both opened",
			in: "https://www.bing.com/news/apiclick.aspx?tid=1&url=" +
				"https%3A%2F%2Fwww.google.com%2Furl%3Fq%3Dhttps%253A%252F%252Fexample.com%252Fart",
			want: "https://example.com/art",
			ok:   true,
		},
		{
			name: "an empty input is left alone",
			in:   "",
			want: "",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Unwrap(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Fatalf("Unwrap() = %q, want %q", got, tc.want)
			}
		})
	}
}
