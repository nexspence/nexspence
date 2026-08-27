package pypi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// The minimum-package-age policy (#323) needs per-file upload dates, and the
// PEP 503 HTML page the proxy pipeline serves carries none. Dates come from a
// separate upstream request for the PEP 691 JSON page (upload-time is PEP 700);
// an upstream that only speaks HTML, or omits upload-time, cannot be policed —
// hybrid failure mode: the policy is skipped and the gap logged.

// pep691JSONAccept asks upstream for the JSON simple page. This request is
// internal to the date lookup; the page pipeline itself keeps forcing HTML
// upstream (#191), which TestPyPI_AgePolicy_ServedPageStaysHTML pins.
const pep691JSONAccept = "application/vnd.pypi.simple.v1+json"

// maxSimpleJSONBytes bounds the date-lookup response read.
const maxSimpleJSONBytes = 16 << 20

// fileAnchorRe matches one file anchor (with its trailing <br>, if any) on a
// PEP 503 per-package page — a machine-generated list of
// <a href=...>filename</a> elements. anchorRe in groupindex.go extracts pairs;
// this one has to swallow the whole element so a filtered file leaves no
// dangling markup behind.
var fileAnchorRe = regexp.MustCompile(`(?s)<a\b[^>]*>(.*?)</a>(\s*<br\s*/?>)?`)

// uploadTimesEntry caches one package's filename→upload-time map. A nil map is
// cached too: an upstream without dates should not be re-asked on every
// request within the TTL.
type uploadTimesEntry struct {
	fetched time.Time
	dates   map[string]time.Time
}

// FilterSimpleHTMLByAge removes from a per-package simple page every file
// anchor whose upload time is after cutoff — and, failing closed within a
// dated map, every anchor the map does not name at all (a file uploaded after
// the dates were fetched has an unknowable age). dates must be non-empty; the
// caller handles the no-dates hybrid case.
func FilterSimpleHTMLByAge(body []byte, dates map[string]time.Time, cutoff time.Time) []byte {
	return fileAnchorRe.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := fileAnchorRe.FindSubmatch(m)
		filename := strings.TrimSpace(string(sub[1]))
		t, ok := dates[filename]
		if !ok || t.After(cutoff) {
			return nil
		}
		return m
	})
}

// simpleUploadTimes returns the filename→upload-time map for pkg from the
// upstream PEP 691 JSON page, cached per (repo, pkg) for the repository's
// metadata TTL. A nil map means no dates are available.
func (h *Handler) simpleUploadTimes(ctx context.Context, repo *domain.Repository, pkg string) map[string]time.Time {
	key := repo.Name + "/" + pkg
	ttl := repoproxy.MetadataMaxAge(repo)

	h.ageMu.Lock()
	if e, ok := h.ageDates[key]; ok && time.Since(e.fetched) < ttl {
		h.ageMu.Unlock()
		return e.dates
	}
	h.ageMu.Unlock()

	dates := fetchSimpleUploadTimes(ctx, repo, pkg)

	h.ageMu.Lock()
	if h.ageDates == nil {
		h.ageDates = make(map[string]uploadTimesEntry)
	}
	h.ageDates[key] = uploadTimesEntry{fetched: time.Now(), dates: dates}
	h.ageMu.Unlock()
	return dates
}

func fetchSimpleUploadTimes(ctx context.Context, repo *domain.Repository, pkg string) map[string]time.Time {
	hdr := http.Header{}
	hdr.Set("Accept", pep691JSONAccept)
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/simple/"+pkg+"/", "", hdr)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK ||
		!strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSimpleJSONBytes))
	if err != nil {
		return nil
	}
	var doc struct {
		Files []struct {
			Filename   string `json:"filename"`
			UploadTime string `json:"upload-time"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	out := make(map[string]time.Time, len(doc.Files))
	for _, f := range doc.Files {
		if f.Filename == "" || f.UploadTime == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, f.UploadTime); err == nil {
			out[f.Filename] = t
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fileAgeAllowed is the download half of the policy: a direct /packages/ URL
// must not bypass the simple-page filter. On refusal the 403 is already
// written. The package name is the distribution segment of the filename
// (everything before the first "-", per wheel/sdist naming), normalized the
// way the simple index is.
func (h *Handler) fileAgeAllowed(c *gin.Context, repo *domain.Repository, filePath string, minAge time.Duration) bool {
	filename := path.Base(filePath)
	dist, _, ok := strings.Cut(filename, "-")
	if !ok || dist == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"cannot derive a package name from %q; this repository enforces a minimum package age", filename)})
		return false
	}
	dates := h.simpleUploadTimes(c.Request.Context(), repo, normalizePackageName(dist))
	if len(dates) == 0 {
		log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — no upload dates available", repo.Name, dist)
		return true
	}
	uploaded, ok := dates[filename]
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%q is not in the package's known upload history; this repository enforces a minimum package age", filename)})
		return false
	}
	if age := time.Since(uploaded); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s was uploaded %s ago; this repository enforces a minimum package age of %s",
			filename, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}
