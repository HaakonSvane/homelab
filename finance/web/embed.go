// Package web embeds the static assets (CSS, htmx, passkey glue) and serves them
// with content-hashed URLs so browsers can cache them forever.
//
// static/htmx.min.js is htmx 2.0.11, vendored unmodified from the npm tarball
// (htmx.org-2.0.11.tgz, integrity sha512-Thx/WtpeOQqSrqBCw/A1cwGJGg4UrVa3+sW0GmrM3p4gJgO89ecH4qtbnyzDDWFvBTqjnIMCgELTNt636dtamA==;
// file sha256 d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717).
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

// Static is the static file tree (app.css, htmx.min.js, passkey.js).
var Static, _ = fs.Sub(files, "static")

var versions = func() map[string]string {
	v := map[string]string{}
	fs.WalkDir(Static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(Static, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		v[path] = hex.EncodeToString(sum[:6])
		return nil
	})
	return v
}()

// URL returns the cache-busting URL of a static file, e.g. /static/app.css?v=1a2b3c4d5e6f.
func URL(name string) string { return "/static/" + name + "?v=" + versions[name] }

// Handler serves /static/. Versioned URLs are cached for a year; anything else is
// revalidated.
func Handler() http.Handler {
	files := http.StripPrefix("/static/", http.FileServerFS(Static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
