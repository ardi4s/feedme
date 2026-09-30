# Security

## robots.txt

robots.txt is honoured by default. A site config may opt one host out
(`respect_robots: false`), which is what makes a feed readable at a publisher
that forbids its own feed path; `-no-robots` turns the check off everywhere and
is the heavier of the two.

## SSRF Protection

Hostnames resolving to private, loopback, link-local, or reserved addresses are
refused unless `-allow-private` is set.

## Size Limits

- Response bodies are capped (`max_body_bytes`, default 5 MiB)
- Number of HTML elements parsed is capped (`max_elements`, default 100,000)
- Fetches are bounded by a global concurrency limit (`global_concurrency`, default 8)
- Per-host interval limits request rate to the same host (`per_host_interval`)

## Management Page Authentication

**The management page is unauthenticated until a token is set.** Set
`FEEDME_ADMIN_TOKEN` before exposing `/feeds` to the internet. The feed
endpoints (`/extract`, `/preview`, `/check`, `/healthz`) stay public either way.

The token can be provided via:
- Environment variable `FEEDME_ADMIN_TOKEN` (recommended)
- Command-line flag `-admin-token` (visible in process list)

A browser gets a session cookie scoped to `/feeds` via the `/login` form.
Scripts can use `Authorization: Bearer <token>` or Basic auth with the token as
password.

## No Per-Client Rate Limiting

Put the server behind a reverse proxy if it is exposed to the internet, and set
a real `user_agent` so site operators can contact you.
