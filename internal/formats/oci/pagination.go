package oci

import (
	"net/url"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Pagination for the OCI Distribution list endpoints. The spec gives
// GET /v2/<name>/tags/list and GET /v2/_catalog the same shape:
//
//	?n=<count>&last=<entry>  → at most n entries, sorted, starting after last
//	Link: <...?n=10&last=v1.9>; rel="next"   when the answer was truncated
//
// Everything here is deliberately free of any knowledge of tags vs repository
// names so the catalog endpoint can reuse it unchanged.

// PageParams are the ?n= / ?last= arguments of a list request.
type PageParams struct {
	// n is the maximum number of entries to return; 0 means "no limit", which
	// is what both an absent n and a non-positive one are treated as. The spec
	// lets a registry reject a malformed n with 400, but returning the full
	// list is the friendlier reading and matches what clients that omit n
	// already get.
	N int
	// last is the cursor: only entries strictly greater than it are returned.
	Last string
}

// ParsePageParams reads the pagination arguments off a request.
func ParsePageParams(c *gin.Context) PageParams {
	p := PageParams{Last: c.Query("last")}
	if raw := c.Query("n"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			p.N = n
		}
	}
	return p
}

// Paginate cuts one page out of a sorted, ascending list of entries. It returns
// the page and whether entries remain after it.
//
// Entries equal to the cursor are skipped, not just the first one, so a list
// that somehow holds duplicates still advances: every page ends on a strictly
// larger entry than the cursor that produced it, which is what makes a client
// following the Link headers terminate.
func Paginate(entries []string, p PageParams) (page []string, more bool) {
	// sort.SearchStrings finds the first entry >= last; the cursor is exclusive,
	// so skip the run equal to it as well.
	start := 0
	if p.Last != "" {
		start = sort.SearchStrings(entries, p.Last)
		for start < len(entries) && entries[start] <= p.Last {
			start++
		}
	}
	rest := entries[start:]
	if p.N <= 0 || len(rest) <= p.N {
		return rest, false
	}
	return rest[:p.N], true
}

// nextLink renders the RFC 8288 Link header value naming the next page of the
// list the request asked for. The URL is built from the request's own path so
// the client stays on the /v2/ form it used: a Docker client on the short path
// only sends its credentials to /v2/, and a long-path link would drop it onto
// an unauthenticated surface (the same trap as issue #47).
func nextLink(c *gin.Context, n int, last string) string {
	q := url.Values{}
	q.Set("n", strconv.Itoa(n))
	q.Set("last", last)
	// EscapedPath keeps a name that needed escaping escaped, and Encode escapes
	// the cursor, so a tag holding a '+' or a space survives the round trip.
	return "<" + c.Request.URL.EscapedPath() + "?" + q.Encode() + `>; rel="next"`
}

// SetNextLink attaches the Link header for a truncated page. Nothing is set for
// a complete answer — the absence of the header is what tells a client to stop.
func SetNextLink(c *gin.Context, p PageParams, page []string, more bool) {
	if !more || len(page) == 0 {
		return
	}
	c.Header("Link", nextLink(c, p.N, page[len(page)-1]))
}
