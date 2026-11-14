package repoproxy

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// A streaming failure after headers cannot be represented by another status or
// an appended JSON error. Let net/http close HTTP/1 or reset the HTTP/2 stream
// without its successful terminator, so clients reject the partial artifact.
// In-process recorders (including group handlers) have no network stream to
// abort: their caller receives the ordinary error instead.
func abortStreamOnError(c *gin.Context, err *error) {
	if *err == nil {
		return
	}
	if !c.Writer.Written() {
		// Format handlers may still send an error response; its body does not
		// have the upstream artifact's declared length.
		c.Writer.Header().Del("Content-Length")
		return
	}
	var writer http.ResponseWriter = c.Writer
	for {
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		writer = unwrapper.Unwrap()
	}
	_, http1 := writer.(http.Hijacker)
	_, http2 := writer.(http.Pusher)
	if http1 || http2 {
		panic(http.ErrAbortHandler)
	}
}
