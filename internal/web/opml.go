package web

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// The management page's feed list, exportable as OPML 2.0, CSV, or JSON.
// Export is what makes the list worth keeping: a reader imports the file in
// one step and feedme stays a feed source rather than growing into a reader.

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

// exportFeed is a single feed record for CSV/JSON export.
type exportFeed struct {
	Key       string `json:"key" csv:"key"`
	SourceURL string `json:"source_url" csv:"source_url"`
	Format    string `json:"format" csv:"format"`
	ItemCount int    `json:"item_count" csv:"item_count"`
	Detector  string `json:"detector" csv:"detector"`
	FetchedAt string `json:"fetched_at" csv:"fetched_at"`
	ExpiresAt string `json:"expires_at" csv:"expires_at"`
	Cached    bool   `json:"cached" csv:"cached"`
	Stale     bool   `json:"stale" csv:"stale"`
	LastError string `json:"last_error" csv:"last_error"`
}

// handleFeedsExport serves every feed the server has built in the requested
// format (OPML, CSV, or JSON). It reads through the same BuiltFeeds call the
// page does, so the export cannot disagree with the listing.
func (s *Server) handleFeedsExport(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "opml"
	}

	built, err := s.admin.BuiltFeeds(r.Context())
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	now := s.nowFn()

	switch format {
	case "opml":
		w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="feedme-%s.opml"`, now.Format("2006-01-02")))
		w.Header().Set("Cache-Control", "no-store")
		if err := writeOPML(w, built, now); err != nil {
			s.log.Error("opml write failed", "error", err.Error())
		}

	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="feedme-%s.csv"`, now.Format("2006-01-02")))
		w.Header().Set("Cache-Control", "no-store")
		if err := writeCSV(w, built); err != nil {
			s.log.Error("csv write failed", "error", err.Error())
		}

	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="feedme-%s.json"`, now.Format("2006-01-02")))
		w.Header().Set("Cache-Control", "no-store")
		if err := writeJSON(w, built); err != nil {
			s.log.Error("json write failed", "error", err.Error())
		}

	default:
		s.fail(w, r, http.StatusBadRequest, "unsupported format: "+format+" (use opml, csv, or json)")
	}
}

// writeOPML renders the feed list as OPML 2.0.
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

// writeCSV renders the feed list as CSV.
func writeCSV(w io.Writer, feeds []BuiltFeed) error {
	wr := csv.NewWriter(w)
	defer wr.Flush()

	header := []string{"key", "source_url", "format", "item_count", "detector", "fetched_at", "expires_at", "cached", "stale", "last_error"}
	if err := wr.Write(header); err != nil {
		return err
	}

	for _, f := range feeds {
		record := []string{
			f.Key,
			f.SourceURL,
			f.Format,
			strconv.Itoa(f.ItemCount),
			f.Detector,
			f.FetchedAt.Format(http.TimeFormat),
			f.ExpiresAt.Format(http.TimeFormat),
			strconv.FormatBool(f.Cached),
			strconv.FormatBool(f.Stale),
			f.LastError,
		}
		if err := wr.Write(record); err != nil {
			return err
		}
	}
	return nil
}

// writeJSON renders the feed list as JSON.
func writeJSON(w io.Writer, feeds []BuiltFeed) error {
	export := make([]exportFeed, len(feeds))
	for i, f := range feeds {
		export[i] = exportFeed{
			Key:       f.Key,
			SourceURL: f.SourceURL,
			Format:    f.Format,
			ItemCount: f.ItemCount,
			Detector:  f.Detector,
			FetchedAt: f.FetchedAt.Format(http.TimeFormat),
			ExpiresAt: f.ExpiresAt.Format(http.TimeFormat),
			Cached:    f.Cached,
			Stale:     f.Stale,
			LastError: f.LastError,
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(export)
}
