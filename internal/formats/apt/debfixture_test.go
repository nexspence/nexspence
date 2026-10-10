package apt

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// debOpts describes a .deb built in-test: real ar/tar/compression framing, so
// the parser and the upload path see exactly what dpkg-deb produces.
type debOpts struct {
	control     string // control file text
	compression string // "", "gz", "xz", "zst"
	controlName string // tar entry name; default "./control"
	data        string // payload carried in data.tar.gz
	extraBefore bool   // add an optional "_member" between debian-binary and control
}

func buildDeb(t *testing.T, o debOpts) []byte {
	t.Helper()
	name := o.controlName
	if name == "" {
		name = "./control"
	}
	controlTar := tarWith(t, name, o.control)
	member := "control.tar"
	var packed []byte
	switch o.compression {
	case "":
		packed = controlTar
	case "gz":
		member += ".gz"
		packed = gzipped(t, controlTar)
	case "xz":
		// There is no xz encoder among the dependencies, so the xz member is a
		// fixture made by xz(1) from libControl (testdata/README).
		require.Equal(t, libControl, o.control, "xz fixtures exist for libControl only")
		require.Equal(t, "./control", name)
		member += ".xz"
		packed = readFixture(t, "libfoo2-control.tar.xz")
	case "zst":
		member += ".zst"
		var b bytes.Buffer
		w, err := zstd.NewWriter(&b)
		require.NoError(t, err)
		_, err = w.Write(controlTar)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		packed = b.Bytes()
	default:
		t.Fatalf("unknown compression %q", o.compression)
	}

	var ar bytes.Buffer
	ar.WriteString(arMagic)
	arMember(&ar, "debian-binary", []byte("2.0\n"))
	if o.extraBefore {
		arMember(&ar, "_gpgorigin", []byte("signature"))
	}
	arMember(&ar, member, packed)
	arMember(&ar, "data.tar.gz", gzipped(t, tarWith(t, "./usr/share/doc/payload", o.data)))
	return ar.Bytes()
}

func tarWith(t *testing.T, name, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return b.Bytes()
}

func gzipped(t *testing.T, in []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, err := w.Write(in)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return b.Bytes()
}

func arMember(b *bytes.Buffer, name string, body []byte) {
	fmt.Fprintf(b, "%-16s%-12s%-6s%-6s%-8s%-10d`\n", name, "0", "0", "0", "100644", len(body))
	b.Write(body)
	if len(body)%2 == 1 {
		b.WriteByte('\n')
	}
}

type tarEntry struct{ name, body, link string }

func tarWithEntries(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e.body))}
		if e.link != "" {
			h = &tar.Header{Name: e.name, Typeflag: tar.TypeSymlink, Linkname: e.link, Mode: 0o777}
		}
		require.NoError(t, tw.WriteHeader(h))
		if e.link == "" {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return b.Bytes()
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return b
}

// xzVarint decodes an xz multibyte integer.
func xzVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 9; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

// setXzBlockDictProps rewrites block n's LZMA2 dictionary property (40 means
// 4 GiB) and fixes that block header's CRC. Blocks after the first are found
// through the compressed size the multi-threaded encoder records in each
// header, as a parallel decoder finds them.
func setXzBlockDictProps(t *testing.T, stream []byte, n int, props byte) []byte {
	t.Helper()
	out := append([]byte{}, stream...)
	checkSize := map[byte]int{0x00: 0, 0x01: 4, 0x04: 8, 0x0a: 32}[out[7]&0x0f]
	off := 12
	for blk := 0; ; blk++ {
		size := (int(out[off]) + 1) * 4
		hdr := out[off : off+size]
		flags, i := hdr[1], 2
		var compressed uint64
		if flags&0x40 != 0 {
			v, k := xzVarint(hdr[i:])
			compressed, i = v, i+k
		}
		if flags&0x80 != 0 {
			_, k := xzVarint(hdr[i:])
			i += k
		}
		if blk == n {
			_, k := xzVarint(hdr[i:]) // filter id
			i += k
			_, k = xzVarint(hdr[i:]) // properties length
			i += k
			hdr[i] = props
			binary.LittleEndian.PutUint32(hdr[size-4:], crc32.ChecksumIEEE(hdr[:size-4]))
			return out
		}
		require.NotZero(t, compressed, "block %d records no compressed size", blk)
		off += size + int((compressed+3)/4*4) + checkSize
	}
}
