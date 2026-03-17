package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed assets/*
var embedded embed.FS

func Assets() fs.FS {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}

func Handler() http.Handler {
	sub := Assets()
	indexBytes, _ := fs.ReadFile(sub, "index.html")
	fileServer := http.FileServer(http.FS(sub))
	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexBytes)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimSpace(r.URL.Path)
		if p == "" || p == "/" {
			serveIndex(w)
			return
		}
		r2 := *r
		r2.URL.Path = path.Clean(p)
		assetPath := strings.TrimPrefix(r2.URL.Path, "/")
		if _, err := fs.Stat(sub, assetPath); err != nil {
			serveIndex(w)
			return
		}
		fileServer.ServeHTTP(w, &r2)
	})
}
