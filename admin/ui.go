package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:ui/dist
var uiDist embed.FS

func uiRoot() fs.FS {
	sub, err := fs.Sub(uiDist, "ui/dist")
	if err != nil {
		return uiDist
	}
	return sub
}

func (a *Admin) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/dns-query" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	root := uiRoot()
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p != "" && p != "." {
		if info, err := fs.Stat(root, p); err == nil && !info.IsDir() {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			http.ServeFileFS(w, r, root, p)
			return
		}
	}
	b, err := fs.ReadFile(root, "index.html")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"name": "coredns-admin",
			"api":  "/api/v1",
			"doh":  "/dns-query",
			"ui":   "/",
		})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
