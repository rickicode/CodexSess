package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Service) ListCodingSessions(ctx context.Context) ([]store.CodingSession, error) {
	return s.Store.ListCodingSessions(ctx)
}

func (s *Service) CreateCodingSession(ctx context.Context, title, model, reasoningLevel, workDir, sandboxMode string) (store.CodingSession, error) {
	useModel := normalizeCodingModel(model)
	useReasoning := normalizeCodingReasoningLevel(reasoningLevel)
	useWorkDir := normalizeWorkDir(workDir)
	useSandbox := normalizeCodingSandboxMode(sandboxMode)
	resolvedWorkDir, err := expandWorkDir(useWorkDir)
	if err != nil {
		return store.CodingSession{}, err
	}
	if err := ensureCodingProjectAgentsFile(resolvedWorkDir); err != nil {
		return store.CodingSession{}, err
	}
	sessionID := uuid.NewString()
	runtimeHome, _, err := s.ensureCodingRuntimeHome(ctx, sessionID, codingRuntimeRoleChat)
	if err != nil {
		return store.CodingSession{}, err
	}
	cleanupRuntimeHome := true
	defer func() {
		if cleanupRuntimeHome {
			_ = os.RemoveAll(s.codingSessionRuntimeRoot(sessionID))
		}
	}()
	thread, err := s.Codex.AppServerStartThread(ctx, provider.ExecOptions{
		CodexHome:       runtimeHome,
		WorkDir:         resolvedWorkDir,
		Model:           useModel,
		ReasoningEffort: useReasoning,
		SandboxMode:     useSandbox,
		Persist:         true,
		CommandMode:     "chat",
	})
	if err != nil {
		return store.CodingSession{}, err
	}
	threadID := strings.TrimSpace(thread.ThreadID)
	if threadID == "" {
		return store.CodingSession{}, fmt.Errorf("codex app-server did not return a thread id")
	}
	cleanupRuntimeHome = false
	session := store.CodingSession{
		ID:             sessionID,
		Title:          normalizeSessionTitle(title),
		Model:          useModel,
		ReasoningLevel: useReasoning,
		WorkDir:        useWorkDir,
		SandboxMode:    useSandbox,
		CodexThreadID:  threadID,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastMessageAt:  time.Now().UTC(),
	}
	return s.Store.CreateCodingSession(ctx, session)
}

func (s *Service) DeleteCodingSession(ctx context.Context, sessionID string) error {
	sid := strings.TrimSpace(sessionID)
	runtimeRoot := s.codingSessionRuntimeRoot(sid)
	provider.CloseAppServerClientsUnder(runtimeRoot)
	if err := s.Store.DeleteCodingSession(ctx, sid); err != nil {
		return err
	}
	_ = os.RemoveAll(runtimeRoot)
	s.cleanupStaleCodingRuntimeHomes(ctx)
	return nil
}

func (s *Service) UpdateCodingSessionPreferences(ctx context.Context, sessionID, model, reasoningLevel, workDir, sandboxMode string) (store.CodingSession, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return store.CodingSession{}, fmt.Errorf("session_id is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return store.CodingSession{}, err
	}
	session.Model = normalizeCodingModel(firstNonEmpty(model, session.Model))
	session.ReasoningLevel = normalizeCodingReasoningLevel(firstNonEmpty(reasoningLevel, session.ReasoningLevel))
	session.WorkDir = normalizeWorkDir(firstNonEmpty(workDir, session.WorkDir))
	session.SandboxMode = normalizeCodingSandboxMode(firstNonEmpty(sandboxMode, session.SandboxMode))
	session.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return store.CodingSession{}, err
	}
	return s.Store.GetCodingSession(ctx, sid)
}

func (s *Service) GetCodingMessages(ctx context.Context, sessionID string) ([]store.CodingMessage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return s.Store.ListCodingMessages(ctx, sessionID)
}

func (s *Service) GetCodingMessagesPage(ctx context.Context, sessionID string, limit int, beforeID string) ([]store.CodingMessage, bool, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, false, fmt.Errorf("session_id is required")
	}
	if limit < 1 {
		return nil, false, fmt.Errorf("limit must be at least 1")
	}
	if limit > 200 {
		limit = 200
	}
	return s.Store.ListCodingMessagesPage(ctx, sid, limit, strings.TrimSpace(beforeID))
}
