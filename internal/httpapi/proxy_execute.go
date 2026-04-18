package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
)

type proxyExecutionSession struct {
	RequestID string
	Model     string
	Stream    bool
	StartedAt time.Time
	Account   store.Account
	Tokens    service.TokenSet
}

type proxyBackendResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
	ToolCalls    []ChatToolCall
}

type proxyProtocolPlan struct {
	RequestID  string
	Selector   string
	Model      string
	Prompt     string
	DirectOpts directCodexRequestOptions
	Stream     bool
}

type proxyProtocolAdapter struct {
	WriteSetupError func(http.ResponseWriter, error)
	WriteStream     func(http.ResponseWriter, *http.Request, *proxyExecutionSession, *int)
	WriteJSON       func(http.ResponseWriter, proxyBackendResult, *int)
	WriteError      func(http.ResponseWriter, error, *int)
}

type proxyPipeline struct {
	Plan    proxyProtocolPlan
	Adapter proxyProtocolAdapter
}

func (s *Server) beginProxyExecution(ctx context.Context, w http.ResponseWriter, selector, requestID, model string, stream bool) (*proxyExecutionSession, error) {
	if s == nil || s.executor == nil {
		return nil, nil
	}
	account, tk, err := s.executor.resolveAPIAccountWithTokens(ctx, selector)
	if err != nil {
		return nil, err
	}
	setResolvedAccountHeaders(w, account)
	return &proxyExecutionSession{
		RequestID: requestID,
		Model:     model,
		Stream:    stream,
		StartedAt: time.Now(),
		Account:   account,
		Tokens:    tk,
	}, nil
}

func (s *Server) finishProxyExecution(ctx context.Context, exec *proxyExecutionSession, status int) {
	if s == nil || s.svc == nil || s.svc.Store == nil || exec == nil {
		return
	}
	_ = s.svc.Store.InsertAudit(ctx, store.AuditRecord{
		RequestID: exec.RequestID,
		AccountID: exec.Account.ID,
		Model:     exec.Model,
		Stream:    exec.Stream,
		Status:    status,
		LatencyMS: time.Since(exec.StartedAt).Milliseconds(),
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Server) executeProxyProtocol(w http.ResponseWriter, r *http.Request, pipeline proxyPipeline) {
	if s == nil {
		return
	}
	plan := pipeline.Plan
	adapter := pipeline.Adapter
	exec, err := s.beginProxyExecution(r.Context(), w, plan.Selector, plan.RequestID, plan.Model, plan.Stream)
	if err != nil {
		if adapter.WriteSetupError != nil {
			adapter.WriteSetupError(w, err)
		}
		return
	}
	status := 200
	defer s.finishProxyExecution(r.Context(), exec, status)

	if plan.Stream {
		if adapter.WriteStream != nil {
			adapter.WriteStream(w, r, exec, &status)
		}
		return
	}

	result, err := s.executor.executeRequest(r.Context(), exec, plan.Prompt, plan.DirectOpts)
	if err != nil {
		if adapter.WriteError != nil {
			adapter.WriteError(w, err, &status)
		}
		return
	}
	if adapter.WriteJSON != nil {
		adapter.WriteJSON(w, result, &status)
	}
}
