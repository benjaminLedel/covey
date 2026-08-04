#!/usr/bin/env python3
"""Baut aus den Einzelbildern von demo/tour die Bilder für das README.

    go run ./demo/tour -url http://localhost:8495 -out /tmp/tour
    python3 demo/tour/build.py /tmp/tour

Erzeugt unter web/public/shots/:
  * <name>.jpg  — die Screenshots, auf die das README verweist
  * tour.gif    — das Demo-GIF, das dieselben Stationen der Reihe nach zeigt

Warum Pillow und nicht ffmpeg: Pillow liegt auf jedem Rechner mit Python, und
ein GIF aus einer Handvoll Bildern mit je eigener Standzeit ist genau das, was
es gut kann. Ein Video wäre kleiner, aber GitHub spielt in einem README nur
GIFs zuverlässig ab.
"""

import json
import sys
from pathlib import Path

from PIL import Image

REPO = Path(__file__).resolve().parents[2]
SHOTS = REPO / "web" / "public" / "shots"

# Breite des GIFs. 1280 wäre schärfer, aber ein README-GIF wird auf einer
# Verbindung geladen, über die niemand nachdenken sollte.
GIF_WIDTH = 900
JPEG_QUALITY = 82


def build(src: Path, suffix: str = "") -> None:
    manifest = json.loads((src / "tour.json").read_text())
    frames = manifest["frames"]
    SHOTS.mkdir(parents=True, exist_ok=True)

    # --- Screenshots fürs README ---
    for f in frames:
        if not f["in_readme"]:
            continue
        img = Image.open(src / f["file"]).convert("RGB")
        target = SHOTS / f"{f['name']}{suffix}.jpg"
        img.save(target, "JPEG", quality=JPEG_QUALITY, optimize=True, progressive=True)
        print(f"  {target.relative_to(REPO)}  {target.stat().st_size // 1024} kB")

    # --- GIF ---
    images, durations = [], []
    for f in frames:
        img = Image.open(src / f["file"]).convert("RGB")
        h = round(img.height * GIF_WIDTH / img.width)
        img = img.resize((GIF_WIDTH, h), Image.LANCZOS)
        # Eine gemeinsame Palette für alle Bilder: quantisiert man jedes für
        # sich, flackern die Farbverläufe der Oberfläche von Bild zu Bild.
        images.append(img)
        durations.append(f["hold_ms"])

    palette_source = images[0].quantize(colors=200, method=Image.MEDIANCUT)
    quantized = [im.quantize(palette=palette_source, dither=Image.FLOYDSTEINBERG) for im in images]

    gif = SHOTS / f"tour{suffix}.gif"
    quantized[0].save(
        gif,
        save_all=True,
        append_images=quantized[1:],
        duration=durations,
        loop=0,
        optimize=True,
    )
    print(f"  {gif.relative_to(REPO)}  {gif.stat().st_size // 1024} kB "
          f"({len(quantized)} Bilder, {sum(durations) / 1000:.1f} s)")


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__)
        return 2
    src = Path(sys.argv[1])
    if not (src / "tour.json").exists():
        print(f"{src}/tour.json fehlt — erst `go run ./demo/tour` laufen lassen")
        return 1
    suffix = sys.argv[2] if len(sys.argv) > 2 else ""
    build(src, suffix)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
