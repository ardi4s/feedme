package extract

import (
	"log/slog"
	"strings"

	"github.com/ryanfowler/readability"
	"golang.org/x/net/html"
)

// readabilityResult wraps the upstream Article plus the node we want to
// sanitise, so later strategies can still work from an unsullied tree.
type readabilityResult struct {
	Node     *html.Node
	Title    string
	Byline   string
	Text     string
	Excerpt  string
	SiteName string
	Chars    int
}

// extractReadability runs the Mozilla Readability algorithm. It is the
// workhorse: rule-based, no guessing about site structure, and the best
// median performer of the heuristic extractors in published benchmarks.
func extractReadability(doc *html.Node, pageURL string, maxElements int, logger *slog.Logger) *readabilityResult {
	opts := []readability.Option{
		// Cap the node count: we parse HTML from untrusted sources and an
		// unbounded document is a cheap way to burn memory.
		readability.WithMaxElemsToParse(maxElements),
	}
	if logger != nil {
		opts = append(opts, readability.WithLogger(logger))
	}
	article, err := readability.ParseNode(doc, pageURL, opts...)
	if err != nil {
		// TooManyElementsError and ErrNoContent are both "no usable result",
		// so the cascade can move on to the density scorer.
		return nil
	}
	node := article.Node
	if node == nil {
		return nil
	}
	return &readabilityResult{
		Node:     node,
		Title:    strings.TrimSpace(article.Title),
		Byline:   strings.TrimSpace(article.Byline),
		Text:     article.TextContent,
		Excerpt:  strings.TrimSpace(article.Excerpt),
		SiteName: strings.TrimSpace(article.SiteName),
		Chars:    len([]rune(article.TextContent)),
	}
}

// probablyReaderable is the cheap pre-check. It is used to reject obvious
// non-articles before paying for a full extraction.
func probablyReaderable(doc *html.Node) bool {
	return readability.IsProbablyReaderableNode(doc)
}
