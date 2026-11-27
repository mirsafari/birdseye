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
# Planetiler 0.10.3-SNAPSHOT (e59e1e3, built 2026-11-24), pinned by digest:
# the build the fixtures came from. 0.10.2, the last release, has no
# --download=streaming, which reads the world's water polygons for the area
# alone instead of downloading all 900 MB of them.
image="${PLANETILER_IMAGE:-ghcr.io/onthegomap/planetiler@sha256:bde2424546edfa40460bdd1c40c19d54e3af332b25d16c6db9d980ca3355623d}"
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
