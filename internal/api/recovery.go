package api

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Preserve net/http's stream-abort signal. Recovering it into an ordinary
// handler return would send a successful terminating chunk (or HTTP/2 END_STREAM)
// for an incomplete artifact. Other panics retain Gin's normal 500 behavior.
func recoveryMiddleware() gin.HandlerFunc {
	// Gin 1.12 swallows ErrAbortHandler before calling CustomRecovery's
	// callback, so it cannot preserve this signal for net/http.
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(http.ErrAbortHandler)
				}
				_, _ = fmt.Fprintf(gin.DefaultErrorWriter, "[Recovery] panic: %v\n%s", recovered, debug.Stack())
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
