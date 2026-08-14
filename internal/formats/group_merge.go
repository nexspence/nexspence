package formats

import "github.com/gin-gonic/gin"

// GroupIndexPart is one member's successful index response, in member order.
type GroupIndexPart struct {
	Member string // member repo name — used to rewrite member URLs to the group
	Body   []byte
}

// GroupIndexMerger is optionally implemented by format handlers whose index
// documents must be merged across group members (#99). Without it, the group
// layer returns the first non-404 member response — correct for artifacts,
// wrong for aggregated indexes (only one member's content is visible, and
// formats whose "not found" is an empty 200 shadow every member after them).
type GroupIndexMerger interface {
	// GroupIndexSourcePath maps a requested path to the member path whose
	// bodies feed the merge, and reports whether path is a mergeable index.
	// Usually source == path; maven checksum paths (.sha1/.md5/.sha256) map
	// to maven-metadata.xml itself so the checksum is computed over the
	// MERGED document.
	GroupIndexSourcePath(path string) (source string, ok bool)
	// MergeGroupIndex merges member bodies (member order = priority, first
	// wins on conflict) into one document rooted at the group's URL.
	MergeGroupIndex(groupName, path string, parts []GroupIndexPart) (body []byte, contentType string, err error)
}

// GroupIndexFetcher returns the body the GROUP itself serves at path — the
// merged document, not any single member's copy.
type GroupIndexFetcher func(path string) ([]byte, error)

// GroupIndexDependentMerger is optionally implemented alongside GroupIndexMerger
// by formats whose index document describes OTHER index documents: apt's Release
// carries a checksum of every Packages file, yum's repomd.xml one of primary.xml
// and its siblings.
//
// Merging those from the members' own copies cannot work. Each member's document
// describes that member's own indexes, while the group serves the union — so the
// checksums never match the bytes the client then downloads, and apt/dnf reject
// the repository outright. Such a merger is handed a fetcher instead, and builds
// its document from what the group itself serves.
//
// The fetched path must not itself be dependent: the fetcher merges it without
// one, so a format that pointed a document at itself would get an empty result
// rather than a recursion through the group layer.
type GroupIndexDependentMerger interface {
	GroupIndexMerger
	// MergeGroupIndexWithFetch merges like MergeGroupIndex, additionally able to
	// ask for the group's own merged body at another index path.
	MergeGroupIndexWithFetch(groupName, path string, parts []GroupIndexPart,
		fetch GroupIndexFetcher) (body []byte, contentType string, err error)
}

// GroupIndexStrictMerger is optionally implemented alongside GroupIndexMerger by
// formats whose merged index must never be served incomplete.
//
// By default the group skips a member that answered non-2xx, so one down
// upstream cannot take the whole group with it. For most indexes a short list is
// a degraded but honest answer. For the OCI referrers index it is not: a client
// reads a short list as "this image carries no signature", so silently dropping
// a member that could not be consulted turns "I could not check" into a
// statement about the subject. A format that says so here gets the opposite
// default — the group relays the member's failure instead of merging around it,
// and a merge that cannot be completed is an error rather than a degradation to
// the first member's document.
type GroupIndexStrictMerger interface {
	// GroupIndexMemberFailureIsFatal reports whether a member's non-2xx answer
	// on path means "I could not check" — which must fail the whole group —
	// rather than "I have nothing to contribute", which is skipped as usual.
	GroupIndexMemberFailureIsFatal(path string, status int) bool
}

// GroupIndexPaginator is optionally implemented alongside GroupIndexMerger by
// formats whose index endpoints are paginated.
//
// Paging a member and paging the merge are different answers. A member asked for
// its first n entries contributes a truncated list, and the entries past its own
// cut are then unreachable through every page of the group: the cursor the
// client sends back names an entry of the merged list, which each member resolves
// against its own. So the group asks members for their COMPLETE documents and
// cuts the client's page out of the merged one, where the order the cursor refers
// to is the order the client was served.
type GroupIndexPaginator interface {
	// GroupIndexMemberQuery returns the raw query members are asked with,
	// given the raw query the client sent. It is where a paginated index drops
	// the paging arguments — and where an index that is filtered rather than
	// paged keeps them, since a filter narrows a member's answer without
	// hiding anything the merge would have to reach past.
	GroupIndexMemberQuery(path, clientQuery string) string
	// PageGroupIndex cuts the page the client asked for out of the merged
	// document and sets whatever cursor header the format's protocol uses on
	// c. Returning merged unchanged is what an unpaginated path does.
	PageGroupIndex(c *gin.Context, path string, merged []byte) ([]byte, error)
}
