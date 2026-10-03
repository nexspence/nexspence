package yum

// repodata builder (#102): primary/filelists/other and repomd.xml are built
// from one snapshot so the checksums repomd advertises always match the
// documents actually served — dnf verifies them and rejects mismatches.
// Hrefs are RELATIVE (repodata/..., pool/...) so they resolve against the
// repo — or group — base URL the client is already using.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// repodataDocs is one consistent snapshot of the generated repo metadata.
type repodataDocs struct {
	Primary     []byte // plain XML
	PrimaryGz   []byte
	Filelists   []byte
	FilelistsGz []byte
	Other       []byte
	OtherGz     []byte
	Repomd      []byte
}

type filelistsXML struct {
	XMLName  xml.Name        `xml:"filelists"`
	XMLNS    string          `xml:"xmlns,attr"`
	Count    int             `xml:"packages,attr"`
	Packages []fileListEntry `xml:"package"`
}
type otherXML struct {
	XMLName  xml.Name        `xml:"otherdata"`
	XMLNS    string          `xml:"xmlns,attr"`
	Count    int             `xml:"packages,attr"`
	Packages []fileListEntry `xml:"package"`
}
type fileListEntry struct {
	PkgID   string     `xml:"pkgid,attr"`
	Name    string     `xml:"name,attr"`
	Arch    string     `xml:"arch,attr"`
	Version rpmVersion `xml:"version"`
}

// rpmChecksum carries the package digest dnf uses as the pkgid.
type rpmChecksum struct {
	Type  string `xml:"type,attr"`
	PkgID string `xml:"pkgid,attr"`
	Value string `xml:",chardata"`
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(data)
	_ = gw.Close()
	return buf.Bytes()
}

// buildRepodata generates the full metadata snapshot for a repo.
func (h *Handler) buildRepodata(ctx context.Context, repoName string) (*repodataDocs, error) {
	// Every component and every asset: one page of each cut a repository past
	// 500 packages (or 1000 files) short, and dnf reported the rest as
	// nonexistent (#617).
	comps, err := base.AllComponents(ctx, h.deps.Components, repoName)
	if err != nil {
		return nil, err
	}
	compMap := make(map[string]*domain.Component, len(comps))
	ids := make([]string, 0, len(comps))
	for i := range comps {
		compMap[comps[i].ID] = &comps[i]
		ids = append(ids, comps[i].ID)
	}
	byComp, err := h.deps.Assets.ListByComponentIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	var assets []domain.Asset
	for _, as := range byComp {
		assets = append(assets, as...)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })

	primary := primaryXML{XMLNS: "http://linux.duke.edu/metadata/common"}
	filelists := filelistsXML{XMLNS: "http://linux.duke.edu/metadata/filelists"}
	other := otherXML{XMLNS: "http://linux.duke.edu/metadata/other"}

	for _, a := range assets {
		if !strings.HasSuffix(a.Path, ".rpm") {
			continue
		}
		comp := compMap[a.ComponentID]
		if comp == nil {
			continue
		}
		arch := "x86_64"
		filename := path.Base(a.Path)
		// name-version-release.arch.rpm
		parts := strings.Split(strings.TrimSuffix(filename, ".rpm"), ".")
		if len(parts) >= 2 {
			arch = parts[len(parts)-1]
		}
		ver := rpmVersion{Epoch: "0", Ver: comp.Version, Rel: "1"}
		primary.Packages = append(primary.Packages, rpmPackage{
			Type:     "rpm",
			Name:     comp.Name,
			Arch:     arch,
			Version:  ver,
			Checksum: rpmChecksum{Type: "sha256", PkgID: "YES", Value: a.SHA256},
			Size:     rpmSize{Package: a.SizeBytes},
			// Relative to the repo root so it resolves under the repo or group URL.
			Location: rpmLoc{Href: strings.TrimPrefix(a.Path, "/")},
		})
		entry := fileListEntry{PkgID: a.SHA256, Name: comp.Name, Arch: arch, Version: ver}
		filelists.Packages = append(filelists.Packages, entry)
		other.Packages = append(other.Packages, entry)
	}
	primary.Count = len(primary.Packages)
	filelists.Count = len(filelists.Packages)
	other.Count = len(other.Packages)

	docs := &repodataDocs{}
	docs.Primary = marshalXML(primary)
	docs.PrimaryGz = gzipBytes(docs.Primary)
	docs.Filelists = marshalXML(filelists)
	docs.FilelistsGz = gzipBytes(docs.Filelists)
	docs.Other = marshalXML(other)
	docs.OtherGz = gzipBytes(docs.Other)

	now := time.Now().Unix()
	docs.Repomd = renderRepomd(now, []repomdEntry{
		repomdEntryFor("primary", docs.PrimaryGz, docs.Primary, now),
		repomdEntryFor("filelists", docs.FilelistsGz, docs.Filelists, now),
		repomdEntryFor("other", docs.OtherGz, docs.Other, now),
	})
	return docs, nil
}

func marshalXML(v any) []byte {
	out, _ := xml.Marshal(v)
	return append([]byte(xml.Header), out...)
}

// repomdEntryFor describes one metadata document: dnf checks the compressed
// bytes it downloads against Checksum and the decompressed ones against
// OpenChecksum, so both must be taken over the documents actually served.
func repomdEntryFor(typ string, gz, plain []byte, now int64) repomdEntry {
	return repomdEntry{
		Type:         typ,
		Location:     repomdLoc{Href: "repodata/" + typ + ".xml.gz"},
		Checksum:     repomdCksum{Type: "sha256", Value: fmt.Sprintf("%x", sha256.Sum256(gz))},
		OpenChecksum: &repomdCksum{Type: "sha256", Value: fmt.Sprintf("%x", sha256.Sum256(plain))},
		Size:         int64(len(gz)),
		OpenSize:     int64(len(plain)),
		Timestamp:    now,
	}
}

func renderRepomd(revision int64, entries []repomdEntry) []byte {
	return marshalXML(repomdXML{
		XMLNS:    "http://linux.duke.edu/metadata/repo",
		Revision: revision,
		Data:     entries,
	})
}
