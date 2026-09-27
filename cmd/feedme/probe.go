package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"feedme/internal/config"
	"feedme/internal/extract"
	"feedme/internal/fetch"
)

// probeOutput is the machine-readable form of a probe run.
type probeOutput struct {
	URL         string            `json:"url"`
	FinalURL    string            `json:"final_url"`
	Status      int               `json:"status"`
	ContentType string            `json:"content_type"`
	Strategy    string            `json:"strategy"`
	Title       string            `json:"title"`
	Byline      string            `json:"byline,omitempty"`
	Published   string            `json:"published,omitempty"`
	SiteName    string            `json:"site_name,omitempty"`
	Image       string            `json:"image,omitempty"`
	Lang        string            `json:"lang,omitempty"`
	Canonical   string            `json:"canonical,omitempty"`
	WordCount   int               `json:"word_count"`
	Excerpt     string            `json:"excerpt,omitempty"`
	ContentHTML string            `json:"content_html,omitempty"`
	Attempts    []extract.Attempt `json:"attempts"`
	Error       string            `json:"error,omitempty"`
	Elapsed     string            `json:"elapsed"`
}

// runProbe fetches a URL and reports what the extraction cascade found. It is
// the primary debugging tool: when a site extracts badly, this is where you
// find out which strategy fired and why the others did not.
func runProbe(args []string) error {
	fs := newFlags()
	cfgPath := fs.String("config", "", "path to a YAML config file")
	siteDir := fs.String("site-dir", "configs/sites", "directory of per-host site configs")
	ua := fs.String("user-agent", "", "override the User-Agent")
	timeout := fs.String("timeout", "20s", "per-request timeout")
	noRobots := fs.Bool("no-robots", "ignore robots.txt")
	allowPrivate := fs.Bool("allow-private", "permit requests to private IP ranges (LAN testing)")
	input := fs.String("input", "", "read HTML from this file instead of fetching a URL")
	asJSON := fs.Bool("json", "emit JSON")
	showHTML := fs.Bool("html", "include the cleaned article HTML in the output")
	debug := fs.Bool("debug", "print extractor debug logging to stderr")

	rest, err := fs.Parse(args)
	if err != nil {
		if _, ok := err.(flagHelp); ok {
			fmt.Fprint(os.Stdout, "usage: feedme probe [flags] <url>\n\nFlags:\n")
			fmt.Fprint(os.Stdout, fs.usage())
			return nil
		}
		return err
	}

	var (
		pageURL = ""
		body    []byte
	)
	if *input != "" {
		data, err := os.ReadFile(*input)
		if err != nil {
			return err
		}
		body = data
		if len(rest) > 0 {
			pageURL = rest[0]
		} else {
			pageURL = "file://" + *input
		}
	} else {
		if len(rest) == 0 {
			fmt.Fprint(os.Stderr, "usage: feedme probe [flags] <url>\n\nFlags:\n")
			fmt.Fprint(os.Stderr, fs.usage())
			return errors.New("no URL given")
		}
		pageURL = rest[0]
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *ua != "" {
		cfg.UserAgent = *ua
	}
	if d, err := time.ParseDuration(*timeout); err == nil {
		cfg.Timeout = d
	}
	if *noRobots {
		f := false
		cfg.RespectRobots = &f
	}
	cfg.AllowPrivate = *allowPrivate

	sites, err := config.LoadSites(*siteDir)
	if err != nil {
		return fmt.Errorf("load site configs: %w", err)
	}

	var (
		out      probeOutput
		finalURL = pageURL
		status   = 0
		ctype    string
		elapsed  time.Duration
	)

	if body == nil {
		client := fetch.New(fetch.Options{
			UserAgent:      cfg.UserAgent,
			Timeout:        cfg.Timeout,
			MaxBodyBytes:   cfg.MaxBodyBytes,
			GlobalParallel: cfg.GlobalConcurrency,
			PerHostGap:     0, // probing a single page needs no politeness delay
			RespectRobots:  cfg.Robots(),
			AllowPrivate:   cfg.AllowPrivate,
			CacheTTL:       cfg.CacheTTL,
		})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout+10*time.Second)
		defer cancel()
		start := time.Now()
		resp, err := client.Get(ctx, pageURL, true)
		elapsed = time.Since(start)
		if err != nil {
			out.URL = pageURL
			out.Error = err.Error()
			out.Elapsed = elapsed.String()
			return emit(&out, *asJSON, *showHTML, fs)
		}
		decoded, derr := fetch.DecodeBody(resp.ContentType, resp.Body)
		if derr != nil {
			decoded = resp.Body
		}
		body = decoded
		finalURL = resp.URL
		status = resp.Status
		ctype = resp.ContentType
	}

	out.URL = pageURL
	out.FinalURL = finalURL
	out.Status = status
	out.ContentType = ctype
	out.Elapsed = elapsed.String()

	host := hostOf(finalURL)
	var site *config.Site
	if s := sites.Get(host); s != nil {
		site = s
	}

	var logger *slog.Logger
	if *debug {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	art, xerr := extract.FromHTML(body, extract.Options{
		PageURL:     finalURL,
		Site:        site,
		MaxElements: cfg.MaxElements,
		Prune:       true,
		Logger:      logger,
	})
	if art != nil {
		out.Strategy = art.Strategy
		out.Title = art.Title
		out.Byline = art.Byline
		out.SiteName = art.SiteName
		out.Image = art.Image
		out.Lang = art.Lang
		out.Canonical = art.Canonical
		out.WordCount = art.WordCount
		out.Excerpt = art.Excerpt
		out.Attempts = art.Attempts
		if !art.Published.IsZero() {
			out.Published = art.Published.UTC().Format(time.RFC3339)
		}
		if *showHTML {
			out.ContentHTML = art.ContentHTML
		}
	}
	if xerr != nil {
		out.Error = xerr.Error()
		// Distinguish "this is not an article page" from "this article is
		// rendered by JavaScript, which this tool does not execute". The
		// second is the actionable one.
		if errors.Is(xerr, extract.ErrNoContent) && !readabilityLikely(body) {
			out.Error += " — the page has almost no server-rendered text, so it is " +
				"probably built client-side; feedme does not execute JavaScript"
		}
	}
	return emit(&out, *asJSON, *showHTML, fs)
}

// readabilityLikely reports whether a response looks like it contains real
// server-rendered prose.
func readabilityLikely(body []byte) bool {
	lower := strings.ToLower(string(body))
	// A typical SPA shell is small and has few closing paragraph tags.
	return len(body) > 8000 || strings.Count(lower, "</p>") > 3
}

func hostOf(raw string) string {
	s := raw
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "]") {
		s = s[:i]
	}
	return strings.ToLower(s)
}

