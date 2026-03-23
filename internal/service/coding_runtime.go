package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/store"
)

type CodingRuntimeStatus struct {
	SessionID      string
	RuntimeMode    string
	RuntimeStatus  string
	RestartPending bool
	InFlight       bool
	StartedAt      time.Time
}

func normalizeCodingRuntimeMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "persistent":
		return "persistent"
	default:
		return "spawn"
	}
}

func normalizeCodingRuntimeStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "stopped", "starting", "idle", "running", "restart_scheduled", "restarting", "error":
		return strings.TrimSpace(strings.ToLower(status))
	default:
		return "idle"
	}
}

func (s *Service) UpdateCodingSessionRuntimeMode(ctx context.Context, sessionID, runtimeMode string) (storeSession store.CodingSession, err error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return storeSession, fmt.Errorf("session_id is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return storeSession, err
	}
	session.RuntimeMode = normalizeCodingRuntimeMode(runtimeMode)
	session.RuntimeStatus = "idle"
	session.RestartPending = false
	session.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return storeSession, err
	}
	return s.Store.GetCodingSession(ctx, sid)
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
	inFlight, startedAt := s.CodingRunStatus(sid)
	return CodingRuntimeStatus{
		SessionID:      sid,
		RuntimeMode:    normalizeCodingRuntimeMode(session.RuntimeMode),
		RuntimeStatus:  normalizeCodingRuntimeStatus(session.RuntimeStatus),
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
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return false, err
	}
	inFlight, _ := s.CodingRunStatus(sid)
	if inFlight && !force {
		session.RuntimeStatus = "restart_scheduled"
		session.RestartPending = true
		session.UpdatedAt = time.Now().UTC()
		if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
			return false, err
		}
		return true, nil
	}

	if inFlight && force {
		s.StopCodingRun(sid)
	}

	session.RuntimeStatus = "restarting"
	session.RestartPending = false
	session.CodexThreadID = ""
	session.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return false, err
	}
	session.RuntimeStatus = "idle"
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
	session.RuntimeStatus = normalizeCodingRuntimeStatus(status)
	if strings.TrimSpace(session.RuntimeMode) == "" {
		session.RuntimeMode = "spawn"
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
	session.RuntimeStatus = "restarting"
	session.RestartPending = false
	session.CodexThreadID = ""
	session.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return false
	}
	session.RuntimeStatus = "idle"
	session.UpdatedAt = time.Now().UTC()
	_ = s.Store.UpdateCodingSession(ctx, session)
	return true
}
