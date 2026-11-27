// Package unzip unpacks gzip within a bound, for the vector tiles and the
// PMTiles directories alike: a few kilobytes of gzip can unpack to
// gigabytes.
package unzip

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// Gzipped reports whether data starts with gzip's magic number.
func Gzipped(data []byte) bool {
	return len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b
}

// Limited unpacks gzipped data and refuses more than limit bytes of it, so
// a damaged or hostile input fails whole instead of unpacking the part of
// it that fits.
func Limited(data []byte, limit int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(zr, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, fmt.Errorf("more than %d bytes unpacked", limit)
	}
	return out, nil
}
