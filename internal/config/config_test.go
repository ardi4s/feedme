package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSiteFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A config with no host key must not leak onto every site. This regressed
// because Host was unmarshalled as yaml:"-", so every file fell into the
// global bucket and a Kompas override would have been applied to the BBC.
func TestLoadSitesScopesByFilename(t *testing.T) {
	dir := t.TempDir()
	writeSiteFile(t, dir, "kompas.com.yaml", "body: ['.article']\n")

	s, err := LoadSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get("kompas.com"); got == nil {
		t.Fatal("kompas.com should have an override")
	}
	// A bare host config must cover its subdomains: Kompas alone publishes on
	// surabaya., regional., tekno. and dozens more.
	if got := s.Get("surabaya.kompas.com"); got == nil {
		t.Error("a kompas.com config should cover surabaya.kompas.com")
	}
	for _, other := range []string{"bbc.com", "example.org", "news.google.com"} {
		if got := s.Get(other); got != nil {
			t.Errorf("override for kompas.com leaked onto %s: %+v", other, got)
		}
	}
}

func TestLoadSitesHonoursHostKey(t *testing.T) {
	dir := t.TempDir()
	// The filename deliberately disagrees with the host key; the key wins.
	writeSiteFile(t, dir, "misleading-name.yaml", "host: bbc.com\nbody: ['.a']\n")

	s, err := LoadSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("bbc.com") == nil {
		t.Error("host: key should scope the config to bbc.com")
	}
	if s.Get("misleading-name.com") != nil {
		t.Error("filename should not scope the config when host: is present")
	}
}

// A list-form config must register every entry. It previously kept only the
// first, silently dropping the rest of the file.
func TestLoadSitesRegistersEveryEntryInList(t *testing.T) {
	dir := t.TempDir()
	writeSiteFile(t, dir, "multi.yaml", `
- host: bbc.com
  body: ['.a']
- host: example.org
  body: ['.b']
- host: "*.theguardian.com"
  strip: ['.comments']
`)

	s, err := LoadSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("bbc.com") == nil {
		t.Error("first list entry missing")
	}
	if s.Get("example.org") == nil {
		t.Error("second list entry was dropped")
	}
	// Wildcards match parent domains, so the article subdomain must resolve.
	got := s.Get("www.theguardian.com")
	if got == nil {
		t.Fatal("wildcard entry did not match a subdomain")
	}
	if len(got.Strip) != 1 || got.Strip[0] != ".comments" {
		t.Errorf("wrong wildcard config matched: %+v", got)
	}
}

// A config with no host anywhere is almost always a mistake, because it would
// apply to every site. It must be reported rather than silently going global.
func TestLoadSitesRejectsHostlessConfig(t *testing.T) {
	dir := t.TempDir()
	// The filename must also fail to yield a host, so the space is deliberate.
	writeSiteFile(t, dir, "orphan config.yaml", "body: ['.a']\n")

	if _, err := LoadSites(dir); err == nil {
		t.Fatal("expected an error for a config with no host")
	}
}

func TestHostFromFilename(t *testing.T) {
	cases := map[string]string{
		"kompas.com.yaml":        "kompas.com",
		"kompas.com.yml":         "kompas.com",
		"*.bbc.co.uk.yaml":       "*.bbc.co.uk",
		"weird name.yaml":        "",
		"has/slash.yaml":         "",
		"http://example.yaml":    "",
		"trailing..yaml":         "",
		"  spaced.example.com.y": "spaced.example.com",
		"UPPER.COM.yaml":         "upper.com",
		"no-ext":                 "no-ext",
	}
	for in, want := range cases {
		if got := hostFromFilename(in); got != want {
			t.Errorf("hostFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookupPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeSiteFile(t, dir, "global.yaml", "host: '*'\ntitle: ['global']\n")
	writeSiteFile(t, dir, "*.example.com.yaml", "title: ['wildcard']\n")
	writeSiteFile(t, dir, "a.example.com.yaml", "title: ['exact']\n")

	s, err := LoadSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	chain := s.Lookup("a.example.com")
	if len(chain) != 3 {
		t.Fatalf("expected exact + wildcard + global, got %d entries", len(chain))
	}
	if chain[0].Title[0] != "exact" {
		t.Errorf("exact host should win, got %q", chain[0].Title[0])
	}
	if chain[len(chain)-1].Title[0] != "global" {
		t.Errorf("global should be last, got %q", chain[len(chain)-1].Title[0])
	}
}

func TestLoadSitesMissingDirIsEmpty(t *testing.T) {
	s, err := LoadSites(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("a missing directory should not be an error: %v", err)
	}
	if s.Get("example.com") != nil {
		t.Error("expected an empty store")
	}
}

// force_host and allow_cross_host are the two keys beyond FiveFilter's format,
// and they must survive parsing with the types the pipeline expects.
func TestLoadSitesReadsForceHostAndCrossHost(t *testing.T) {
	dir := t.TempDir()
	writeSiteFile(t, dir, "kontan.co.id.yaml",
		"host: kontan.co.id\nforce_host: www.kontan.co.id\nallow_cross_host: true\n")

	s, err := LoadSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	site := s.Get("www.kontan.co.id")
	if site == nil {
		t.Fatal("no config for www.kontan.co.id")
	}
	if site.ForceHost != "www.kontan.co.id" {
		t.Errorf("ForceHost = %q", site.ForceHost)
	}
	if !site.CrossHost() {
		t.Error("CrossHost should be true")
	}

	// Absent means off, not true.
	writeSiteFile(t, dir, "other.com.yaml", "host: other.com\n")
	s2, err := LoadSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get("other.com"); got.CrossHost() {
		t.Error("CrossHost should default to false")
	}
}
