package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var assets embed.FS

func Handler() http.Handler {
	dist, _ := fs.Sub(assets, "dist")
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := fs.Stat(dist, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, _ := fs.ReadFile(dist, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
