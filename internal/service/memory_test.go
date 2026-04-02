package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ricki/codexsess/internal/store"
)

func TestPutCodingSessionMemoryPersistsStructuredItem(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_memory")

	item, err := svc.PutCodingSessionMemory(
		t.Context(),
		"sess_memory",
		"constraint",
		"active_goal",
		map[string]any{"text": "finish backend MVP"},
		"user",
		"msg_1",
		true,
		92,
	)
	if err != nil {
		t.Fatalf("PutCodingSessionMemory: %v", err)
	}
	if item.Scope != "session" || item.ScopeID != "sess_memory" {
		t.Fatalf("unexpected memory scope: %#v", item)
	}
	if item.SourceType != "user" || item.SourceRef != "msg_1" {
		t.Fatalf("unexpected source metadata: %#v", item)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(item.ValueJSON), &payload); err != nil {
		t.Fatalf("unmarshal value_json: %v", err)
	}
	if payload["text"] != "finish backend MVP" {
		t.Fatalf("unexpected memory payload: %#v", payload)
	}
}

func TestListCodingSessionMemoryFiltersVerifiedOnly(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_memory_filters")

	_, err := svc.PutCodingSessionMemory(t.Context(), "sess_memory_filters", "fact", "repo_stack", map[string]any{"name": "go"}, "repo_scan", "scan_1", true, 85)
	if err != nil {
		t.Fatalf("put verified memory: %v", err)
	}
	_, err = svc.PutCodingSessionMemory(t.Context(), "sess_memory_filters", "fact", "guess_stack", map[string]any{"name": "unknown"}, "runtime_observation", "run_1", false, 30)
	if err != nil {
		t.Fatalf("put unverified memory: %v", err)
	}

	verifiedOnly, err := svc.ListCodingSessionMemory(t.Context(), "sess_memory_filters", []string{"fact"}, true)
	if err != nil {
		t.Fatalf("ListCodingSessionMemory: %v", err)
	}
	if len(verifiedOnly) != 1 {
		t.Fatalf("expected one verified fact, got %d", len(verifiedOnly))
	}
	if verifiedOnly[0].Key != "repo_stack" {
		t.Fatalf("expected verified key repo_stack, got %q", verifiedOnly[0].Key)
	}
}

func TestDeleteMemoryItemsByScopeRemovesSessionMemory(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_memory_delete")

	_, err := svc.PutCodingSessionMemory(t.Context(), "sess_memory_delete", "constraint", "goal", map[string]any{"text": "ship"}, "user", "msg_1", true, 80)
	if err != nil {
		t.Fatalf("put memory: %v", err)
	}
	if err := svc.DeleteMemoryItemsByScope(t.Context(), "session", "sess_memory_delete"); err != nil {
		t.Fatalf("DeleteMemoryItemsByScope: %v", err)
	}
	items, err := svc.ListMemoryItems(t.Context(), store.MemoryQuery{
		Scope:   "session",
		ScopeID: "sess_memory_delete",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("ListMemoryItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected session memory to be deleted, got %d", len(items))
	}
}

func TestEnsureProjectMemoryExtractsVerifiedRepoFacts(t *testing.T) {
	svc, _, _, _ := newCodingTestService(t)

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "Uncodixfy.md"), []byte("# UI source\n"), 0o644); err != nil {
		t.Fatalf("write Uncodixfy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "Makefile"), []byte("dev:\n\t@echo dev\n\ntest:\n\tgo test ./...\n\nbuild-frontend:\n\tcd web && npm run build:web\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "web"), 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "web", "package.json"), []byte(`{
  "scripts": {"dev":"vite","build:web":"vite build"},
  "devDependencies": {"svelte":"^5.0.0","vite":"^8.0.0"}
}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	if err := svc.EnsureProjectMemory(t.Context(), workDir); err != nil {
		t.Fatalf("EnsureProjectMemory: %v", err)
	}

	items, err := svc.ListMemoryItems(t.Context(), store.MemoryQuery{
		Scope:        "project",
		ScopeID:      workDir,
		VerifiedOnly: true,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("ListMemoryItems: %v", err)
	}
	if len(items) < 5 {
		t.Fatalf("expected extracted project memory facts, got %#v", items)
	}
	keys := map[string]bool{}
	for _, item := range items {
		keys[item.Key] = true
	}
	for _, want := range []string{
		"design.source_of_truth",
		"stack.backend",
		"stack.frontend",
		"command.test",
		"command.web_build",
	} {
		if !keys[want] {
			t.Fatalf("expected project memory key %q, got %#v", want, keys)
		}
	}
}
