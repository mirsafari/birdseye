#!/usr/bin/env bash
# Builds the tile archive a release carries: zoom-14 vector tiles of one
# Geofabrik extract (default: Croatia) in the OpenMapTiles schema, with only
# the layers birdseye reads, as a PMTiles archive, plus its SHA-256.
#
#   scripts/build-tiles.sh [area] [out-dir]     (needs Docker)
#
# About two minutes and 250 MB of downloads for Croatia (cached in
# <out-dir>/sources between runs); the archive is about 90 MB.
set -euo pipefail

area="${1:-croatia}"
out="${2:-dist}"
image="${PLANETILER_IMAGE:-ghcr.io/onthegomap/planetiler:0.10.2}"
name="${area}-z14.pmtiles"

mkdir -p "$out"
docker run --rm -e JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:--Xmx1300m}" \
  -v "$(cd "$out" && pwd)":/data "$image" openmaptiles \
  --area="$area" --download=streaming --minzoom=14 --maxzoom=14 \
  --only-layers=building,water,waterway,park,landcover,landuse,transportation \
  --building-hide-outlines-with-parts \
  --output="/data/$name"

(cd "$out" && shasum -a 256 "$name" > "$name.sha256")
cat "$out/$name.sha256"
