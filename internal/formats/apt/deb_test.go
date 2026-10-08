package apt

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const libControl = `Package: libfoo2
Version: 2.3.1-1
Architecture: amd64
Maintainer: Foo Developers <foo@example.org>
Installed-Size: 2048
Depends: libfoo-common (= 2.3.1-1), libc6 (>= 2.35)
Provides: libfoo-abi-2 (= 2.3.1-1)
Conflicts: libfoo1
Description: foo runtime library
 Shared library for foo.
 .
 Second paragraph of the long description.
`

// Every codec dpkg-deb writes, and both tar entry spellings.
func TestReadDebHead_ParsesControlFromEveryCodec(t *testing.T) {
	for _, tc := range []struct{ compression, name string }{
		{"", "./control"}, {"gz", "./control"}, {"xz", "./control"}, {"zst", "./control"}, {"gz", "control"},
	} {
		t.Run(tc.compression+tc.name, func(t *testing.T) {
			deb := buildDeb(t, debOpts{control: libControl, compression: tc.compression, controlName: tc.name, data: "payload"})
			head, ctl, err := readDebHead(bytes.NewReader(deb))
			require.NoError(t, err)
			assert.Equal(t, "libfoo2", ctl.Package)
			assert.Equal(t, "2.3.1-1", ctl.Version)
			assert.Equal(t, "amd64", ctl.Architecture)
			assert.Contains(t, ctl.Paragraph, "Depends: libfoo-common (= 2.3.1-1), libc6 (>= 2.35)")
			assert.Contains(t, ctl.Paragraph, "Provides: libfoo-abi-2 (= 2.3.1-1)")
			assert.Contains(t, ctl.Paragraph, " Shared library for foo.\n .\n Second paragraph")
			assert.False(t, strings.HasSuffix(ctl.Paragraph, "\n"), "stored without trailing newline")
			assert.True(t, bytes.HasPrefix(deb, head), "head is a prefix of the body")
			assert.Less(t, len(head), len(deb), "data.tar is never read")
		})
	}
}

// head + the unread rest must reproduce the exact upload, byte for byte.
func TestReadDebHead_HeadPlusRestIsTheWholeBody(t *testing.T) {
	deb := buildDeb(t, debOpts{control: libControl, compression: "xz", data: strings.Repeat("x", 1<<16)})
	r := bytes.NewReader(deb)
	head, _, err := readDebHead(r)
	require.NoError(t, err)
	all, err := io.ReadAll(io.MultiReader(bytes.NewReader(head), r))
	require.NoError(t, err)
	assert.Equal(t, deb, all)
}

func TestReadDebHead_OptionalMemberBeforeControl(t *testing.T) {
	deb := buildDeb(t, debOpts{control: libControl, compression: "gz", extraBefore: true})
	_, ctl, err := readDebHead(bytes.NewReader(deb))
	require.NoError(t, err)
	assert.Equal(t, "libfoo2", ctl.Package)
}

func TestReadDebHead_NotAnArArchive(t *testing.T) {
	for _, body := range []string{"", "a", "deb-bytes", "PK\x03\x04 zip archive"} {
		head, ctl, err := readDebHead(strings.NewReader(body))
		assert.ErrorIs(t, err, errNotDeb, "%q", body)
		assert.Nil(t, ctl)
		all, _ := io.ReadAll(io.MultiReader(bytes.NewReader(head), strings.NewReader(body[len(head):])))
		assert.Equal(t, body, string(all), "head replays exactly what was consumed")
	}
}

func TestReadDebHead_ServerFieldsAreDropped(t *testing.T) {
	ctl := libControl + "Filename: pool/x.deb\nSize: 1\nMD5sum: 00\nSHA1: 00\nSHA256: 00\nSHA512: 00\n"
	_, c, err := readDebHead(bytes.NewReader(buildDeb(t, debOpts{control: ctl, compression: "gz"})))
	require.NoError(t, err)
	for _, f := range []string{"Filename:", "Size:", "MD5sum:", "SHA1:", "SHA256:", "SHA512:"} {
		assert.NotContains(t, c.Paragraph, "\n"+f, f)
	}
	assert.Contains(t, c.Paragraph, "Installed-Size: 2048", "Installed-Size is not Size")
}

