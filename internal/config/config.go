// Package config holds global settings and per-host site overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the global runtime configuration.
type Config struct {
	DBPath            string        `yaml:"db_path"`
	SiteDir           string        `yaml:"site_dir"`
	UserAgent         string        `yaml:"user_agent"`
	Timeout           time.Duration `yaml:"timeout"`
	MaxBodyBytes      int64         `yaml:"max_body_bytes"`
	GlobalConcurrency int           `yaml:"global_concurrency"`
	PerHostInterval   time.Duration `yaml:"per_host_interval"`
	RespectRobots     *bool         `yaml:"respect_robots"`
	AllowPrivate      bool          `yaml:"allow_private"`
	CacheTTL          time.Duration `yaml:"cache_ttl"`
	MaxElements       int           `yaml:"max_elements"`
	// RenderURL is the base URL of a headless-browser service (browserless
	// compatible). Empty disables render_js.
	RenderURL string `yaml:"render_url"`
	// RenderTimeout bounds one browser render.
	RenderTimeout time.Duration `yaml:"render_timeout"`
}

// Default returns the built-in defaults.
func Default() *Config {
	return &Config{
		DBPath:            defaultDBPath(),
		SiteDir:           "configs/sites",
		UserAgent:         "feedme/0.1 (+self-hosted full-text feed generator)",
		Timeout:           20 * time.Second,
		MaxBodyBytes:      5 << 20,
		GlobalConcurrency: 8,
		PerHostInterval:   time.Second,
		RespectRobots:     boolPtr(true),
		AllowPrivate:      false,
		CacheTTL:          24 * time.Hour,
		MaxElements:       100000,
		RenderURL:         "",
		RenderTimeout:     30 * time.Second,
	}
}

func boolPtr(b bool) *bool { return &b }

func defaultDBPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "feedme", "feedme.db")
	}
	return "feedme.db"
}

// Robots reports whether robots.txt should be honoured.
func (c *Config) Robots() bool {
	return c.RespectRobots == nil || *c.RespectRobots
}

// Load reads a YAML config file over the defaults. A missing path is not an
// error: the defaults are returned unchanged.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = Default().MaxBodyBytes
	}
	if cfg.GlobalConcurrency <= 0 {
		cfg.GlobalConcurrency = Default().GlobalConcurrency
	}
	return cfg, nil
}

// Site is a per-host override. Directive names mirror FiveFilter's site config
// format so existing ftr-site-config rules can be ported with minimal edits.
// Selector values are CSS by default; prefix with "xpath:" to use XPath.
type Site struct {
	// Host scopes the override. It may be given as a `host:` key, or implied
	// by the filename ("kompas.com.yaml", "*.example.com.yaml"). A config
	// with no host applies to every site, which is rarely intended.
	Host    string   `yaml:"host"`
	Title   []string `yaml:"title"`
	Body    []string `yaml:"body"`
	Strip   []string `yaml:"strip"`
	Item    []string `yaml:"item"`
	URL     []string `yaml:"url"`
	Date    []string `yaml:"date"`
	Summary []string `yaml:"summary"`

	StripIDOrClass      []string `yaml:"strip_id_or_class"`
	AllowCrossHost      *bool    `yaml:"allow_cross_host"`
	ForceHost           string   `yaml:"force_host"`
	Tidy                *bool    `yaml:"tidy"`
	Prune               *bool    `yaml:"prune"`
	AutodetectOnFailure *bool    `yaml:"autodetect_on_failure"`
	SinglePageLink      []string `yaml:"single_page_link"`
	NextPageLink        []string `yaml:"next_page_link"`

	Categories []string `yaml:"categories"`
	TestURLs   []string `yaml:"test_urls"`

	// RespectRobots overrides the global robots.txt decision for this host.
	// Nil states no opinion.
	//
	// It exists for the publishers who forbid automated clients from the very
	// path that serves their feed — Google News and Google Alerts both do — and
	// it is a per-host opt-in on purpose. Turning the check off everywhere is
	// one flag away, and one flag away is not a decision an operator should make
	// by accident.
	RespectRobots *bool `yaml:"respect_robots"`
}

// CrossHost reports whether a site config allows links to sibling subdomains.
func (s *Site) CrossHost() bool { return s.AllowCrossHost != nil && *s.AllowCrossHost }

// Autodetect reports whether extraction may fall back to auto-detection when
// the configured selectors fail to match. Defaults to true, like FiveFilter.
func (s *Site) Autodetect() bool {
	return s.AutodetectOnFailure == nil || *s.AutodetectOnFailure
}

// PruneEnabled reports whether the prune pass runs. Defaults to true.
func (s *Site) PruneEnabled() bool {
	return s.Prune == nil || *s.Prune
}

// Store loads site overrides from a directory of YAML files. Files are keyed
// by their contents, not their filename, so a config can live in any file.
type Store struct {
	byHost    map[string]*Site
	wildcards map[string]*Site
	global    []*Site
	all       []*Site
}

