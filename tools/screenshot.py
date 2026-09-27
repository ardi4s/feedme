#!/usr/bin/env python3
"""Regenerate the screenshots the README embeds.

``tools/brand.py`` draws the card and the icons from the mark the server already
embeds, so those cannot drift away from the logo. The two screenshots are
different: they are photographs of a running instance, and drawing cannot
reproduce them. This script drives a running server through a browserless
service instead, so a layout change can be re-photographed rather than retouched
by hand.

    python3 tools/screenshot.py            # every shot
    python3 tools/screenshot.py home       # just one

Two things have to be reachable. A feedme server, ``--app``, and a browserless
service, ``--browserless``. The browser is the side that fetches the page, so
``--app`` is resolved from inside the browser container.

The compose service publishes no port on purpose, so a throwaway browser is
pointed at the stack's own network instead. ``feedme_default`` is the network of
a stack started in this directory:

    docker run -d --rm --name shot-browser --network feedme_default \
      --shm-size=1g -p 127.0.0.1:3000:3000 browserless/chrome:latest
    python3 tools/screenshot.py
    docker rm -f shot-browser

The viewport and the action taken on each page are fixed here, so a re-run
produces the same framing and a diff only shows what actually changed. The
script prints the width and height of every page it captured, which makes a
clipped or empty render visible without opening the image.

Only the standard library is used, so this adds no dependency alongside the
Pillow that ``brand.py`` needs.
"""

from __future__ import annotations

import argparse
import base64
import json
import pathlib
import sys
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent

# Every shot: the page to open, the browser window, and what to do on the page
# before the shutter. The height is chosen so the whole page fits in the frame —
# a screenshot wider than its content reads as a mistake, and one that is too
# short cuts a sentence in half.
SHOTS: dict[str, dict] = {
    "home": {
        "path": "home.png",
        "url": "/",
        "width": 1280,
        "height": 780,
        # Typed into the listing field so the image shows the builder in use.
        # A public listing page, so nothing about this machine ends up in it.
        "fill": ("#url", "https://news.ycombinator.com/newest"),
    },
    "hero": {
        "path": "hero.png",
        "url": "/feeds",
        "width": 1280,
        "height": 860,
        "fill": None,
    },
}

# The page is fetched by the browser, so this has to resolve inside that
# container, not on the host.
DEFAULT_APP = "http://feedme:8080"
DEFAULT_BROWSERLESS = "http://127.0.0.1:3000"


def page_code(app: str, shot: dict) -> str:
    """The browserless function body: open the page, pose it, photograph it.

    The returned value carries the PNG and a few measurements, so a render that
    silently clipped its own content is reported instead of being discovered
    later by eye.
    """
    fill = ""
    if shot["fill"]:
        selector, text = shot["fill"]
        fill = (
            f"  await page.waitForSelector({json.dumps(selector)}, {{ timeout: 20000 }});\n"
            f"  await page.click({json.dumps(selector)});\n"
            f"  await page.type({json.dumps(selector)}, {json.dumps(text)}, {{ delay: 15 }});\n"
            f"  await page.evaluate((sel) => document.querySelector(sel).blur(), {json.dumps(selector)});\n"
        )
    return f"""
module.exports = async ({{ page }}) => {{
  await page.setViewport({{ width: {shot['width']}, height: {shot['height']}, deviceScaleFactor: 2 }});
  await page.goto({json.dumps(app + shot['url'])}, {{ waitUntil: "networkidle0", timeout: 45000 }});
{fill}  await new Promise((r) => setTimeout(r, 800));
  const box = await page.evaluate(() => {{
    const bottom = (sel) => {{
      const el = document.querySelector(sel);
      return el ? Math.round(el.getBoundingClientRect().bottom) : null;
    }};
    return {{
      title: document.title,
      scrollHeight: document.documentElement.scrollHeight,
      tabsBottom: bottom(".tabs"),
      actionsBottom: bottom(".actions"),
    }};
  }});
  const buf = await page.screenshot({{ type: "png" }});
  return {{
    type: "application/json",
    data: Buffer.from(JSON.stringify({{ png: buf.toString("base64"), box }})).toString("base64"),
  }};
}};
"""


def decode(body: str) -> dict:
    """Unwrap the payload browserless returns.

    A JSON result comes back base64-encoded, and depending on the version the
    base64 is sometimes JSON-quoted on top of that, so both layers are peeled
    rather than assumed away.
    """
    body = body.strip()
    try:
        outer = json.loads(body)
    except ValueError:
        outer = None
    if isinstance(outer, str):
        return json.loads(base64.b64decode(outer))
    if isinstance(outer, dict) and isinstance(outer.get("data"), str):
        return json.loads(base64.b64decode(outer["data"]))
    if isinstance(outer, dict):
        return outer
    return json.loads(base64.b64decode(body))


def shoot(browserless: str, app: str, shot: dict) -> tuple[bytes, dict]:
    req = urllib.request.Request(
        browserless.rstrip("/") + "/function",
        data=json.dumps({"code": page_code(app, shot)}).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=120) as r:
        out = decode(r.read().decode())
    png = base64.b64decode(out["png"])
    if not png.startswith(b"\x89PNG"):
        raise ValueError(f"answer was not a PNG, started with {png[:8]!r}")
    return png, out["box"]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("shots", nargs="*", choices=sorted(SHOTS), metavar="SHOT",
                    help=f"which shots to take (default: all of {', '.join(sorted(SHOTS))})")
    ap.add_argument("--browserless", default=DEFAULT_BROWSERLESS,
                    help=f"browserless base URL (default: {DEFAULT_BROWSERLESS})")
    ap.add_argument("--app", default=DEFAULT_APP,
                    help=f"feedme base URL, as the browser sees it (default: {DEFAULT_APP})")
    ap.add_argument("--out", default=str(ROOT / "docs/brand"),
                    help="directory to write into (default: docs/brand)")
    args = ap.parse_args()

    names = args.shots or sorted(SHOTS)
    out_dir = pathlib.Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    failed = 0
    for name in names:
        shot = SHOTS[name]
        try:
            png, box = shoot(args.browserless, args.app, shot)
        except (urllib.error.URLError, ValueError) as err:
            print(f"{name}: {err}", file=sys.stderr)
            failed += 1
            continue

        dest = out_dir / shot["path"]
        dest.write_bytes(png)
        fits = "fits" if box["scrollHeight"] <= shot["height"] else "TALLER THAN THE FRAME"
        print(
            f"{name}: {shot['url']} at {shot['width']}x{shot['height']} "
            f"-> {dest} ({len(png)} bytes)\n"
            f"  {box['scrollHeight']}px of page, {fits}"
            + (f", last control ends at {box['actionsBottom']}px" if box["actionsBottom"] else "")
        )
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
