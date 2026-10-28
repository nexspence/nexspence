package conda

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Bounds on what reading a package's metadata may cost. info/index.json is a
// few hundred bytes in practice; a .conda's info member holds only info/, so
// the tar around it is small too. A .tar.bz2 is read in full when it has to be:
// older builds put info/ after the payload.
const (
	maxIndexJSONBytes = 4 << 20   // info/index.json itself
	maxInfoTarBytes   = 256 << 20 // decompressed bytes of a .conda info member
	maxZstdWindow     = 64 << 20  // zstd window the .conda info member may demand
	// A .conda zip has three members; the central directory is checked before
	// archive/zip allocates a record per entry it claims.
	maxCondaZipEntries   = 64
	maxCondaZipDirectory = 1 << 20
)

// PkgMeta holds metadata extracted from an uploaded conda package.
type PkgMeta struct {
	Name        string
	Version     string
	Build       string
	BuildNumber int
	Subdir      string
	Depends     []string
	// Index is the package's info/index.json as published: a repodata.json
	// entry is this document plus the file's checksums and size (#677). Nil
	// when the metadata came from the filename alone.
	Index map[string]any
}

// ParseMeta extracts PkgMeta from a package's info/index.json: inside the
// tar of a .tar.bz2, or inside the info-*.tar.zst member of a .conda zip. It
// returns nil, nil when the package carries no info/index.json; the caller
// then falls back to the filename.
func ParseMeta(filename string, r io.ReaderAt, size int64) (*PkgMeta, error) {
	switch {
	case strings.HasSuffix(filename, ".tar.bz2"):
		return parseTarBz2(io.NewSectionReader(r, 0, size))
	case strings.HasSuffix(filename, ".conda"):
		return parseConda(r, size)
	}
	return nil, nil //nolint:nilnil // not a conda package file: caller falls back to the filename
}

// parseConda reads info/index.json from a .conda: a zip whose
// info-<name>-<version>-<build>.tar.zst member holds the info/ directory.
func parseConda(r io.ReaderAt, size int64) (*PkgMeta, error) {
	if err := checkZipDirectory(r, size); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("conda zip: %w", err)
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "info-") || !strings.HasSuffix(f.Name, ".tar.zst") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("conda %s: %w", f.Name, err)
		}
		defer func() { _ = rc.Close() }()
		zd, err := zstd.NewReader(rc, zstd.WithDecoderMaxWindow(maxZstdWindow), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, fmt.Errorf("conda %s: %w", f.Name, err)
		}
		defer zd.Close()
		return indexFromTar(io.LimitReader(zd, maxInfoTarBytes))
	}
	return nil, nil //nolint:nilnil // no info member: caller falls back to the filename
}

// parseTarBz2 reads info/index.json from a .tar.bz2 stream.
func parseTarBz2(r io.Reader) (*PkgMeta, error) {
	return indexFromTar(bzip2.NewReader(r))
}

// checkZipDirectory refuses a zip whose end-of-central-directory record claims
// more entries or a larger directory than a .conda ever has.
func checkZipDirectory(r io.ReaderAt, size int64) error {
	const eocdLen, maxComment = 22, 65535
	tail := int64(eocdLen + maxComment)
	if tail > size {
		tail = size
	}
	buf := make([]byte, tail)
	if _, err := r.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("conda zip: %w", err)
	}
	for i := len(buf) - eocdLen; i >= 0; i-- {
		if buf[i] != 'P' || buf[i+1] != 'K' || buf[i+2] != 5 || buf[i+3] != 6 {
			continue
		}
		entries := int(buf[i+10]) | int(buf[i+11])<<8
		dirSize := int64(buf[i+12]) | int64(buf[i+13])<<8 | int64(buf[i+14])<<16 | int64(buf[i+15])<<24
		if entries > maxCondaZipEntries || dirSize > maxCondaZipDirectory {
			return fmt.Errorf("conda zip: central directory of %d entries, %d bytes is not a .conda", entries, dirSize)
		}
		return nil
	}
	return errors.New("conda zip: no end of central directory")
}

// indexFromTar finds info/index.json in a (decompressed) tar stream.
func indexFromTar(r io.Reader) (*PkgMeta, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, nil //nolint:nilnil // no info/index.json in package: caller falls back to filename-derived metadata (not an error)
		}
		if err != nil {
			return nil, fmt.Errorf("conda tar: %w", err)
		}
		if strings.TrimPrefix(hdr.Name, "./") != "info/index.json" {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, maxIndexJSONBytes+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > maxIndexJSONBytes {
			return nil, fmt.Errorf("conda info/index.json exceeds %d bytes", maxIndexJSONBytes)
		}
		return unmarshalIndex(raw)
	}
}

type indexJSON struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Build       string   `json:"build"`
	BuildNumber int      `json:"build_number"`
	Subdir      string   `json:"subdir"`
	Depends     []string `json:"depends"`
}

func unmarshalIndex(raw []byte) (*PkgMeta, error) {
	var idx indexJSON
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("conda info/index.json: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("conda info/index.json: %w", err)
	}
	return &PkgMeta{
		Name:        idx.Name,
		Version:     idx.Version,
		Build:       idx.Build,
		BuildNumber: idx.BuildNumber,
		Subdir:      idx.Subdir,
		Depends:     idx.Depends,
		Index:       doc,
	}, nil
}

// metaFromFilename parses "numpy-1.24.0-py311_0.tar.bz2" → PkgMeta.
// Conda filenames are <name>-<version>-<build>.<ext>; the name may contain
// hyphens and the version and build never do, so the split runs from the
// right ("scikit-learn-1.3.0-py311_0" is scikit-learn 1.3.0, #677).
func metaFromFilename(filename string) *PkgMeta {
	stem := strings.TrimSuffix(strings.TrimSuffix(filename, ".tar.bz2"), ".conda")
	m := &PkgMeta{Name: stem}
	rest, build, ok := cutLast(stem, "-")
	if !ok {
		return m
	}
	name, version, ok := cutLast(rest, "-")
	if !ok {
		m.Name, m.Version = rest, build
		return m
	}
	m.Name, m.Version, m.Build = name, version, build
	return m
}

func cutLast(s, sep string) (before, after string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i <= 0 || i == len(s)-len(sep) {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}