func TestReadDebHead_RejectsBrokenControl(t *testing.T) {
	cases := map[string]string{
		"missing Version":      "Package: a1\nArchitecture: amd64\n",
		"missing Architecture": "Package: a1\nVersion: 1.0\n",
		"bad package name":     "Package: Bad_Name\nVersion: 1.0\nArchitecture: amd64\n",
		"bad version":          "Package: a1\nVersion: 1 0\nArchitecture: amd64\n",
		"bad architecture":     "Package: a1\nVersion: 1.0\nArchitecture: amd/64\n",
		"two paragraphs":       "Package: a1\nVersion: 1.0\nArchitecture: amd64\n\nPackage: b1\n",
		"duplicate field":      "Package: a1\nVersion: 1.0\nVersion: 2.0\nArchitecture: amd64\n",
		"leading continuation": " Package: a1\nVersion: 1.0\nArchitecture: amd64\n",
		"malformed line":       "Package: a1\nVersion 1.0\nArchitecture: amd64\n",
		"empty control":        "\n\n",
	}
	for name, ctl := range cases {
		t.Run(name, func(t *testing.T) {
			_, c, err := readDebHead(bytes.NewReader(buildDeb(t, debOpts{control: ctl, compression: "gz"})))
			require.Error(t, err)
			assert.Nil(t, c)
			assert.False(t, errors.Is(err, errNotDeb), "a real ar archive is never 'not a deb'")
		})
	}
}

func TestReadDebHead_ArchiveFraming(t *testing.T) {
	good := buildDeb(t, debOpts{control: libControl, compression: "gz"})

	_, _, err := readDebHead(bytes.NewReader(good[:len(arMagic)+30]))
	assert.ErrorContains(t, err, "truncated", "cut inside the first header")

	var noControl bytes.Buffer
	noControl.WriteString(arMagic)
	arMember(&noControl, "debian-binary", []byte("2.0\n"))
	_, _, err = readDebHead(&noControl)
	assert.ErrorContains(t, err, "no control member")

	var wrongOrder bytes.Buffer
	wrongOrder.WriteString(arMagic)
	arMember(&wrongOrder, "control.tar", tarWith(t, "./control", libControl))
	_, _, err = readDebHead(&wrongOrder)
	assert.ErrorContains(t, err, "before debian-binary")

	var badVersion bytes.Buffer
	badVersion.WriteString(arMagic)
	arMember(&badVersion, "debian-binary", []byte("3.0\n"))
	_, _, err = readDebHead(&badVersion)
	assert.ErrorContains(t, err, "unsupported format version")

	var dataFirst bytes.Buffer
	dataFirst.WriteString(arMagic)
	arMember(&dataFirst, "debian-binary", []byte("2.0\n"))
	arMember(&dataFirst, "data.tar.gz", []byte("x"))
	_, _, err = readDebHead(&dataFirst)
	assert.ErrorContains(t, err, "unexpected member")

	var badCodec bytes.Buffer
	badCodec.WriteString(arMagic)
	arMember(&badCodec, "debian-binary", []byte("2.0\n"))
	arMember(&badCodec, "control.tar.bz2", []byte("x"))
	_, _, err = readDebHead(&badCodec)
	assert.ErrorContains(t, err, "unsupported control member")

	var corrupt bytes.Buffer
	corrupt.WriteString(arMagic)
	arMember(&corrupt, "debian-binary", []byte("2.0\n"))
	arMember(&corrupt, "control.tar.gz", []byte("not gzip"))
	_, _, err = readDebHead(&corrupt)
	assert.ErrorContains(t, err, "control.tar.gz")

	var noFile bytes.Buffer
	noFile.WriteString(arMagic)
	arMember(&noFile, "debian-binary", []byte("2.0\n"))
	arMember(&noFile, "control.tar", tarWith(t, "./md5sums", "x"))
	_, _, err = readDebHead(&noFile)
	assert.ErrorContains(t, err, "no control file")
}

// The control member is read into memory, so it is capped: a huge one is
// refused before any of data.tar is touched.
func TestReadDebHead_OversizedControlMember(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(arMagic)
	arMember(&b, "debian-binary", []byte("2.0\n"))
	// Declares a member larger than the cap; the body need not be there.
	b.WriteString("control.tar.gz  0           0     0     100644  99999999  `\n")
	_, _, err := readDebHead(&b)
	assert.ErrorIs(t, err, errDebTooLarge)

	huge := libControl + "X-Pad: " + strings.Repeat("y", maxControlFile) + "\n"
	_, _, err = readDebHead(bytes.NewReader(buildDeb(t, debOpts{control: huge, compression: "gz"})))
	assert.ErrorIs(t, err, errDebTooLarge)
}

func TestParseControl_EpochAndCRLF(t *testing.T) {
	c, err := parseControl([]byte("Package: a1\r\nVersion: 2:1.0-1ubuntu1\r\nArchitecture: all\r\n"))
	require.NoError(t, err)
	assert.Equal(t, "2:1.0-1ubuntu1", c.Version)
	assert.Equal(t, "all", c.Architecture)
	assert.NotContains(t, c.Paragraph, "\r")
}

