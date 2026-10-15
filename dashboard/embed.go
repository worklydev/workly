// Package dashboard serves the web UI, embedded into the binary.
//
// The UI is built into dist/ (gitignored apart from .gitkeep). When dist/ has
// no index.html, for example in a fresh clone, a placeholder page is served so
// the Go build never depends on the frontend build.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the dashboard embedded in this binary.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return HandlerFS(sub)
}

// HandlerFS serves a dashboard build from fsys: files as they are, and
// index.html for any other path, so that deep links such as /queues/emails
// load the app and its router takes over. A build that wraps the dashboard
// (workly-cloud) serves its own dist with it.
func HandlerFS(fsys fs.FS) http.Handler {
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return http.HandlerFunc(placeholder)
	}
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if _, err := fs.Stat(fsys, name); err == nil {
				// Vite puts content-hashed files in assets/.
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			// A missing file (one with an extension) is a real 404, not a route.
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		// The shell must never be cached, or clients keep asking for old assets.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}

func placeholder(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(placeholderHTML))
}

const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Workly</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 16px/1.5 system-ui, sans-serif; max-width: 36rem; margin: 4rem auto; padding: 0 1rem; }
  code { font-size: 0.9em; }
</style>
</head>
<body>
  <h1>Workly is running</h1>
  <p>The dashboard hasn't been built into this binary.</p>
  <p>The API is available under <code>/v1</code>. Health: <a href="/healthz"><code>/healthz</code></a>, <a href="/readyz"><code>/readyz</code></a>.</p>
</body>
</html>
`
