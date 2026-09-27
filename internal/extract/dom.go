package extract

import (
	"time"

	"golang.org/x/net/html"

	"feedme/internal/domx"
)

// bestTimeIn returns the first <time datetime> or class/id hint inside n that
// parses to a date.
func bestTimeIn(n *html.Node) time.Time {
	if n == nil {
		return time.Time{}
	}
	for _, t := range domx.FindElements(n, "time") {
		if dt := domx.Attr(t, "datetime"); dt != "" {
			if parsed := parseDate(dt); !parsed.IsZero() {
				return parsed
			}
		}
		if txt := domx.NormSpace(domx.TextOf(t)); txt != "" {
			if parsed := parseDate(txt); !parsed.IsZero() {
				return parsed
			}
		}
	}
	return time.Time{}
}
