// Package formats defines the FormatHandler interface and shared types.
// Each artifact format (maven2, npm, docker, etc.) lives in a sub-package
// and implements FormatHandler.
package formats

import (
	"github.com/gin-gonic/gin"
)

// FormatHandler is implemented by each artifact format package.
//
// ServeHTTP receives full control over the request so that complex protocols
// (Docker blobs/manifests, npm metadata, GOPROXY) can do their own
// method/path dispatch. Before calling ServeHTTP the outer router sets:
//
//	c.Param("repoName") — the repository name
//	c.Param("path")     — the artifact path (may be empty for protocol root)
type FormatHandler interface {
	// Name returns the repository format string, e.g. "maven2", "npm", "docker".
	Name() string
	// ServeHTTP handles all HTTP requests for this format.
	ServeHTTP(c *gin.Context)
}

// GroupMemberKey is set on the gin context of a request a group repository is
// fanning out to one of its members. A group walks its members until one answers
// something other than 404, so a member handler may need to report an upstream
// "not mine" — a Helm remote answering 403 for a chart it does not host, say — as
// a miss during fan-out, while the same answer to a request addressed straight at
// the member must reach the client unchanged.
const GroupMemberKey = "nexspence.group_member"
