package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCodingProjectAgentsFile_PrependsManagedBlockWhenMissingMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	initial := "# Project Rules\n\nExisting user content.\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := ensureCodingProjectAgentsFile(dir); err != nil {
		t.Fatalf("ensureCodingProjectAgentsFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "<!-- CODEXSESS:START -->") || !strings.Contains(got, codexsessManagedAgentsEndMarker) {
		t.Fatalf("expected CODEXSESS managed block, got %q", got)
	}
	if !strings.Contains(got, initial) {
		t.Fatalf("expected existing content to remain after managed block injection, got %q", got)
	}
	if !strings.HasPrefix(got, "<!-- CODEXSESS:START -->") {
		t.Fatalf("expected managed block at start of file, got %q", got)
	}
}

func TestEnsureCodingProjectAgentsFile_SkipsWhenMarkerAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	initial := "<!-- CODEXSESS:START -->\nmanaged\n" + codexsessManagedAgentsEndMarker + "\n\n# Existing\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := ensureCodingProjectAgentsFile(dir); err != nil {
		t.Fatalf("ensureCodingProjectAgentsFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(raw) != initial {
		t.Fatalf("expected AGENTS.md unchanged when marker already exists, got %q", string(raw))
	}
}

func TestEnsureCodingProjectAgentsFile_CreatesManagedFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	if err := ensureCodingProjectAgentsFile(dir); err != nil {
		t.Fatalf("ensureCodingProjectAgentsFile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "<!-- CODEXSESS:START -->") || !strings.Contains(got, codexsessManagedAgentsEndMarker) {
		t.Fatalf("expected managed AGENTS file, got %q", got)
	}
}
