package unzip

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// TestLimitedRefusesMoreThanTheLimit: what unpacks to more than the limit
// is refused for its size, not cut at the limit and then refused for
// whatever the cut made of it.
func TestLimitedRefusesMoreThanTheLimit(t *testing.T) {
	packed := gzipped(make([]byte, 100))
	if got, err := Limited(packed, 100); err != nil || len(got) != 100 {
		t.Fatalf("100 bytes under a limit of 100 = %d bytes, %v", len(got), err)
	}
	if got, err := Limited(packed, 99); err == nil || !strings.Contains(err.Error(), "more than 99 bytes") {
		t.Fatalf("100 bytes under a limit of 99 = %d bytes, %v; want refused for its size", len(got), err)
	}
	if _, err := Limited([]byte("not gzip"), 100); err == nil {
		t.Fatal("bytes that are not gzip were unpacked")
	}
}

func TestGzippedReadsTheMagicNumber(t *testing.T) {
	if !Gzipped(gzipped([]byte("x"))) {
		t.Error("gzip is not gzipped")
	}
	for _, b := range [][]byte{nil, {0x1f}, {0x1f, 0x8b}, []byte("plain")} {
		if Gzipped(b) {
			t.Errorf("%q is gzipped", b)
		}
	}
}
