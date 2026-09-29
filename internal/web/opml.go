package web

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The management page's feed list, in OPML 2.0. Export is what makes the list
// worth keeping: a reader imports the file in one step and feedme stays a feed
// source rather than growing into a reader.

// opmlDoc is the document shape the OPML 2.0 spec's own example shows: a head
// with a title and date, and a body of outlines. No namespace is emitted — the
// spec makes it optional and every reader in the field accepts its absence.
type opmlDoc struct {
	XMLName xml.Name      `xml:"opml"`
	Version string        `xml:"version,attr"`
	Head    opmlHead      `xml:"head"`
	Body    []opmlOutline `xml:"body>outline"`
}

type opmlHead struct {
	Title       string `xml:"title"`
	DateCreated string `xml:"dateCreated"`
}

// opmlOutline is one subscription. xmlUrl is the feed URL itself — the thing a
// reader subscribes to — and htmlUrl is the page the feed was built from, so a
// reader can show where a feed came from. text is what the reader displays and
// is required by the spec, so a merge feed with no source page falls back to
// its feed URL rather than going out with a blank name.
type opmlOutline struct {
	Text    string `xml:"text,attr"`
	Title   string `xml:"title,attr,omitempty"`
	Type    string `xml:"type,attr"`
	HTMLURL string `xml:"htmlUrl,attr,omitempty"`
	XMLURL  string `xml:"xmlUrl,attr"`
}

// handleFeedsOPML serves every feed the server has built as one OPML file. It
// reads through the same BuiltFeeds call the page does, so the export cannot
// disagree with the listing. The file is an attachment rather than a page, so a
// browser downloads it instead of trying to render it.
func (s *Server) handleFeedsOPML(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	built, err := s.admin.BuiltFeeds(r.Context())
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	now := s.nowFn()
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="feedme-%s.opml"`, now.Format("2006-01-02")))
	w.Header().Set("Cache-Control", "no-store")
	if err := writeOPML(w, built, now); err != nil {
		// The headers are already gone, so there is no useful status left to
		// send; the log is where a truncated download gets explained.
		s.log.Error("opml write failed", "error", err.Error())
	}
}

// writeOPML renders the feed list. The encoder does the escaping, which matters
// because a feed URL is a query string and its & and = are everywhere.
func writeOPML(w io.Writer, feeds []BuiltFeed, now time.Time) error {
	doc := opmlDoc{
		Version: "2.0",
		Head: opmlHead{
			Title:       "feedme subscriptions",
			DateCreated: now.Format(http.TimeFormat),
		},
	}
	for _, f := range feeds {
		name := f.SourceURL
		if name == "" {
			name = f.Key
		}
		doc.Body = append(doc.Body, opmlOutline{
			Text:    name,
			Title:   name,
			Type:    "rss",
			HTMLURL: f.SourceURL,
			XMLURL:  f.Key,
		})
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return enc.Flush()
}
