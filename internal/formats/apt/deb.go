package apt

// Reading the control file out of an uploaded .deb (#637).
//
// A .deb is an ar(1) archive: "debian-binary", then "control.tar[.gz|.xz|.zst]",
// then "data.tar.*". Only the first two members are read, and only into memory
// up to fixed caps, so a large package still streams to the blob store: the
// caller replays the bytes read here in front of the rest of the body.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"github.com/xi2/xz"
)

const (
	arMagic        = "!<arch>\n"
	arHeaderLen    = 60
	maxDebHead     = 32 << 20 // ar headers + debian-binary + control member, read into memory
	maxControlTar  = 16 << 20 // decompressed control.tar
	maxControlFile = 1 << 20  // the control file itself
	maxControlZstd = 64 << 20 // zstd decoder window bound
	maxControlXz   = 64 << 20 // xz (LZMA2) dictionary bound, enforced per block by the decoder
)

var (
	// errNotDeb: the body is not an ar archive at all. Callers keep the
	// filename-derived coordinates for it, as before #637.
	errNotDeb = errors.New("not a Debian package")
	// errDebTooLarge: the control member does not fit the in-memory caps.
	errDebTooLarge = errors.New("the package's control member exceeds the size limit")

	// Debian policy 5.6.1 (Package), 5.6.12 (Version) and dpkg's architecture
	// syntax. Architecture also has to pass releaseName so a group can list it.
	debPackageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	debVersion     = regexp.MustCompile(`^([0-9]+:)?[A-Za-z0-9][A-Za-z0-9.+~:-]*$`)
	debArchName    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	debFieldName   = regexp.MustCompile(`^[!-9;-~][!-9;-~]*$`)
)

// serverFields are recomputed from the stored asset for every index, never
// copied from the control file (dpkg-scanpackages does the same).
var serverFields = map[string]bool{
	"filename": true, "size": true, "md5sum": true,
	"sha1": true, "sha256": true, "sha512": true,
}

// debControl is a validated control paragraph.
type debControl struct {
	Package      string
	Version      string
	Architecture string
	// Paragraph is the control file as written, minus server-computed fields,
	// without a trailing newline. It is emitted verbatim into Packages.
	Paragraph string
}

// readDebHead reads r through the end of the control member and parses the
// control file. It returns every byte it consumed, so the caller can store
// io.MultiReader(bytes.NewReader(head), r) as the complete body.
//
// errNotDeb means the body is not an ar archive; head still holds what was read.
func readDebHead(r io.Reader) (head []byte, ctl *debControl, err error) {
	var buf bytes.Buffer
	in := &capReader{r: io.TeeReader(r, &buf), left: maxDebHead}

	magic := make([]byte, len(arMagic))
	if _, err := io.ReadFull(in, magic); err != nil || string(magic) != arMagic {
		return buf.Bytes(), nil, errNotDeb
	}

	sawBinary := false
	for {
		name, size, err := readArHeader(in)
		if err != nil {
			return buf.Bytes(), nil, err
		}
		switch {
		case name == "debian-binary":
			if err := checkDebianBinary(in, size); err != nil {
				return buf.Bytes(), nil, err
			}
			sawBinary = true
		case strings.HasPrefix(name, "control.tar"):
			if !sawBinary {
				return buf.Bytes(), nil, fmt.Errorf("invalid .deb: control member before debian-binary")
			}
			member, err := readArMember(in, size)
			if err != nil {
				return buf.Bytes(), nil, err
			}
			ctl, err := controlFromMember(name, member)
			return buf.Bytes(), ctl, err
		case strings.HasPrefix(name, "_"):
			// Optional members (signatures and the like) may precede control.
			if _, err := readArMember(in, size); err != nil {
				return buf.Bytes(), nil, err
			}
		default:
			return buf.Bytes(), nil, fmt.Errorf("invalid .deb: unexpected member %q before control", name)
		}
	}
}

