package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %q: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore working dir: %v", err)
		}
	})
}

func TestHandler_PrefersDiskIndexForRootAndSPAFallback(t *testing.T) {
	embeddedAssets := Assets()
	embeddedIndex, err := fs.ReadFile(embeddedAssets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}

	tempDir := t.TempDir()
	diskAssetsDir := filepath.Join(tempDir, "internal", "webui", "assets")
	if err := os.MkdirAll(diskAssetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}

	diskIndex := "<!doctype html><html><body>disk-index-marker</body></html>"
	if err := os.WriteFile(filepath.Join(diskAssetsDir, "index.html"), []byte(diskIndex), 0o644); err != nil {
		t.Fatalf("write disk index: %v", err)
	}
	withWorkingDir(t, tempDir)

	handler := Handler()

	for _, target := range []string{"/", "/chat"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d want=%d", target, rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "disk-index-marker") {
			t.Fatalf("%s body did not use disk index: %q", target, body)
		}
		if string(embeddedIndex) == body {
			t.Fatalf("%s body unexpectedly matched embedded index", target)
		}
	}
}

func TestHandler_PrefersDiskIndexFromAncestorWorkingDir(t *testing.T) {
	embeddedAssets := Assets()
	embeddedIndex, err := fs.ReadFile(embeddedAssets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}

	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	diskAssetsDir := filepath.Join(projectRoot, "internal", "webui", "assets")
	if err := os.MkdirAll(diskAssetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	runDir := filepath.Join(projectRoot, "cmd", "devserver")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	diskIndex := "<!doctype html><html><body>disk-index-ancestor-marker</body></html>"
	if err := os.WriteFile(filepath.Join(diskAssetsDir, "index.html"), []byte(diskIndex), 0o644); err != nil {
		t.Fatalf("write disk index: %v", err)
	}
	withWorkingDir(t, runDir)

	handler := Handler()

	for _, target := range []string{"/", "/chat", "/index.html"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d want=%d", target, rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "disk-index-ancestor-marker") {
			t.Fatalf("%s body did not use disk index from ancestor path: %q", target, body)
		}
		if string(embeddedIndex) == body {
			t.Fatalf("%s body unexpectedly matched embedded index", target)
		}
	}
}

func TestHandler_PrefersDiskAssetsFromAncestorWorkingDir(t *testing.T) {
	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	diskAssetsDir := filepath.Join(projectRoot, "internal", "webui", "assets", "assets")
	if err := os.MkdirAll(diskAssetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	runDir := filepath.Join(projectRoot, "cmd", "devserver")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	diskAsset := "console.log('disk-asset-ancestor-marker');"
	assetPath := filepath.Join(diskAssetsDir, "index-test.js")
	if err := os.WriteFile(assetPath, []byte(diskAsset), 0o644); err != nil {
		t.Fatalf("write disk asset: %v", err)
	}
	withWorkingDir(t, runDir)

	handler := Handler()

	req := httptest.NewRequest(http.MethodGet, "/assets/index-test.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "disk-asset-ancestor-marker") {
		t.Fatalf("body did not use disk asset from ancestor path: %q", body)
	}
}
