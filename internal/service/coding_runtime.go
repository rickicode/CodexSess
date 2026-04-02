package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/provider"
)

type CodingRuntimeStatus struct {
	SessionID      string
	RunnerRole     string
	RestartPending bool
	InFlight       bool
	StartedAt      time.Time
}

func normalizeCodingRunnerRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "chat", "executor":
		return "chat"
	default:
		return ""
	}
}

func (s *Service) CodingRuntimeStatusDetail(ctx context.Context, sessionID string) (CodingRuntimeStatus, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return CodingRuntimeStatus{}, fmt.Errorf("session_id is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return CodingRuntimeStatus{}, err
	}
	inFlight, startedAt, runnerRole := s.CodingRunStatus(sid)
	return CodingRuntimeStatus{
		SessionID:      sid,
		RunnerRole:     normalizeCodingRunnerRole(runnerRole),
		RestartPending: session.RestartPending,
		InFlight:       inFlight,
		StartedAt:      startedAt,
	}, nil
}

func (s *Service) RestartCodingRuntime(ctx context.Context, sessionID string, force bool) (deferred bool, err error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false, fmt.Errorf("session_id is required")
	}
	provider.CloseAppServerClientsUnder(s.codingSessionRuntimeRoot(sid))
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return false, err
	}
	inFlight, _, _ := s.CodingRunStatus(sid)
	if inFlight && !force {
		session.RestartPending = true
		session.UpdatedAt = time.Now().UTC()
		if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
			return false, err
		}
		return true, nil
	}

	if inFlight && force {
		s.StopCodingRun(sid, true)
	}

	session.RestartPending = false
	session.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return false, err
	}
	session.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Service) setCodingRuntimeState(ctx context.Context, sessionID, status string, restartPending *bool) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return
	}
	if restartPending != nil {
		session.RestartPending = *restartPending
	}
	session.UpdatedAt = time.Now().UTC()
	_ = s.Store.UpdateCodingSession(ctx, session)
}

func (s *Service) finalizeDeferredCodingRestart(ctx context.Context, sessionID string) bool {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return false
	}
	if !session.RestartPending {
		return false
	}
	session.RestartPending = false
	session.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return false
	}
	session.UpdatedAt = time.Now().UTC()
	_ = s.Store.UpdateCodingSession(ctx, session)
	return true
}
