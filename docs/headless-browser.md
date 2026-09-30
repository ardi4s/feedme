# Headless Browser

Some listings ship an empty shell over HTTP and fill it with JavaScript.
`render_js=1` handles those, but only when a browser is configured:

```sh
feedme serve -render-url http://localhost:3000
```

The server does not embed a browser. It speaks the browserless `POST /content`
contract over HTTP, so the browser runs as its own process — its memory use, its
crashes, and its attack surface stay out of the feed server.

## Compose Setup

The browser is optional and off by default. Nothing uses it unless a feed URL
carries `render_js=1`, and the ordinary HTML path never contacts it, so a
deployment without a browser still serves every listing that returns its items
in HTML. In the compose file the browser sits behind the `browser` profile:

```sh
# Server only (the default): no browser, no render_js.
docker compose up -d

# Server plus browser. They share the stack's own network, so the server
# reaches the browser by service name.
FEEDME_RENDER_URL=http://chromium:3000 docker compose --profile browser up -d
```

## Rendering Behavior

Rendering is a fallback, not the default path: the plain fetch is tried first,
and the browser is used only when that HTML yields no item list. The browser is
never published to the host; only the feed server reaches it, over the stack's
own network.

## Requirements

- A browserless-compatible service at `render_url`
- The `render_js=1` parameter in the feed URL
- Sufficient memory for the browser process (separate from the feed server)
