package conan

import (
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// Conan v2 search (#247):
//
//	GET /v2/conans/search?q=<pattern>&ignorecase=<bool>   → {"results": [refs]}
//	GET /v2/conans/:n/:v/:u/:c/search?list_only=<bool>    → {pkgID: info} for the latest recipe revision
//	GET /v2/conans/:n/:v/:u/:c/revisions/:rrev/search?list_only=<bool> → {pkgID: info}
//
// Contract per conan_server (conans/server/rest/controller/v2/) and the Conan 2
// client (conan/internal/rest/rest_client_v2.py): recipe search returns bare
// reference strings, package search returns a JSON object keyed by package ID
// whose values carry the RAW conaninfo.txt text under "content" — the client
// parses it itself ("Avoid serializing conaninfo in server side"), and with
// list_only=True only the keys matter.

// patternMaxBytes bounds the search pattern: references are short, so a long
// pattern is never legitimate, and compiling one is the only place this
// handler spends CPU on unauthenticated input.
const patternMaxBytes = 256

// v2SearchRecipes answers the repository-wide recipe search. References are
// rendered the way the client prints them — "name/version" when user and
// channel are the "_" placeholders, "name/version@user/channel" otherwise —
// and the fnmatch-style pattern is matched against that rendering, folding
// case unless ignorecase=False (both defaults mirror conan_server).
//
// Only the v2 storage tree is indexed. Assets a Conan 1 client stored under
// /files/... deliberately do not surface: every other v2 route (latest,
// revisions, download, delete) answers 404 for them, and advertising a
// reference the follow-up calls cannot resolve makes version-range
// resolution pick it and abort the whole install.
func (h *Handler) v2SearchRecipes(c *gin.Context, repoName string) {
	q := c.Query("q")
	if len(q) > patternMaxBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "search pattern too long"})
		return
	}
	rx, err := fnmatchRegexp(q, !strings.EqualFold(c.Query("ignorecase"), "false"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad search pattern: " + err.Error()})
		return
	}
	// Exhaustive prefix listing — the paged Assets.List would cap the result
	// at its first page and silently drop recipes from search.
	assets, err := h.deps.Assets.ListByRepoAndPath(c.Request.Context(), repoName, "/v2/conans/")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	seen := map[string]struct{}{}
	for _, a := range assets {
		if ref, ok := refFromAssetPath(a.Path); ok {
			seen[ref] = struct{}{}
		}
	}
	results := make([]string, 0, len(seen))
	for ref := range seen {
		if rx.MatchString(ref) {
			results = append(results, ref)
		}
	}
	sort.Strings(results)
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// refFromAssetPath recovers the recipe reference from an asset stored by the
// v2 revisions API (verbatim under /v2/conans/{n}/{v}/{u}/{c}/revisions/...).
func refFromAssetPath(p string) (string, bool) {
	if !strings.HasPrefix(p, "/v2/conans/") {
		return "", false
	}
	segs := strings.Split(strings.TrimPrefix(p, "/v2/conans/"), "/")
	if len(segs) < 5 || segs[4] != "revisions" {
		return "", false
	}
	name, version, user, channel := segs[0], segs[1], segs[2], segs[3]
	if name == "" || version == "" {
		return "", false
	}
	if user == "_" && channel == "_" {
		return name + "/" + version, true
	}
	return name + "/" + version + "@" + user + "/" + channel, true
}

// matchAll is the compiled form of an absent or "*" pattern: the empty,
// unanchored regexp matches every reference.
var matchAll = regexp.MustCompile("")

// fnmatchRegexp translates an fnmatch-style pattern (*, ?, [seq], [!seq]) to
// an anchored regexp. Unlike path.Match, `*` crosses "/" — the pattern
// "boost*" has to match "boost/1.83.0", which is how both fnmatch and
// conan_server behave.
func fnmatchRegexp(pattern string, ignorecase bool) (*regexp.Regexp, error) {
	if pattern == "" || pattern == "*" {
		return matchAll, nil
	}
	var b strings.Builder
	if ignorecase {
		b.WriteString("(?i)")
	}
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch ch := pattern[i]; ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' { // first ']' is literal
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j >= len(pattern) {
				// Unterminated class: treat "[" as a literal, like fnmatch.
				b.WriteString(regexp.QuoteMeta("["))
				continue
			}
			// A backslash is literal inside an fnmatch class; unescaped it
			// would swallow the closing "]" in the regexp and fail to compile.
			cls := strings.ReplaceAll(pattern[i+1:j], `\`, `\\`)
			switch {
			case strings.HasPrefix(cls, "!"):
				// fnmatch negation is "!", not "^"...
				cls = "^" + cls[1:]
			case strings.HasPrefix(cls, "^"):
				// ...and a leading "^" is a literal caret, like in Python's
				// fnmatch — escape it so the regexp does not negate.
				cls = `\^` + cls[1:]
			}
			b.WriteString("[" + cls + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// v2SearchPackages answers the binary package search for one recipe, at the
// reference level (resolve the latest recipe revision first, like
// conan_server) or for an explicit revision. 404 when the recipe (revision)
// does not exist; an existing revision with no packages is an empty object,
// which the download flow treats as "recipe-only" rather than an error.
func (h *Handler) v2SearchPackages(c *gin.Context, repoName, p string) {
	refBase := strings.TrimSuffix(p, "/search")

	if !strings.Contains(refBase, "/revisions/") {
		rel, ok := h.pathsUnder(c, repoName, refBase+"/revisions/")
		if !ok {
			return
		}
		var bestRev string
		var bestTime time.Time
		for rev, ts := range revisionTimes(rel) {
			if bestRev == "" || ts.After(bestTime) {
				bestRev, bestTime = rev, ts
			}
		}
		if bestRev == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "recipe not found"})
			return
		}
		refBase += "/revisions/" + bestRev
	}

	rel, ok := h.pathsUnder(c, repoName, refBase+"/")
	if !ok {
		return
	}
	if len(rel) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "recipe revision not found"})
		return
	}

	// Files live under packages/<pkgID>/revisions/<prev>/files/...; report
	// each package once, reading conaninfo.txt from its newest revision.
	type pkgRev struct {
		prev string
		ts   time.Time
	}
	latest := map[string]pkgRev{}
	for f, ts := range rel {
		rest, found := strings.CutPrefix(f, "packages/")
		if !found {
			continue
		}
		pkgID, tail, ok2 := strings.Cut(rest, "/")
		if !ok2 || pkgID == "" {
			continue
		}
		revPart, ok3 := strings.CutPrefix(tail, "revisions/")
		if !ok3 {
			continue
		}
		prev, _, ok4 := strings.Cut(revPart, "/")
		if !ok4 || prev == "" {
			continue
		}
		if cur, seenIt := latest[pkgID]; !seenIt || ts.After(cur.ts) {
			latest[pkgID] = pkgRev{prev: prev, ts: ts}
		}
	}

	listOnly := strings.EqualFold(c.Query("list_only"), "true")
	out := gin.H{}
	for pkgID, pr := range latest {
		if listOnly {
			out[pkgID] = gin.H{}
			continue
		}
		out[pkgID] = h.packageInfo(c, repoName,
			refBase+"/packages/"+pkgID+"/revisions/"+pr.prev+"/files/conaninfo.txt")
	}
	c.JSON(http.StatusOK, out)
}

// conanInfoMaxBytes bounds the conaninfo.txt read: the file is a few hundred
// bytes of settings/options, so anything beyond this is not a conaninfo.
const conanInfoMaxBytes = 64 << 10

// packageInfo returns the value the package search reports for one binary:
// the raw conaninfo.txt text under "content" when the file is readable, an
// empty object otherwise — the client falls back to the value as-is.
func (h *Handler) packageInfo(c *gin.Context, repoName, infoPath string) gin.H {
	rc, _, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, infoPath)
	if err != nil {
		return gin.H{}
	}
	defer func() { _ = rc.Close() }()
	content, err := io.ReadAll(io.LimitReader(rc, conanInfoMaxBytes))
	if err != nil {
		return gin.H{}
	}
	return gin.H{"content": string(content)}
}
