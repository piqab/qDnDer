// Package ui serves the embedded web UI and admin panel.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var files embed.FS

// Handler serves the single-page app from the binary.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "web")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
