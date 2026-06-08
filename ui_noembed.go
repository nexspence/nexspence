//go:build !embed_ui

// Package nexspence exposes the optional embedded frontend. Without the
// `embed_ui` build tag no assets are embedded and FrontendFS reports ok=false.
package nexspence

import "io/fs"

// FrontendFS reports that no frontend is embedded in this build.
func FrontendFS() (fs.FS, bool) { return nil, false }
