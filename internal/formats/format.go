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
