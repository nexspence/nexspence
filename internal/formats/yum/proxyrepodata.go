package yum

// A Yum repository names its metadata documents in repomd.xml, and an upstream
// that follows the protocol names them by the checksum of their contents:
// "repodata/707889cc…-primary.xml.gz". The canonical names a hosted
// repository serves — "repodata/primary.xml" — are not among them.
//
// A group merges its members at the canonical names, so a proxy member that
// relayed the client's path verbatim contributed nothing to that merge: the
// canonical path answered 404 upstream, and the upstream's packages were
// invisible in the group. A group of proxy members only had no canonical
// document to merge either, and fell back to relaying a member's own
// repomd.xml — which works, but leaves a hosted member's packages out.
//
// So the proxy resolves the canonical name through upstream's own repomd.xml
// and serves the document it points at. A client that reads the proxy's
// repomd.xml is unaffected: it asks for the checksum-prefixed name, which is
// still relayed verbatim.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// maxRepomdBytes bounds the repomd.xml read while resolving a document name.
// The file lists a handful of documents and their checksums — kilobytes — so a
// megabyte is generous; the bound keeps a hostile or broken upstream from
// buffering without limit.
const maxRepomdBytes = 1 << 20

// canonicalRepodataDoc names the document a canonical repodata path holds:
// "/repodata/primary.xml.gz" is the "primary" document. repomd.xml itself is
// not one of them — it is the index that names the others, and it is relayed
// as it came.
func canonicalRepodataDoc(p string) (string, bool) {
	for _, typ := range repodataDocTypes {
		if p == "/repodata/"+typ+".xml" || p == "/repodata/"+typ+".xml.gz" {
			return typ, true
		}
	}
	return "", false
}

// upstreamRepodataHref resolves the path upstream publishes the given
// document under, through upstream's own repomd.xml.
//
// The result is the repository-relative path upstream named ("repodata/
// <checksum>-primary.xml.gz"). An upstream that names no such document — or
// whose repomd.xml cannot be read — yields an error, and the caller falls
// back to relaying the canonical path as it is.
func upstreamRepodataHref(ctx context.Context, repo *domain.Repository, docType string) (string, error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/repodata/repomd.xml", "", nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream repomd.xml: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRepomdBytes+1))
	if err != nil {
		return "", fmt.Errorf("upstream repomd.xml: %w", err)
	}
	if int64(len(body)) > maxRepomdBytes {
		return "", fmt.Errorf("upstream repomd.xml exceeds %d bytes", maxRepomdBytes)
	}
	var doc repomdXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("upstream repomd.xml: %w", err)
	}
	for _, entry := range doc.Data {
		if entry.Type == docType && entry.Location.Href != "" {
			return entry.Location.Href, nil
		}
	}
	return "", fmt.Errorf("upstream repomd.xml names no %q document", docType)
}

// serveProxyRepodataDoc serves a canonical repodata document for a proxy
// repository: the document upstream publishes it as, in the flavor the client
// asked for.
//
// The upstream's own bytes are cached under the canonical name — the upstream
// compressed document — and the flavor is applied on the way out, so the
// cached copy stays the upstream's and a change of the upstream's naming does
// not invalidate it.
func (h *Handler) serveProxyRepodataDoc(c *gin.Context, repo *domain.Repository, p, docType string) {
	upstreamPath := p
	if href, err := upstreamRepodataHref(c.Request.Context(), repo, docType); err == nil {
		upstreamPath = "/" + strings.TrimPrefix(href, "/")
	}
	// An upstream that names no such document — or whose repomd.xml cannot be
	// read — is served the canonical path as it is, which is the answer for an
	// upstream that publishes the canonical names itself.
	plain := !strings.HasSuffix(p, ".gz")
	contentType := "application/xml; charset=utf-8"
	if !plain {
		contentType = "application/x-gzip"
	}
	if err := repoproxy.ServeGETWithOptions(c, h.deps, repo, p, upstreamPath, base.Coords{},
		contentType, repoproxy.MetadataMaxAge(repo), repoproxy.ServeOptions{
			Rewrite:     repodataFlavorRewrite(plain),
			ContentType: contentType,
		}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	}
}

// repodataFlavorRewrite returns the rewrite that turns an upstream document
// into the flavor the client asked for: the plain XML for a ".xml" request,
// the compressed bytes for a ".xml.gz" one.
//
// The upstream's bytes are cached as they came, and the rewrite runs on every
// answer — so the same cached copy serves both flavors, and a body that is
// already in the requested flavor is passed through unchanged.
func repodataFlavorRewrite(plain bool) func([]byte) []byte {
	return func(body []byte) []byte {
		compressed := len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b
		switch {
		case plain && compressed:
			out, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				return body
			}
			defer func() { _ = out.Close() }()
			decompressed, err := io.ReadAll(out)
			if err != nil {
				return body
			}
			return decompressed
		case !plain && !compressed:
			return gzipBytes(body)
		}
		return body
	}
}
