// Package webui holds the gateway's web page, built from web/ (React) by
// `make web` into static/ and committed, so that `go install` gets the page
// without node. `make web-check` proves static/ matches web/.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var files embed.FS

// Handler serves the page: index.html for every path that is not a built
// asset, so that the page's own routes load it too.
func Handler() http.Handler {
	static, err := fs.Sub(files, "static")
	if err != nil {
		panic(err) // the embedded tree always has static/
	}

	assets := http.FileServerFS(static)

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		if strings.HasPrefix(name, "assets/") {
			if _, err := fs.Stat(static, name); err == nil {
				// Built assets carry a content hash in their name.
				rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				assets.ServeHTTP(rw, r)

				return
			}

			http.NotFound(rw, r)

			return
		}

		index, err := fs.ReadFile(static, "index.html")
		if err != nil {
			http.Error(rw, "the page was not built: run `make web`", http.StatusNotFound)

			return
		}

		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-cache")
		_, _ = rw.Write(index)
	})
}
