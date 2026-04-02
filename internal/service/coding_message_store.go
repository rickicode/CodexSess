package service

import (
	"context"
	"strings"

	"github.com/ricki/codexsess/internal/store"
)

func (s *Service) appendCodingMessage(ctx context.Context, msg store.CodingMessage) (store.CodingMessage, error) {
	if strings.TrimSpace(msg.AccountEmail) == "" {
		msg.AccountEmail = s.resolveCodingMessageAccountEmail(ctx, msg)
	}
	return s.Store.AppendCodingMessage(ctx, msg)
}

func (s *Service) updateCodingMessage(ctx context.Context, msg store.CodingMessage) (store.CodingMessage, error) {
	if strings.TrimSpace(msg.AccountEmail) == "" {
		msg.AccountEmail = s.resolveCodingMessageAccountEmail(ctx, msg)
	}
	return s.Store.UpdateCodingMessage(ctx, msg)
}

func (s *Service) resolveCodingMessageAccountEmail(ctx context.Context, msg store.CodingMessage) string {
	sid := strings.TrimSpace(msg.SessionID)
	if sid == "" {
		return ""
	}
	runtimeRole := normalizeCodingRunnerRole(msg.Actor)
	if runtimeRole == "" {
		runtimeRole = codingRuntimeRoleChat
	}
	if email := s.resolveCodingRuntimeAccountEmail(ctx, sid, runtimeRole); email != "" {
		return email
	}
	if runtimeRole != codingRuntimeRoleChat {
		if email := s.resolveCodingRuntimeAccountEmail(ctx, sid, codingRuntimeRoleChat); email != "" {
			return email
		}
	}
	active, err := s.Store.ActiveCLIAccount(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(active.Email)
}

func (s *Service) resolveCodingRuntimeAccountEmail(ctx context.Context, sessionID, runtimeRole string) string {
	sid := strings.TrimSpace(sessionID)
	role := strings.TrimSpace(runtimeRole)
	if sid == "" || role == "" {
		return ""
	}
	accountID := strings.TrimSpace(s.currentCodingRuntimeAccount(sid, role))
	if accountID == "" {
		return ""
	}
	account, err := s.Store.FindAccountBySelector(ctx, accountID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(account.Email)
}
