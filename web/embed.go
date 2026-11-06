// Package web embeds the built dashboard (web/dist) into the binary.
package web

import (
	"embed"
	"io/fs"
)

// dist holds the output of `pnpm build`. The committed dist/.gitkeep keeps the directory present, so the Go build
// works before the dashboard has been built.
//
//go:embed all:dist
var dist embed.FS

// FS returns the built dashboard with dist/ as its root (index.html, assets/...).
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub fails only for an invalid path, and "dist" is a valid constant.
		panic("web: " + err.Error())
	}
	return sub
}
