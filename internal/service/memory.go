package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/store"
)

type MemoryPutInput struct {
	Scope      string
	ScopeID    string
	Kind       string
	Key        string
	Value      any
	SourceType string
	SourceRef  string
	Verified   bool
	Confidence int
	Stale      bool
	ExpiresAt  *time.Time
}

func normalizeMemoryScope(scope string) string {
	return strings.ToLower(strings.TrimSpace(scope))
}

func normalizeMemoryKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

func clampMemoryConfidenceValue(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (s *Service) PutMemoryItem(ctx context.Context, input MemoryPutInput) (store.MemoryItem, error) {
	scope := normalizeMemoryScope(input.Scope)
	scopeID := strings.TrimSpace(input.ScopeID)
	kind := normalizeMemoryKind(input.Kind)
	key := strings.TrimSpace(input.Key)
	sourceType := normalizeMemoryKind(input.SourceType)
	sourceRef := strings.TrimSpace(input.SourceRef)
	if scope == "" {
		return store.MemoryItem{}, fmt.Errorf("memory scope is required")
	}
	if kind == "" {
		return store.MemoryItem{}, fmt.Errorf("memory kind is required")
	}
	if key == "" {
		return store.MemoryItem{}, fmt.Errorf("memory key is required")
	}
	if sourceType == "" {
		return store.MemoryItem{}, fmt.Errorf("memory source_type is required")
	}
	rawValue, err := json.Marshal(input.Value)
	if err != nil {
		return store.MemoryItem{}, err
	}
	item := store.MemoryItem{
		ID:         "mem_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Scope:      scope,
		ScopeID:    scopeID,
		Kind:       kind,
		Key:        key,
		ValueJSON:  string(rawValue),
		SourceType: sourceType,
		SourceRef:  sourceRef,
		Verified:   input.Verified,
		Confidence: clampMemoryConfidenceValue(input.Confidence),
		Stale:      input.Stale,
		ExpiresAt:  input.ExpiresAt,
	}
	return s.Store.UpsertMemoryItem(ctx, item)
}

func (s *Service) ListMemoryItems(ctx context.Context, query store.MemoryQuery) ([]store.MemoryItem, error) {
	query.Scope = normalizeMemoryScope(query.Scope)
	query.ScopeID = strings.TrimSpace(query.ScopeID)
	for i := range query.Kinds {
		query.Kinds[i] = normalizeMemoryKind(query.Kinds[i])
	}
	return s.Store.ListMemoryItems(ctx, query)
}

func (s *Service) DeleteMemoryItem(ctx context.Context, memoryID string) error {
	return s.Store.DeleteMemoryItem(ctx, strings.TrimSpace(memoryID))
}

func (s *Service) DeleteMemoryItemsByScope(ctx context.Context, scope, scopeID string) error {
	return s.Store.DeleteMemoryItemsByScope(ctx, normalizeMemoryScope(scope), strings.TrimSpace(scopeID))
}

func (s *Service) PutCodingSessionMemory(ctx context.Context, sessionID, kind, key string, value any, sourceType, sourceRef string, verified bool, confidence int) (store.MemoryItem, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return store.MemoryItem{}, fmt.Errorf("session_id is required")
	}
	if _, err := s.Store.GetCodingSession(ctx, sid); err != nil {
		return store.MemoryItem{}, err
	}
	return s.PutMemoryItem(ctx, MemoryPutInput{
		Scope:      "session",
		ScopeID:    sid,
		Kind:       kind,
		Key:        key,
		Value:      value,
		SourceType: sourceType,
		SourceRef:  sourceRef,
		Verified:   verified,
		Confidence: confidence,
	})
}

func (s *Service) ListCodingSessionMemory(ctx context.Context, sessionID string, kinds []string, verifiedOnly bool) ([]store.MemoryItem, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return s.ListMemoryItems(ctx, store.MemoryQuery{
		Scope:        "session",
		ScopeID:      sid,
		Kinds:        kinds,
		VerifiedOnly: verifiedOnly,
		Limit:        200,
	})
}