// capReader fails with errDebTooLarge once more than left bytes are read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errDebTooLarge
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// readArHeader reads one 60-byte ar member header.
func readArHeader(r io.Reader) (name string, size int64, err error) {
	hdr := make([]byte, arHeaderLen)
	if _, err := io.ReadFull(r, hdr); err != nil {
		if errors.Is(err, errDebTooLarge) {
			return "", 0, err
		}
		return "", 0, fmt.Errorf("invalid .deb: truncated archive (no control member)")
	}
	if string(hdr[58:60]) != "`\n" {
		return "", 0, fmt.Errorf("invalid .deb: bad ar member header")
	}
	name = strings.TrimRight(strings.TrimSpace(string(hdr[0:16])), "/")
	size, err = strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("invalid .deb: bad ar member size")
	}
	return name, size, nil
}

// readArMember reads a member body of size bytes plus its even-alignment pad.
func readArMember(r io.Reader, size int64) ([]byte, error) {
	if size > maxDebHead {
		return nil, errDebTooLarge
	}
	// Grow with the bytes that actually arrive: a header may claim a size the
	// request never sends, and must not reserve it up front.
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, r, size); err != nil {
		if errors.Is(err, errDebTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("invalid .deb: truncated member")
	}
	body := buf.Bytes()
	if size%2 == 1 {
		if _, err := io.ReadFull(r, make([]byte, 1)); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("invalid .deb: truncated member padding")
		}
	}
	return body, nil
}

// checkDebianBinary accepts format version 2.x, the only one dpkg writes.
func checkDebianBinary(r io.Reader, size int64) error {
	body, err := readArMember(r, size)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(body), "2.") {
		return fmt.Errorf("invalid .deb: unsupported format version %q", strings.TrimSpace(string(body)))
	}
	return nil
}

// controlFromMember decompresses control.tar[.ext] and parses its control file.
func controlFromMember(name string, member []byte) (*debControl, error) {
	tr, closeFn, err := controlTarReader(name, member)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	raw, err := controlFileFromTar(tr)
	if err != nil {
		return nil, err
	}
	return parseControl(raw)
}

func controlTarReader(name string, member []byte) (io.Reader, func(), error) {
	src := bytes.NewReader(member)
	noop := func() {}
	switch strings.TrimPrefix(name, "control.tar") {
	case "":
		return src, noop, nil
	case ".gz":
		zr, err := gzip.NewReader(src)
		if err != nil {
			return nil, noop, fmt.Errorf("invalid .deb: control.tar.gz: %w", err)
		}
		return zr, func() { _ = zr.Close() }, nil
	case ".xz":
		// The dictionary a block declares is allocated before any of it is
		// decoded, and can be 4 GiB from a few hundred bytes. This decoder
		// checks every block's dictionary against the cap first and fails with
		// ErrMemlimit instead. Nothing valid needs more: the decompressed tar
		// is capped well below it, so no back-reference can reach further.
		xr, err := xz.NewReader(src, maxControlXz)
		if errors.Is(err, xz.ErrMemlimit) {
			return nil, noop, errDebTooLarge // the first block is read by NewReader
		}
		if err != nil {
			return nil, noop, fmt.Errorf("invalid .deb: control.tar.xz: %w", err)
		}
		xr.Multistream(false)
		return xr, noop, nil
	case ".zst":
		zr, err := zstd.NewReader(src, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxControlZstd))
		if err != nil {
			return nil, noop, fmt.Errorf("invalid .deb: control.tar.zst: %w", err)
		}
		return zr, zr.Close, nil
	default:
		return nil, noop, fmt.Errorf("invalid .deb: unsupported control member %q", name)
	}
}

