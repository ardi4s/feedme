package main

import (
	"context"
	"testing"
)

// A probe of a click-through link has to fetch the publisher's page, or it
// reports on the aggregator's shell and answers a question nobody asked. The
// wrappers that name their destination are opened without a request; the ones
// that do not are left to the caller to open, which is a network concern and not
// something this unit can stand up.
func TestOpenLinkOpensAWrapperThatNamesItsTarget(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bing news",
			in: "http://www.bing.com/news/apiclick.aspx?ref=FexRss&tid=1" +
				"&url=https%3a%2f%2fwww.liputan6.com%2ftekno%2fread%2f8301723",
			want: "https://www.liputan6.com/tekno/read/8301723",
		},
		{
			name: "google alerts",
			in:   "https://www.google.com/url?q=https%3A%2F%2Fpluang.com%2Fnews-feed%2Fagen-ai&sa=t&usg=ALkJrhg",
			want: "https://pluang.com/news-feed/agen-ai",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// nil client: reaching for it would mean a request was needed.
			got, opened, err := openLink(context.Background(), nil, tc.in, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !opened {
				t.Fatal("opened = false, want true")
			}
			if got != tc.want {
				t.Fatalf("openLink() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A link that is already the publisher's is left alone, and reported as
// untouched, so the probe output does not claim to have opened something.
func TestOpenLinkLeavesADirectLinkAlone(t *testing.T) {
	const in = "https://www.liputan6.com/tekno/read/8301723/top-3-tekno"
	got, opened, err := openLink(context.Background(), nil, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opened {
		t.Error("opened = true, want false for a direct link")
	}
	if got != in {
		t.Errorf("openLink() = %q, want it unchanged", got)
	}
}
