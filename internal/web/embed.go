// Package web embeds the prebuilt Meerkat frontend (built from frontend/ with `npm run build`).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var embedded embed.FS

// Assets returns the frontend build output rooted at assets/:
//
//	app/                 static browser bundle (index.html + hashed assets)
//	mount/meerkat-ui.js  self-contained IIFE exposing window.MeerkatUI.mount
//	mount/meerkat-ui.css companion stylesheet for the mount bundle
func Assets() fs.FS {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		panic(err) // unreachable: "assets" is embedded at build time
	}
	return sub
}
