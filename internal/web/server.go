package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFiles embed.FS

// RegisterRoutes registers the web UI routes on the given ServeMux.
// Serves the embedded static files at the root path.
func RegisterRoutes(mux *http.ServeMux) {
	// Create a sub-filesystem rooted at "static"
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic("failed to create sub filesystem for static files: " + err.Error())
	}

	// Serve static files
	fileServer := http.FileServer(http.FS(staticFS))

	// Handle root path - serve index.html
	mux.Handle("/", fileServer)
}