// dpkg unpacks the whole control member, so a second "control" entry would win
// there while the index described the first: refuse it. A symlinked control is
// not a control file either.
func TestReadDebHead_ControlMemberMustHoldOneRegularControl(t *testing.T) {
	two := tarWithEntries(t, []tarEntry{{name: "./control", body: libControl}, {name: "./control", body: strings.Replace(libControl, "libfoo2", "libbar9", 1)}})
	var b bytes.Buffer
	b.WriteString(arMagic)
	arMember(&b, "debian-binary", []byte("2.0\n"))
	arMember(&b, "control.tar", two)
	_, _, err := readDebHead(&b)
	assert.ErrorContains(t, err, "exactly one regular control file")

	link := tarWithEntries(t, []tarEntry{{name: "./control", link: "/etc/passwd"}})
	b.Reset()
	b.WriteString(arMagic)
	arMember(&b, "debian-binary", []byte("2.0\n"))
	arMember(&b, "control.tar", link)
	_, _, err = readDebHead(&b)
	assert.ErrorContains(t, err, "exactly one regular control file")
}

// The paragraph goes into Packages verbatim; nothing a consumer could take
// for a line break may ride along.
func TestParseControl_RejectsControlCharacters(t *testing.T) {
	for name, ctl := range map[string]string{
		"lone CR":   "Package: a1\nVersion: 1.0\nArchitecture: amd64\nDescription: x\rPackage: evil\n",
		"NUL":       "Package: a1\nVersion: 1.0\x00\nArchitecture: amd64\n",
		"form feed": "Package: a1\nVersion: 1.0\nArchitecture: amd64\nDescription: x\fy\n",
		"bad UTF-8": "Package: a1\nVersion: 1.0\nArchitecture: amd64\nDescription: \xff\n",
	} {
		_, err := parseControl([]byte(ctl))
		assert.Error(t, err, name)
	}
	c, err := parseControl([]byte("Package: a1\nVersion: 1.0\nArchitecture: amd64\nDescription: tab\there, ünïcode\n"))
	require.NoError(t, err, "tabs and UTF-8 text are fine")
	assert.Contains(t, c.Paragraph, "ünïcode")
}

// A block declares its dictionary before any of it is decoded, and a few
// hundred bytes can ask for 4 GiB. Every block is held to the cap, the first
// and any later one, while real multi-block members from multi-threaded
// encoders (dpkg-deb -z0/-z1) still decode.
func TestReadDebHead_XzDictionaryIsBoundedPerBlock(t *testing.T) {
	single := readFixture(t, "libfoo2-control.tar.xz")
	multi := readFixture(t, "libfoo2-control-multiblock.tar.xz")
	for name, member := range map[string][]byte{"single block": single, "multi block": multi} {
		ctl, err := controlFromMember("control.tar.xz", member)
		require.NoError(t, err, name)
		assert.Equal(t, "libfoo2", ctl.Package, name)
	}

	for name, member := range map[string][]byte{
		"first block": setXzBlockDictProps(t, single, 0, 40),
		"later block": setXzBlockDictProps(t, multi, 2, 40),
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := controlFromMember("control.tar.xz", member)
		runtime.ReadMemStats(&after)
		assert.ErrorIs(t, err, errDebTooLarge, name)
		assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(128<<20), "%s: refused before the dictionary was allocated", name)
	}

	_, err := controlFromMember("control.tar.xz", []byte("not xz at all"))
	assert.ErrorContains(t, err, "control.tar.xz")
}

// The decompressed control tar is capped. Reaching the cap is an error, not a
// quiet end of the archive that would hide the entries after it.
func TestReadDebHead_ControlTarCapIsNotACleanEnd(t *testing.T) {
	filler := maxControlTar - 2*512 - 512 - 1024 // control header+body, filler header, end blocks
	tarball := tarWithEntries(t, []tarEntry{{name: "./control", body: libControl}, {name: "./md5sums", body: strings.Repeat("x", filler)}, {name: "./control", body: "Package: other\n"}})
	_, err := controlFileFromTar(bytes.NewReader(tarball))
	require.Error(t, err)
	assert.True(t, errors.Is(err, errDebTooLarge) || strings.Contains(err.Error(), "exactly one"), "%v", err)

	// A tar whose entries run past the cap is too large, whatever it holds.
	over := tarWithEntries(t, []tarEntry{{name: "./control", body: libControl}, {name: "./md5sums", body: strings.Repeat("x", maxControlTar)}})
	_, err = controlFileFromTar(bytes.NewReader(over))
	assert.ErrorIs(t, err, errDebTooLarge)
}

// Big members are read as their bytes arrive, not reserved by the header.
func TestReadArMember_DoesNotReserveTheDeclaredSize(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := readArMember(strings.NewReader("short"), 30<<20)
	runtime.ReadMemStats(&after)
	assert.ErrorContains(t, err, "truncated")
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20))
}