// LoadSites reads every *.yaml / *.yml in dir. A missing directory yields an
// empty store rather than an error.
func LoadSites(dir string) (*Store, error) {
	s := &Store{byHost: map[string]*Site{}, wildcards: map[string]*Site{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		sites, err := parseSiteFile(p)
		if err != nil {
			return nil, err
		}
		if len(sites) == 0 {
			continue
		}
		// A file that does not name its hosts is scoped by its filename, so
		// "kompas.com.yaml" only ever applies to kompas.com.
		fileHost := hostFromFilename(e.Name())
		for _, site := range sites {
			if site.Host == "" {
				site.Host = fileHost
			}
			if site.Host == "" {
				return nil, fmt.Errorf(
					"parse %s: site has no host; add a host: key or name the file after its host", p)
			}
			s.add(site)
		}
	}
	return s, nil
}

// hostFromFilename derives a host scope from a config filename, stripping the
// extension. "kompas.com.yaml" gives "kompas.com", "*.bbc.co.uk.yml" gives
// "*.bbc.co.uk". A name that is not a plausible hostname yields "", which
// makes the caller report the config as unscoped.
func hostFromFilename(name string) string {
	base := strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
	base = strings.ToLower(base)
	if base == "" || strings.ContainsAny(base, "/\\:@ ") {
		return ""
	}
	host := base
	if strings.HasPrefix(host, "*.") {
		host = strings.TrimPrefix(host, "*.")
	}
	if !plausibleHost(host) {
		return ""
	}
	return base
}

// plausibleHost reports whether s looks like a hostname: dot-separated
// non-empty labels of letters, digits and hyphens, with no leading or
// trailing dot.
func plausibleHost(s string) bool {
	if s == "" || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return false
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return false
			}
		}
	}
	return true
}

// parseSiteFile reads one config file, which may hold a single site or a list
// of them. Every site in the file is returned; none are silently dropped.
func parseSiteFile(path string) ([]*Site, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Try the list form first: a single mapping unmarshals into a list as an
	// error, so this cannot shadow the single-site case.
	var list []*Site
	if err := yaml.Unmarshal(data, &list); err == nil {
		return list, nil
	}
	var one Site
	if err := yaml.Unmarshal(data, &one); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return []*Site{&one}, nil
}

func (s *Store) add(site *Site) {
	if site == nil {
		return
	}
	s.all = append(s.all, site)
	host := strings.ToLower(strings.TrimSpace(site.Host))
	switch {
	case host == "" || host == "*":
		s.global = append(s.global, site)
	case strings.HasPrefix(host, "*."):
		base := strings.TrimPrefix(host, "*.")
		s.wildcards[base] = site
	default:
		s.byHost[host] = site
	}
}

// Lookup returns the merged override chain for a hostname, highest priority
// first: exact host, then a bare parent host, then wildcard parent domains,
// then global. Later entries fill in fields the earlier ones left empty.
func (s *Store) Lookup(host string) []*Site {
	host = strings.ToLower(host)
	var chain []*Site
	if site, ok := s.byHost[host]; ok {
		chain = append(chain, site)
	}
	// Walk parent domains so "surabaya.kompas.com" is covered by a
	// "kompas.com" config and by a "*.kompas.com" wildcard.
	parent := host
	for {
		i := strings.Index(parent, ".")
		if i < 0 || i == len(parent)-1 {
			break
		}
		parent = parent[i+1:]
		if site, ok := s.byHost[parent]; ok && !containsSite(chain, site) {
			chain = append(chain, site)
		}
		if site, ok := s.wildcards[parent]; ok {
			chain = append(chain, site)
		}
	}
	chain = append(chain, s.global...)
	return chain
}

func containsSite(chain []*Site, site *Site) bool {
	for _, c := range chain {
		if c == site {
			return true
		}
	}
	return false
}

// Get returns the single best override for a host, or nil.
func (s *Store) Get(host string) *Site {
	chain := s.Lookup(host)
	if len(chain) == 0 {
		return nil
	}
	return chain[0]
}

// RobotsFor reports whether robots.txt should be honoured for a host, given the
// global setting.
//
// The lookup walks the same override chain the selectors use and takes the first
// config that states an opinion, so an exact-host config can override a parent
// domain's and a config that says nothing is never treated as permission. A host
// nobody configured keeps the global setting.
func (s *Store) RobotsFor(host string, global bool) bool {
	for _, site := range s.Lookup(host) {
		if site.RespectRobots != nil {
			return *site.RespectRobots
		}
	}
	return global
}

// Count reports how many site configs were loaded. It is used for a startup log
// line, so that a server coming up with an empty site directory is visible
// rather than silently doing nothing.
func (s *Store) Count() int { return len(s.all) }
