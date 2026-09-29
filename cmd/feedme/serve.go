package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"feedme/internal/config"
	"feedme/internal/fetch"
	"feedme/internal/gnews"
	"feedme/internal/pipeline"
	"feedme/internal/render"
	"feedme/internal/store"
	"feedme/internal/urlx"
	"feedme/internal/web"
)

// serveCommand wires the pieces together and runs an HTTP server.
//
// The wiring lives here rather than in internal/web because it is the only
// place that knows about all of them at once. Everything below this function is
// independently testable; this is glue.
func runServe(args []string) error {
	fs := newFlags()
	cfgPath := fs.String("config", "", "path to a YAML config file")
	siteDir := fs.String("site-dir", "", "directory of per-host site configs (overrides the config file)")
	addr := fs.String("addr", ":8080", "listen address")
	dbPath := fs.String("db", "", "SQLite path (overrides the config file)")
	ua := fs.String("user-agent", "", "override the User-Agent sent to source sites")
	timeout := fs.String("timeout", "", "per-request timeout, e.g. 20s")
	parallel := fs.String("fulltext-parallel", "4", "how many article bodies to fetch at once")
	renderURL := fs.String("render-url", "", "base URL of a headless browser (browserless); enables render_js")
	adminToken := fs.String("admin-token", "", "password for the /feeds management page (default $FEEDME_ADMIN_TOKEN)")
	noRobots := fs.Bool("no-robots", "ignore robots.txt")
	allowPrivate := fs.Bool("allow-private", "permit requests to private IP ranges (LAN testing)")
	logLevel := fs.String("log-level", "info", "debug, info, warn or error")
	quiet := fs.Bool("quiet", "log warnings and errors only")

	rest, err := fs.Parse(args)
	if err != nil {
		if _, ok := err.(flagHelp); ok {
			fmt.Fprint(os.Stdout, "usage: feedme serve [flags]\n\nFlags:\n")
			fmt.Fprint(os.Stdout, fs.usage())
			return nil
		}
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("serve takes no arguments, got %q", strings.Join(rest, " "))
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *ua != "" {
		cfg.UserAgent = *ua
	}
	if *timeout != "" {
		d, err := time.ParseDuration(*timeout)
		if err != nil {
			return fmt.Errorf("parse -timeout: %w", err)
		}
		cfg.Timeout = d
	}
	if *siteDir != "" {
		cfg.SiteDir = *siteDir
	}
	if *renderURL != "" {
		cfg.RenderURL = *renderURL
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *noRobots {
		f := false
		cfg.RespectRobots = &f
	}
	cfg.AllowPrivate = *allowPrivate

	// The environment is the better place for a secret than a flag: a flag is
	// visible in the process list to every user on the machine. The flag wins
	// when both are given, so a one-off local run can override a deployed env.
	manageToken := *adminToken
	if manageToken == "" {
		manageToken = strings.TrimSpace(os.Getenv("FEEDME_ADMIN_TOKEN"))
	}

	level := slog.LevelInfo
	switch strings.ToLower(*logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	if *quiet && level < slog.LevelWarn {
		level = slog.LevelWarn
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	sites, err := config.LoadSites(cfg.SiteDir)
	if err != nil {
		return fmt.Errorf("load site configs: %w", err)
	}

	robots := cfg.RespectRobots == nil || *cfg.RespectRobots

	client := fetch.New(fetch.Options{
		UserAgent:      cfg.UserAgent,
		Timeout:        cfg.Timeout,
		MaxBodyBytes:   cfg.MaxBodyBytes,
		GlobalParallel: cfg.GlobalConcurrency,
		PerHostGap:     cfg.PerHostInterval,
		RespectRobots:  robots,
		AllowPrivate:   cfg.AllowPrivate,
		CacheTTL:       cfg.CacheTTL,
		// A site config may opt one host out of the check, which is the only
		// way to read a feed whose publisher forbids automated clients from
		// that very path. Every other host keeps the global decision.
		RobotsFor: func(host string) bool {
			return sites.RobotsFor(host, robots)
		},
		CacheGet: func(ctx context.Context, u string) (*fetch.Response, bool) {
			c, err := st.CacheGet(ctx, u)
			if err != nil || c == nil {
				return nil, false
			}
			return &fetch.Response{
				URL: c.FinalURL, Status: c.Status, Body: c.Body,
				ContentType: c.ContentType, ETag: c.ETag,
				LastModified: c.LastModified, FromCache: true, FetchedAt: c.FetchedAt,
			}, true
		},
		OnStore: func(ctx context.Context, r *fetch.Response) error {
			return st.CachePut(ctx, &store.CachedResponse{
				URL: r.URL, Status: r.Status, ETag: r.ETag, LastModified: r.LastModified,
				FinalURL: r.URL, ContentType: r.ContentType, Body: r.Body,
				FetchedAt: r.FetchedAt, ExpiresAt: r.FetchedAt.Add(cfg.CacheTTL),
			})
		},
	})

	pipe := &pipeline.Pipeline{
		Fetch:    client,
		Sites:    siteLookup{sites: sites},
		Parallel: intOr(*parallel, 4),
		// Google News and Google Alerts publish links that have to be opened
		// before the publisher's page can be read. Opening them costs a request
		// per item, so it happens only for full text, which is the only stage
		// that needs the publisher's URL at all.
		Links: gnews.NewResolver(client, logger),
	}
	if cfg.RenderURL != "" {
		// The browser is a separate service. Configuring one is what turns
		// render_js from a clear error into a working feature.
		pipe.Renderer = render.NewHTTP(cfg.RenderURL, cfg.RenderTimeout, cfg.MaxBodyBytes)
	}

	handler := web.New(web.Options{
		Pipeline:    pipe,
		Logger:      logger,
		Cache:       storeFeedCache{st: st},
		Recorder:    storeRecorder{st: st},
		Admin:       storeAdmin{st: st},
		ManageToken: manageToken,
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a full-text feed over forty articles can take a
		// while, and cutting it off produces a truncated document that a reader
		// stores as if it were whole.
		IdleTimeout: 120 * time.Second,
	}

	// A periodic prune keeps the caches and the build history from growing
	// without bound. It runs once at startup as well, so a restart after a long
	// idle period does not leave expired rows behind until the first tick.
	pruneOnce := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := st.CachePrune(ctx); err != nil {
			logger.Warn("prune http cache", "error", err)
		}
		if _, err := st.FeedCachePrune(ctx); err != nil {
			logger.Warn("prune feed cache", "error", err)
		}
		// Twenty builds per feed is enough to see what changed. The newest row
		// of every feed is kept whatever this number is, because the management
		// page lists feeds from this table.
		if _, err := st.FeedBuildsPrune(ctx, 20); err != nil {
			logger.Warn("prune feed builds", "error", err)
		}
	}
	prune := time.NewTicker(1 * time.Hour)
	defer prune.Stop()
	go func() {
		pruneOnce()
		for range prune.C {
			pruneOnce()
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening",
			"addr", *addr,
			"db", cfg.DBPath,
			"sites", sites.Count(),
			"robots", robots,
			"manage_auth", manageToken != "",
			"user_agent", cfg.UserAgent)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// siteLookup adapts the config store to the interface the pipeline wants, so
// that pipeline does not depend on how site configs are stored on disk.
type siteLookup struct {
	sites *config.Store
}

func (l siteLookup) SiteFor(host string) *pipeline.SiteConfig {
	site := l.sites.Get(urlx.Host("https://" + host))
	if site == nil {
		return nil
	}
	return &pipeline.SiteConfig{
		Item:           site.Item,
		URL:            site.URL,
		Title:          site.Title,
		Date:           site.Date,
		Summary:        site.Summary,
		Strip:          site.Strip,
		ForceHost:      site.ForceHost,
		AllowCrossHost: site.CrossHost(),
	}
}

func intOr(s string, def int) int {
	n := def
	if s != "" {
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
			return def
		}
	}
	if n < 1 {
		return def
	}
	return n
}
