package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
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
	diskAssetsDir := resolveDiskAssetsDir()
	diskIndexPath := ""
	if diskAssetsDir != "" {
		diskIndexPath = filepath.Join(diskAssetsDir, "index.html")
	}
	serveIndex := func(w http.ResponseWriter) {
		body := indexBytes
		if diskIndexPath != "" {
			if diskBytes, err := os.ReadFile(diskIndexPath); err == nil && len(diskBytes) > 0 {
				body = diskBytes
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
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
			// Dev fallback: serve fresh built assets from disk even if the running
			// binary/embed is stale.
			if strings.HasPrefix(assetPath, "assets/") {
				if diskAssetsDir != "" {
					diskPath := filepath.Join(diskAssetsDir, filepath.FromSlash(assetPath))
					if st, statErr := os.Stat(diskPath); statErr == nil && !st.IsDir() {
						http.ServeFile(w, r, diskPath)
						return
					}
				}
				http.NotFound(w, r)
				return
			}
			// SPA fallback for app routes only.
			if !strings.Contains(path.Base(assetPath), ".") {
				serveIndex(w)
				return
			}
			http.NotFound(w, r)
			return
		}
		if assetPath == "index.html" {
			serveIndex(w)
			return
		}
		fileServer.ServeHTTP(w, &r2)
	})
}

func resolveDiskAssetsDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(wd, "internal", "webui", "assets")
		if st, statErr := os.Stat(candidate); statErr == nil && st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return ""
		}
		wd = parent
	}
}
