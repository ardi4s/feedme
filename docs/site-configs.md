# Site Configs

Most pages need nothing beyond the feed URL. When a host's markup needs more
help than selectors give it, drop a YAML file in `configs/sites/`. The filename
scopes it (`kompas.com.yaml` covers `kompas.com` and its subdomains); a `host:`
key overrides that, and `*.example.com` is a wildcard. A config with no host
anywhere is rejected.

## Bundled Configs

The following configs ship with the binary:

| File | Setting | Purpose |
| --- | --- | --- |
| `news.google.com.yml` | `respect_robots: false` | Google News RSS path is disallowed by robots.txt but is a published reader endpoint |
| `www.google.com.yml` | `respect_robots: false` | Google Alerts feed path is disallowed by robots.txt but is a published reader endpoint |
| `medium.com.yml` | `render_js: true` | Medium article pages need JavaScript rendering for full text |

No other configs are bundled. A config is read only for the host it names, and
only fills in fields the request left empty: a selector given in the feed URL
always wins.

## Available Keys

| Key | Description |
| --- | --- |
| `title` | Feed title override |
| `body` | Body selector override |
| `strip` | Selectors to strip text from (combined with request's `remove`) |
| `item` | Item container selector |
| `url` | Item link selector |
| `date` | Item date selector |
| `summary` | Item summary selector |
| `strip_id_or_class` | Strip element by id/class |
| `tidy` | HTML tidy options |
| `prune` | Prune selectors |
| `autodetect_on_failure` | Enable auto-detection when selectors fail |
| `single_page_link` | Single page link selector |
| `next_page_link` | Pagination next link selector |
| `categories` | Category selectors |
| `test_urls` | URLs to test the config against |
| `force_host` | Rewrite links to this host |
| `allow_cross_host` | Allow cross-host links |
| `respect_robots` | Override robots.txt check for this host |

Selectors given in the URL win over the site config, which only fills in fields
the request left empty. A `remove` list is the exception: the site config's
`strip` and the request's `remove` are combined, never narrowed.

## Config File Location

- Default: `configs/sites/` (relative to working directory)
- Override with `-site-dir` flag or `site_dir` in config file
