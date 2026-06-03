package conda

import (
	"archive/tar"
	"bytes"
	"compress/bzip2"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PkgMeta holds metadata extracted from an uploaded conda package.
type PkgMeta struct {
	Name        string
	Version     string
	Build       string
	BuildNumber int
	Subdir      string
	Depends     []string
}

// ParseMeta extracts PkgMeta from .tar.bz2 or .conda bytes.
// For .conda (zip+zstd), returns metadata derived from filename only (zstd not in stdlib).
func ParseMeta(filename string, data []byte) (*PkgMeta, error) {
	if strings.HasSuffix(filename, ".tar.bz2") {
		return parseTarBz2(data)
	}
	// .conda: fall back to filename parsing (zstd requires external dep)
	return metaFromFilename(filename), nil
}

func parseTarBz2(data []byte) (*PkgMeta, error) {
	br := bzip2.NewReader(bytes.NewReader(data))
	tr := tar.NewReader(br)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("conda tar: %w", err)
		}
		if hdr.Name == "info/index.json" {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			return unmarshalIndex(raw)
		}
	}
	// Not found — fall back to filename
	return nil, nil //nolint:nilnil // (nil, nil) signals not-found; callers check the returned value
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
	return &PkgMeta{
		Name:        idx.Name,
		Version:     idx.Version,
		Build:       idx.Build,
		BuildNumber: idx.BuildNumber,
		Subdir:      idx.Subdir,
		Depends:     idx.Depends,
	}, nil
}

// metaFromFilename parses "numpy-1.24.0-py311_0.tar.bz2" → PkgMeta.
// Conda filenames: <name>-<version>-<build>.<ext>
func metaFromFilename(filename string) *PkgMeta {
	base := strings.TrimSuffix(strings.TrimSuffix(filename, ".tar.bz2"), ".conda")
	parts := strings.SplitN(base, "-", 3)
	m := &PkgMeta{}
	if len(parts) >= 1 {
		m.Name = parts[0]
	}
	if len(parts) >= 2 {
		m.Version = parts[1]
	}
	if len(parts) >= 3 {
		m.Build = parts[2]
	}
	return m
}
