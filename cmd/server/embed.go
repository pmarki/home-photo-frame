//go:build !dev

package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

//go:embed all:frontend/dist
var embeddedFrontend embed.FS

func init() {
	dist, err := fs.Sub(embeddedFrontend, "frontend/dist")
	if err != nil {
		log.Fatal("embed: cannot sub frontend/dist:", err)
	}
	frontendFS = dist
	frontendHandler = func() http.Handler { return spaHandler(dist) }
}
