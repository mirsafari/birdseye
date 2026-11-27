#!/usr/bin/env bash
# Redraws the README's picture, docs/birdseye-light.png and -dark.png: two
# fixture places drawn as a page shows them (inline, coloured by the
# default stylesheet, with a marker at PinX, PinY), at twice the pixels.
#
#   scripts/screenshot.sh       (needs Google Chrome; CHROME overrides its path)
set -euo pipefail
cd "$(dirname "$0")/.."

chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

figures=""
for place in "split:43.50805:16.4405:Split, the pinned building in the accent colour" \
             "komiza-harbour:43.0437:16.0886:Komiža, a harbour village"; do
  IFS=: read -r name lat lng caption <<<"$place"
  go run ./cmd/birdseye one -lat "$lat" -lng "$lng" -o "$tmp/$name.svg" >/dev/null
  figures+="<figure><div class=\"birdseye frame\">$(cat "$tmp/$name.svg")<span class=\"pin\"></span></div><figcaption>$caption</figcaption></figure>"
done

cat > "$tmp/index.html" <<HTML
<!doctype html><html><head><meta charset="utf-8"><style>
$(cat birdseye.css)
:root { color-scheme: light dark; }
body { margin: 0; padding: 20px 24px 16px; background: var(--birdseye-paper); color: var(--birdseye-ink);
  font: 13px/1.3 -apple-system, system-ui, sans-serif; display: grid; grid-template-columns: 1fr 1fr; gap: 24px; }
figure { margin: 0; } figcaption { margin-top: 2px; opacity: .65; text-align: center; }
.frame { position: relative; } .frame svg { display: block; width: 100%; height: auto; }
.pin { position: absolute; left: 50%; top: 58%; width: 2px; height: 22px; margin-left: -1px; transform: translateY(-100%);
  background: var(--birdseye-accent); }
.pin::before { content: ""; position: absolute; left: 50%; top: -24px; width: 22px; height: 22px; margin-left: -14px;
  border-radius: 50%; background: var(--birdseye-accent); border: 3px solid var(--birdseye-paper); box-shadow: 0 2px 6px #0005; }
.pin::after { content: ""; position: absolute; left: 50%; bottom: -3px; width: 6px; height: 6px; margin-left: -3px;
  border-radius: 50%; background: var(--birdseye-accent); }
</style></head><body>$figures</body></html>
HTML

for scheme in light:1 dark:0; do
  IFS=: read -r name value <<<"$scheme"
  "$chrome" --headless --disable-gpu --hide-scrollbars --user-data-dir="$tmp/profile" \
    --force-device-scale-factor=2 --window-size=1200,420 --virtual-time-budget=2000 \
    --blink-settings=preferredColorScheme="$value" \
    --screenshot="$PWD/docs/birdseye-$name.png" "file://$tmp/index.html" 2>/dev/null
done
ls -l docs/birdseye-*.png
