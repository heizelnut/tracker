#!/usr/bin/env python3
"""Generate the tracker's favicon/PWA icons.

The design is a rounded blue gradient tile with a rising trend line — the same
shape as favicon.svg. Run from the repo root:

    python3 tools/gen_icons.py

Requires Pillow (pip install pillow).
"""
from pathlib import Path

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent
C0 = (37, 99, 235)     # #2563eb
C1 = (96, 165, 250)    # #60a5fa
WHITE = (255, 255, 255, 255)

# Points of the trend line, in a 512x512 viewBox.
LINE = [(120, 336), (208, 240), (288, 296), (392, 168)]
DOT = (392, 168)
DOT_R = 32
LINE_W = 36


def gradient(size: int) -> Image.Image:
    grad = Image.new("RGB", (size, size))
    px = grad.load()
    denom = 2 * (size - 1) if size > 1 else 1
    for y in range(size):
        base = C0[0], C0[1], C0[2]
        for x in range(size):
            t = (x + y) / denom
            px[x, y] = (
                round(base[0] + (C1[0] - base[0]) * t),
                round(base[1] + (C1[1] - base[1]) * t),
                round(base[2] + (C1[2] - base[2]) * t),
            )
    return grad


def render(size: int, rounded: bool) -> Image.Image:
    img = gradient(size).convert("RGBA")
    if rounded:
        mask = Image.new("L", (size, size), 0)
        ImageDraw.Draw(mask).rounded_rectangle(
            [0, 0, size - 1, size - 1], radius=round(112 * size / 512), fill=255
        )
        img.putalpha(mask)

    draw = ImageDraw.Draw(img)
    scale = size / 512
    pts = [(x * scale, y * scale) for x, y in LINE]
    width = max(1, round(LINE_W * scale))
    draw.line(pts, fill=WHITE, width=width, joint="curve")
    for x, y in (pts[0],):
        r = width / 2
        draw.ellipse([x - r, y - r, x + r, y + r], fill=WHITE)
    cx, cy = DOT[0] * scale, DOT[1] * scale
    r = DOT_R * scale
    draw.ellipse([cx - r, cy - r, cx + r, cy + r], fill=WHITE)
    return img


def main() -> None:
    icons = ROOT / "icons"
    icons.mkdir(exist_ok=True)

    render(192, rounded=True).save(icons / "icon-192.png")
    render(512, rounded=True).save(icons / "icon-512.png")
    # Maskable: full-bleed, the OS applies its own mask.
    render(512, rounded=False).save(icons / "icon-maskable-512.png")
    # iOS applies its own rounding too.
    render(180, rounded=False).save(icons / "apple-touch-icon.png")

    render(256, rounded=True).save(
        ROOT / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)]
    )
    print("wrote icons/ and favicon.ico")


if __name__ == "__main__":
    main()
