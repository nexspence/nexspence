//go:build embed_ui

package nexspence

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var distFS embed.FS

// FrontendFS returns the embedded frontend assets rooted at the dist directory.
// ok is false when the frontend was not embedded (built without -tags embed_ui).
func FrontendFS() (fs.FS, bool) {
	sub, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		return nil, false
	}
	return sub, true
}
