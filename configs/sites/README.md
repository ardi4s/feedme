# Site configs

**Optional.** A config only fills in fields the feed URL left empty, and is only
needed when a host's markup needs more help than selectors give it. The server
works on sites it has never seen; nothing here is required to read an ordinary
listing page.

One YAML file per host, or a list of them. The filename scopes the file
(`kompas.com.yaml` covers `kompas.com` and its subdomains); an explicit
`host:` key overrides that, and `*.example.com` is a wildcard.

A config with no host anywhere is rejected, because it would silently apply to
every site.

## Bundled configs

Two files ship here, both one line each:

- `news.google.com.yml` and `www.google.com.yml`, each `respect_robots: false`.

Google News and Google Alerts serve their feeds from paths their own `robots.txt`
disallows to every client. Those paths are published endpoints for readers, not
pages to be crawled, so the two files make them readable without every operator
meeting the same 403 first. Each covers exactly one host: `google.com` and
`mail.google.com` are unaffected, and `-no-robots` is still what turns the check
off everywhere.

Every other site should be read from a plain page. Add a file here only when
selectors are not enough, and prefer a `-site-dir` of your own over this
directory, so a config of yours cannot be confused with one of these.

## Additional keys

- `force_host`: rewrite every item link and image onto this bare host, keeping
  the path and query. For a site whose links point at a host that cannot be
  fetched while the article is served from one that can.
- `allow_cross_host: true`: keep links to other hosts. Off by default.
- `respect_robots: false`: read this host even though its `robots.txt` forbids
  the path. Google News and Google Alerts both serve their feeds from paths they
  disallow to every client, so a config like this is what makes them readable:

  ```yaml
  # configs/sites/news.google.com.yml
  respect_robots: false
  ```

  One host at a time, and it is a decision in a file rather than a flag, because
  the alternative — `-no-robots` — silences the check for every site at once.
  A config that does not mention this key keeps the global setting either way.