// controlFileFromTar returns the control file: the one entry named "control"
// or "./control". dpkg unpacks the whole member, so a later entry of the same
// name would win there; a second one, or one that is not a regular file, is
// refused rather than letting the index describe a different package than the
// one dpkg installs.
func controlFileFromTar(r io.Reader) ([]byte, error) {
	// One byte past the cap is read so that a tar running exactly up to it is
	// told apart from one cut off there: a clean EOF at the boundary would
	// otherwise end the scan and hide whatever entries follow.
	in := &countingReader{r: io.LimitReader(r, maxControlTar+1)}
	tr := tar.NewReader(in)
	var raw []byte
	for {
		h, err := tr.Next()
		if in.n > maxControlTar {
			return nil, errDebTooLarge
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, xz.ErrMemlimit) {
			return nil, errDebTooLarge
		}
		if err != nil {
			return nil, fmt.Errorf("invalid .deb: control member: %w", err)
		}
		if path.Clean(strings.TrimPrefix(h.Name, "./")) != "control" {
			continue
		}
		if h.Typeflag != tar.TypeReg || raw != nil {
			return nil, fmt.Errorf("invalid .deb: control member must hold exactly one regular control file")
		}
		raw, err = io.ReadAll(io.LimitReader(tr, maxControlFile+1))
		if errors.Is(err, xz.ErrMemlimit) || in.n > maxControlTar {
			return nil, errDebTooLarge
		}
		if err != nil {
			return nil, fmt.Errorf("invalid .deb: reading control file: %w", err)
		}
		if len(raw) > maxControlFile {
			return nil, errDebTooLarge
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("invalid .deb: no control file in control member")
	}
	return raw, nil
}

// parseControl validates a single deb822 paragraph and keeps it verbatim,
// minus the fields the server computes itself.
func parseControl(raw []byte) (*debControl, error) {
	text := strings.Trim(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if text == "" {
		return nil, fmt.Errorf("invalid .deb: empty control file")
	}
	// The paragraph is emitted verbatim into Packages, where a stray CR, NUL
	// or form feed could read as a line break to some consumer and smuggle in
	// a field or a stanza; the control file is text, so refuse them.
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("invalid .deb: control file is not valid UTF-8")
	}
	for i := 0; i < len(text); i++ {
		if b := text[i]; (b < 0x20 && b != '\n' && b != '\t') || b == 0x7f {
			return nil, fmt.Errorf("invalid .deb: control file contains control character %#x", b)
		}
	}
	var kept []string
	values := map[string]string{}
	dropping := false
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			return nil, fmt.Errorf("invalid .deb: control file has more than one paragraph")
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(kept) == 0 && !dropping {
				return nil, fmt.Errorf("invalid .deb: control file starts with a continuation line")
			}
			if !dropping {
				kept = append(kept, line)
			}
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || !debFieldName.MatchString(name) {
			return nil, fmt.Errorf("invalid .deb: malformed control line %q", line)
		}
		key := strings.ToLower(name)
		if _, dup := values[key]; dup {
			return nil, fmt.Errorf("invalid .deb: duplicate control field %q", name)
		}
		values[key] = strings.TrimSpace(value)
		dropping = serverFields[key]
		if !dropping {
			kept = append(kept, line)
		}
	}
	ctl := &debControl{
		Package:      values["package"],
		Version:      values["version"],
		Architecture: values["architecture"],
		Paragraph:    strings.Join(kept, "\n"),
	}
	if err := ctl.validate(); err != nil {
		return nil, err
	}
	return ctl, nil
}

func (c *debControl) validate() error {
	switch {
	case !debPackageName.MatchString(c.Package):
		return fmt.Errorf("invalid .deb: control Package %q is not a valid package name", c.Package)
	case !debVersion.MatchString(c.Version):
		return fmt.Errorf("invalid .deb: control Version %q is not a valid version", c.Version)
	case !debArchName.MatchString(c.Architecture) || !releaseName.MatchString(c.Architecture):
		return fmt.Errorf("invalid .deb: control Architecture %q is not a valid architecture", c.Architecture)
	}
	return nil
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
