// Package unzip unpacks gzip within a bound, for the vector tiles and the
// PMTiles directories alike: a few kilobytes of gzip can unpack to
// gigabytes.
package unzip

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// maxRatio is the most the trailer's size is believed to exceed the
// packed bytes by: vector tiles and PMTiles directories pack 3 to 6 times.
const maxRatio = 64

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
	// gzip's trailer says how long the data is unpacked (modulo 2³²),
	// which sizes the buffer once instead of doubling it up to the size.
	// It is believed only within the limit and within maxRatio of what is
	// packed, so a few bytes that claim megabytes allocate nothing for it.
	var buf bytes.Buffer
	if n := len(data); n >= 4 {
		if size := int64(binary.LittleEndian.Uint32(data[n-4:])); size <= limit && size <= maxRatio*int64(n) {
			buf.Grow(int(size) + bytes.MinRead)
		}
	}
	if _, err := buf.ReadFrom(io.LimitReader(zr, limit+1)); err != nil {
		return nil, err
	}
	if int64(buf.Len()) > limit {
		return nil, fmt.Errorf("more than %d bytes unpacked", limit)
	}
	return buf.Bytes(), nil
}
