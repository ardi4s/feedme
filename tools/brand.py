#!/usr/bin/env python3
"""Regenerate the brand images the README and the favicon routes use.

The source of truth is the mark the server already embeds,
``internal/web/assets/feedme.png``. Nothing here is drawn from scratch: the mark
is placed as-is, so the card cannot drift away from the logo a visitor sees in
the corner of the app.

    python3 tools/brand.py

Pillow is the only dependency. The script prints what it decided, including
whether it picked the light or the dark card, so the choice can be checked
without opening the images.
"""

from __future__ import annotations

import pathlib
import sys

from PIL import Image, ImageDraw, ImageFont

ROOT = pathlib.Path(__file__).resolve().parent.parent
MARK = ROOT / "internal/web/assets/feedme.png"
SOCIAL = ROOT / "docs/brand/social-preview.png"
FAVICON_PNG = ROOT / "internal/web/assets/favicon.png"
FAVICON_ICO = ROOT / "internal/web/assets/favicon.ico"

# The Ubuntu fonts ship with the CI image and most Debian-based hosts. When they
# are missing the script falls back to Pillow's bundled font rather than
# failing, so it still produces something on a bare machine.
FONT_DIR = pathlib.Path("/usr/share/fonts/truetype/ubuntu")

CARD_W, CARD_H = 1280, 640
FAVICON_SIZES = [16, 32, 48, 64]


def font(name: str, size: int) -> ImageFont.FreeTypeFont:
    path = FONT_DIR / name
    if path.exists():
        return ImageFont.truetype(str(path), size)
    print(f"  ! {path} missing, falling back to Pillow's default font", file=sys.stderr)
    return ImageFont.load_default(size=size)


def mark() -> Image.Image:
    """The logo, cropped to the pixels it actually draws.

    The source file carries a transparent margin, and layout maths that has to
    guess at it drifts the moment the artwork is re-exported. Cropping to the
    alpha bounding box makes the placement depend only on the visible mark.
    """
    img = Image.open(MARK).convert("RGBA")
    box = img.getchannel("A").getbbox()
    return img.crop(box) if box else img


def mean_luminance(img: Image.Image) -> float:
    """Perceived brightness of the drawn pixels, 0 (black) to 255 (white)."""
    total = 0.0
    count = 0
    for r, g, b, a in img.getdata():
        if a > 32:
            total += 0.299 * r + 0.587 * g + 0.114 * b
            count += 1
    return total / count if count else 0.0


def theme(img: Image.Image) -> dict[str, tuple[int, int, int]]:
    """Pick a card palette that the mark is actually visible against.

    A light mark on a white card is an invisible logo, and the artwork cannot be
    inspected by whoever runs the build, so the background is chosen from the
    mark's own brightness instead of being fixed.
    """
    if mean_luminance(img) > 160:
        return {
            "bg": (16, 16, 16),
            "fg": (245, 245, 245),
            "muted": (156, 156, 156),
            "line": (48, 48, 48),
        }
    return {
        "bg": (255, 255, 255),
        "fg": (17, 17, 17),
        "muted": (110, 110, 110),
        "line": (226, 226, 226),
    }


def social_preview(src: Image.Image) -> Image.Image:
    colours = theme(src)
    card = Image.new("RGBA", (CARD_W, CARD_H), colours["bg"] + (255,))
    draw = ImageDraw.Draw(card)

    pad = 88
    mark_h = 120
    mark_w = round(src.width * mark_h / src.height)
    card.alpha_composite(src.resize((mark_w, mark_h), Image.Resampling.LANCZOS), (pad, pad))

    # The wordmark and the tagline are stacked against the mark's centre line,
    # which keeps the lock-up square whatever the mark's aspect ratio is.
    centre = pad + mark_h // 2
    x = pad + mark_w + 40
    draw.text((x, centre - 28), "feedme", font=font("Ubuntu-B.ttf", 116), fill=colours["fg"], anchor="lm")
    draw.text((x, centre + 60), "RSS feed creator", font=font("Ubuntu-R.ttf", 46), fill=colours["muted"], anchor="lm")

    draw.text((pad, 406), "Turn any web page into a feed.", font=font("Ubuntu-R.ttf", 38), fill=colours["fg"])
    draw.text(
        (pad, 472),
        "RSS 2.0   \u00b7   Atom 1.0   \u00b7   JSON Feed 1.1",
        font=font("Ubuntu-R.ttf", 30),
        fill=colours["muted"],
    )

    # A hairline along the bottom so the card has a defined edge on a white
    # page, where a pure-white image would otherwise float.
    draw.line([(0, CARD_H - 6), (CARD_W, CARD_H - 6)], fill=colours["line"], width=3)
    return card


def square(src: Image.Image, margin: float = 0.06) -> Image.Image:
    """Centre the mark on a transparent square canvas with a small margin."""
    side = round(max(src.width, src.height) * (1 + 2 * margin))
    canvas = Image.new("RGBA", (side, side), (0, 0, 0, 0))
    canvas.alpha_composite(src, ((side - src.width) // 2, (side - src.height) // 2))
    return canvas


def coverage(img: Image.Image) -> float:
    """Share of pixels the mark covers, used to notice a blank render."""
    drawn = sum(1 for _, _, _, a in img.getdata() if a > 32)
    return drawn / (img.width * img.height)


def main() -> int:
    if not MARK.exists():
        print(f"missing brand mark: {MARK}", file=sys.stderr)
        return 1

    src = mark()
    if not src.width or not src.height:
        print("the brand mark is fully transparent", file=sys.stderr)
        return 1

    luminance = mean_luminance(src)
    print(f"mark:   {MARK.relative_to(ROOT)}  {Image.open(MARK).size[0]}x{Image.open(MARK).size[1]}"
          f" -> trimmed {src.width}x{src.height}, mean luminance {luminance:.0f}"
          f" ({'dark card' if luminance > 160 else 'light card'})")

    SOCIAL.parent.mkdir(parents=True, exist_ok=True)
    card = social_preview(src)
    card.convert("RGB").save(SOCIAL, optimize=True)
    print(f"social: {SOCIAL.relative_to(ROOT)}  {card.width}x{card.height}")

    icon = square(src)
    view = icon.resize((64, 64), Image.Resampling.LANCZOS)
    view.save(FAVICON_PNG)
    print(f"icon:   {FAVICON_PNG.relative_to(ROOT)}  64x64, mark covers {coverage(view):.0%}")

    icon.resize((max(FAVICON_SIZES), max(FAVICON_SIZES)), Image.Resampling.LANCZOS).save(
        FAVICON_ICO, format="ICO", sizes=[(s, s) for s in FAVICON_SIZES]
    )
    print(f"icon:   {FAVICON_ICO.relative_to(ROOT)}  sizes {FAVICON_SIZES}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
