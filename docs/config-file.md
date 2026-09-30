# Configuration File

`feedme serve -config config.yaml` reads a YAML file over the defaults:

```yaml
db_path: /var/lib/feedme/feedme.db
site_dir: /etc/feedme/sites
user_agent: "feedme/0.1 (+https://example.com/bot)"
timeout: 20s
max_body_bytes: 5242880
global_concurrency: 8
per_host_interval: 1s
respect_robots: true
allow_private: false
cache_ttl: 24h             # freshness window for cacheable fetches (robots.txt)
max_elements: 100000
render_url: http://chromium:3000
render_timeout: 30s
```

## All Options

| Option | Default | Description |
| --- | --- | --- |
| `db_path` | `$XDG_CONFIG_HOME/feedme/feedme.db` | SQLite database path |
| `site_dir` | `configs/sites` | Directory of per-host site configs |
| `user_agent` | `feedme/0.1 (+self-hosted full-text feed generator)` | User-Agent sent to source sites |
| `timeout` | `20s` | Per-request timeout |
| `max_body_bytes` | `5242880` (5 MiB) | Maximum response body size |
| `global_concurrency` | `8` | Maximum concurrent fetches globally |
| `per_host_interval` | `0` | Minimum gap between requests to the same host (e.g., `1s`) |
| `respect_robots` | `true` | Honor robots.txt by default |
| `allow_private` | `false` | Allow requests to private IP ranges (LAN testing) |
| `cache_ttl` | `24h` | Freshness window for cacheable fetches (robots.txt) |
| `max_elements` | `100000` | Maximum HTML elements to parse |
| `render_url` | | Base URL of headless browser service (browserless-compatible) |
| `render_timeout` | `30s` | Timeout for render requests |

All options can also be set via command-line flags (see `feedme serve -help`).
Command-line flags take precedence over the config file.
