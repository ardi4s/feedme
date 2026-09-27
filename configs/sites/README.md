# Site configs

**Optional.** A config only fills in fields the feed URL left empty, and is only
needed when a host's markup needs more help than selectors give it.

One YAML file per host, or a list of them. The filename scopes the file
(`kompas.com.yaml` covers `kompas.com` and its subdomains); an explicit
`host:` key overrides that, and `*.example.com` is a wildcard.

A config with no host anywhere is rejected, because it would silently apply to
every site.

## Additional keys

- `force_host`: rewrite every item link and image onto this bare host, keeping
  the path and query. For a site whose links point at a host that cannot be
  fetched while the article is served from one that can.
- `allow_cross_host: true`: keep links to other hosts. Off by default.