func (s *Service) EnsureProjectMemory(ctx context.Context, workDir string) error {
	resolvedWorkDir, err := expandWorkDir(workDir)
	if err != nil {
		return err
	}
	projectScopeID := normalizeWorkDir(resolvedWorkDir)
	if err := s.putProjectMemoryFact(ctx, projectScopeID, "repo.workdir", map[string]any{
		"path": resolvedWorkDir,
	}, "repo_scan", resolvedWorkDir, 100); err != nil {
		return err
	}
	uncodixfyPath := filepath.Join(resolvedWorkDir, "Uncodixfy.md")
	if _, err := os.Stat(uncodixfyPath); err == nil {
		if err := s.putProjectMemoryFact(ctx, projectScopeID, "design.source_of_truth", map[string]any{
			"path": "Uncodixfy.md",
		}, "repo_scan", "Uncodixfy.md", 100); err != nil {
			return err
		}
	}
	goModPath := filepath.Join(resolvedWorkDir, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		if err := s.putProjectMemoryFact(ctx, projectScopeID, "stack.backend", map[string]any{
			"language": "go",
			"tooling":  "go",
		}, "repo_scan", "go.mod", 95); err != nil {
			return err
		}
	}
	makefilePath := filepath.Join(resolvedWorkDir, "Makefile")
	if raw, err := os.ReadFile(makefilePath); err == nil {
		text := string(raw)
		if strings.Contains(text, "\ndev:") || strings.HasPrefix(text, "dev:") {
			if err := s.putProjectMemoryFact(ctx, projectScopeID, "command.dev", map[string]any{
				"command": "make dev",
			}, "repo_scan", "Makefile", 95); err != nil {
				return err
			}
		}
		if strings.Contains(text, "\ntest:") || strings.HasPrefix(text, "test:") {
			if err := s.putProjectMemoryFact(ctx, projectScopeID, "command.test", map[string]any{
				"command": "make test",
			}, "repo_scan", "Makefile", 95); err != nil {
				return err
			}
		}
		if strings.Contains(text, "\nbuild-frontend:") || strings.HasPrefix(text, "build-frontend:") {
			if err := s.putProjectMemoryFact(ctx, projectScopeID, "command.build_frontend", map[string]any{
				"command": "make build-frontend",
			}, "repo_scan", "Makefile", 95); err != nil {
				return err
			}
		}
	}
	packagePath := filepath.Join(resolvedWorkDir, "web", "package.json")
	if raw, err := os.ReadFile(packagePath); err == nil {
		var pkg struct {
			Scripts         map[string]string `json:"scripts"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(raw, &pkg); err == nil {
			if _, ok := pkg.DevDependencies["svelte"]; ok {
				if err := s.putProjectMemoryFact(ctx, projectScopeID, "stack.frontend", map[string]any{
					"framework": "svelte",
					"bundler":   "vite",
				}, "repo_scan", "web/package.json", 95); err != nil {
					return err
				}
			}
			if cmd := strings.TrimSpace(pkg.Scripts["build:web"]); cmd != "" {
				if err := s.putProjectMemoryFact(ctx, projectScopeID, "command.web_build", map[string]any{
					"command": "npm run build:web --prefix web",
				}, "repo_scan", "web/package.json", 95); err != nil {
					return err
				}
			}
			if cmd := strings.TrimSpace(pkg.Scripts["dev"]); cmd != "" {
				if err := s.putProjectMemoryFact(ctx, projectScopeID, "command.web_dev", map[string]any{
					"command": "npm run dev --prefix web",
				}, "repo_scan", "web/package.json", 95); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) putProjectMemoryFact(ctx context.Context, scopeID, key string, value any, sourceType, sourceRef string, confidence int) error {
	_, err := s.PutMemoryItem(ctx, MemoryPutInput{
		Scope:      "project",
		ScopeID:    scopeID,
		Kind:       "fact",
		Key:        key,
		Value:      value,
		SourceType: sourceType,
		SourceRef:  sourceRef,
		Verified:   true,
		Confidence: confidence,
	})
	return err
}
