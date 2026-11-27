# birdseye fixtures

`tiles/` holds zoom-14 vector tiles (gzipped Mapbox Vector Tiles,
`<z>-<x>-<y>.pbf`, XYZ numbering) around the pins in `places.json`: every
tile within 540 m of each pin, more than the widest disc and its margin
(460 m, `birdseye.TilesFor`) need.

- **Source**: OpenStreetMap data from Geofabrik's Croatia extract of
  2026-11-25 (replication time 2026-11-25T21:22:46Z), built into tiles with
  Planetiler 0.10.3-SNAPSHOT and its OpenMapTiles profile (schema 3.16.0) on
  2026-11-26, the flags `scripts/build-tiles.sh` uses. A PMTiles archive
  built from the same extract on 2026-11-27 holds the same tiles
  (`BIRDSEYE_PMTILES_EXACT=1 go test ./pmtiles`).
- **Licence**: the data is © OpenStreetMap contributors under the ODbL
  (openstreetmap.org/copyright); the tiles are OpenMapTiles-schema produced
  work. Anything drawn from them shows the credit
  **© OpenMapTiles © OpenStreetMap contributors**.

Refresh them with `go run ./cmd/birdseye cut -tiles <archive>`; the render
tests pin counts per place, which then move.