func emit(out *probeOutput, asJSON, showHTML bool, fs *flagSet) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	w := os.Stdout
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%-11s %s\n", k, v)
		}
	}
	fmt.Fprintln(w, "── fetch ──────────────────────────────")
	line("URL", out.URL)
	line("Final", out.FinalURL)
	if out.Status != 0 {
		line("Status", fmt.Sprintf("%d", out.Status))
	}
	line("Type", out.ContentType)
	line("Took", out.Elapsed)

	fmt.Fprintln(w, "\n── extraction ─────────────────────────")
	if out.Strategy != "" {
		line("Strategy", out.Strategy)
	}
	line("Title", out.Title)
	line("Author", out.Byline)
	line("Published", out.Published)
	line("Site", out.SiteName)
	line("Lang", out.Lang)
	line("Canonical", out.Canonical)
	line("Words", fmt.Sprintf("%d", out.WordCount))
	if out.Image != "" {
		line("Image", out.Image)
	}
	if out.Excerpt != "" {
		line("Excerpt", truncate(out.Excerpt, 200))
	}

	if len(out.Attempts) > 0 {
		fmt.Fprintln(w, "\n── cascade ────────────────────────────")
		for _, a := range out.Attempts {
			mark := "fail"
			if a.OK {
				mark = "OK  "
			}
			fmt.Fprintf(w, "  [%s] %-12s %s\n", mark, a.Name, a.Reason)
		}
	}
	if out.Error != "" {
		fmt.Fprintln(w, "\n── problem ────────────────────────────")
		fmt.Fprintf(w, "  %s\n", out.Error)
	}
	if showHTML && out.ContentHTML != "" {
		fmt.Fprintln(w, "\n── content ────────────────────────────")
		fmt.Fprintln(w, out.ContentHTML)
	}
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
