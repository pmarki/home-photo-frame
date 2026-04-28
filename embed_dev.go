//go:build dev

package main

import (
	"net/http"
	"os"
)

func init() {
	frontendFS = os.DirFS("./frontend/dist")
	frontendHandler = func() http.Handler { return spaHandler(frontendFS) }
}
