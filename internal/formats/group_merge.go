package formats

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
