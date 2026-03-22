package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

const (
	codingCLIUsageMinPercent   = 20
	codingUsageFreshnessTTL    = 5 * time.Minute
	codingEventPersistMax      = 240
	codingEventContentMaxRunes = 6000
)

var ErrCodingSessionBusy = errors.New("coding session is already processing")

type CodingChatResult struct {
	Session       store.CodingSession
	User          store.CodingMessage
	Assistant     store.CodingMessage
	Assistants    []store.CodingMessage
	EventMessages []store.CodingMessage
}

func (s *Service) ListCodingSessions(ctx context.Context) ([]store.CodingSession, error) {
	return s.Store.ListCodingSessions(ctx)
}

func (s *Service) CreateCodingSession(ctx context.Context, title, model, workDir, sandboxMode string) (store.CodingSession, error) {
	session := store.CodingSession{
		ID:            "sess_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Title:         normalizeSessionTitle(title),
		Model:         normalizeCodingModel(model),
		WorkDir:       normalizeWorkDir(workDir),
		SandboxMode:   normalizeCodingSandboxMode(sandboxMode),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		LastMessageAt: time.Now().UTC(),
	}
	return s.Store.CreateCodingSession(ctx, session)
}

func (s *Service) DeleteCodingSession(ctx context.Context, sessionID string) error {
	return s.Store.DeleteCodingSession(ctx, strings.TrimSpace(sessionID))
}

func (s *Service) UpdateCodingSessionPreferences(ctx context.Context, sessionID, model, workDir, sandboxMode string) (store.CodingSession, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return store.CodingSession{}, fmt.Errorf("session_id is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return store.CodingSession{}, err
	}
	session.Model = normalizeCodingModel(firstNonEmpty(model, session.Model))
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

func (s *Service) SendCodingMessage(ctx context.Context, sessionID, content, model, workDirOverride, sandboxModeOverride, command string) (CodingChatResult, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return CodingChatResult{}, fmt.Errorf("session_id is required")
	}
	releaseRun, err := s.beginCodingRun(sid)
	if err != nil {
		return CodingChatResult{}, err
	}
	defer releaseRun()
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	s.setCodingRunCancel(sid, runCancel)
	commandMode := normalizeCodingCommandMode(command)
	trimmedContent := strings.TrimSpace(content)
	promptInput, userVisibleContent := resolveCommandContent(commandMode, trimmedContent)
	if commandMode == "chat" && promptInput == "" {
		return CodingChatResult{}, fmt.Errorf("message content is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return CodingChatResult{}, err
	}
	useModel := normalizeCodingModel(firstNonEmpty(model, session.Model))
	useWorkDir := normalizeWorkDir(firstNonEmpty(workDirOverride, session.WorkDir))
	useSandboxMode := normalizeCodingSandboxMode(firstNonEmpty(sandboxModeOverride, session.SandboxMode))
	resolvedWorkDir, err := expandWorkDir(useWorkDir)
	if err != nil {
		return CodingChatResult{}, err
	}
	if commandMode == "chat" && isStatusSlashCommand(promptInput) {
		return s.handleLocalStatusCommand(ctx, session, useModel, useWorkDir, useSandboxMode, userVisibleContent, nil)
	}
	if commandMode == "chat" && isMCPSlashCommand(promptInput) {
		return s.handleLocalMCPCommand(ctx, session, useModel, useWorkDir, useSandboxMode, userVisibleContent, nil)
	}

	prompt := buildCodingPrompt(commandMode, promptInput)
	resumeID := ""
	if commandMode == "chat" {
		resumeID = strings.TrimSpace(session.CodexThreadID)
	}
	if commandMode == "chat" && resumeID == "" && !isRawSlashCommand(promptInput) && shouldWrapCodingPrompt() {
		history, err := s.Store.ListCodingMessages(ctx, sid)
		if err != nil {
			return CodingChatResult{}, err
		}
		prompt = buildContextHygienePrompt(buildSessionPromptWithIncoming(history, promptInput))
	}
	s.codingExecMu.Lock()
	_ = s.ensureCodingCLIAccountForCoding(ctx)
	codexHome := strings.TrimSpace(s.Cfg.CodexHome)

	reply, err := s.Codex.ChatWithOptions(runCtx, provider.ExecOptions{
		CodexHome:   codexHome,
		WorkDir:     resolvedWorkDir,
		Model:       useModel,
		Prompt:      prompt,
		ResumeID:    resumeID,
		Persist:     true,
		SandboxMode: useSandboxMode,
		CommandMode: commandMode,
	})
	if err != nil && resumeID != "" && shouldStartNewThreadFromResumeError(err) {
		reply, err = s.Codex.ChatWithOptions(runCtx, provider.ExecOptions{
			CodexHome:   codexHome,
			WorkDir:     resolvedWorkDir,
			Model:       useModel,
			Prompt:      buildCodingPrompt(commandMode, promptInput),
			Persist:     true,
			SandboxMode: useSandboxMode,
			CommandMode: commandMode,
		})
	}
	s.codingExecMu.Unlock()
	if err != nil {
		return CodingChatResult{}, err
	}

	userMsg, err := s.Store.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: sid,
		Role:      "user",
		Content:   userVisibleContent,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}
	assistantParts := normalizedAssistantParts(reply.Messages, reply.Text)
	if len(assistantParts) == 0 {
		return CodingChatResult{}, fmt.Errorf("empty response from codex")
	}
	assistants := make([]store.CodingMessage, 0, len(assistantParts))
	for idx, part := range assistantParts {
		msg := store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: sid,
			Role:      "assistant",
			Content:   part,
			CreatedAt: time.Now().UTC(),
		}
		if idx == len(assistantParts)-1 {
			msg.InputTokens = reply.InputTokens
			msg.OutputTokens = reply.OutputTokens
		}
		saved, err := s.Store.AppendCodingMessage(ctx, msg)
		if err != nil {
			return CodingChatResult{}, err
		}
		assistants = append(assistants, saved)
	}
	assistantMsg := assistants[len(assistants)-1]

	session.Model = useModel
	session.WorkDir = useWorkDir
	session.SandboxMode = useSandboxMode
	if tid := strings.TrimSpace(reply.ThreadID); tid != "" {
		session.CodexThreadID = tid
	}
	session.UpdatedAt = time.Now().UTC()
	session.LastMessageAt = session.UpdatedAt
	if strings.EqualFold(strings.TrimSpace(session.Title), "new session") {
		session.Title = deriveSessionTitle(userVisibleContent)
	}
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return CodingChatResult{}, err
	}
	updatedSession, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return CodingChatResult{}, err
	}
	return CodingChatResult{
		Session:    updatedSession,
		User:       userMsg,
		Assistant:  assistantMsg,
		Assistants: assistants,
	}, nil
}

func (s *Service) SendCodingMessageStream(
	ctx context.Context,
	sessionID,
	content,
	model,
	workDirOverride string,
	sandboxModeOverride string,
	command string,
	onEvent func(provider.ChatEvent) error,
) (CodingChatResult, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return CodingChatResult{}, fmt.Errorf("session_id is required")
	}
	releaseRun, err := s.beginCodingRun(sid)
	if err != nil {
		return CodingChatResult{}, err
	}
	defer releaseRun()
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	s.setCodingRunCancel(sid, runCancel)
	commandMode := normalizeCodingCommandMode(command)
	trimmedContent := strings.TrimSpace(content)
	promptInput, userVisibleContent := resolveCommandContent(commandMode, trimmedContent)
	if commandMode == "chat" && promptInput == "" {
		return CodingChatResult{}, fmt.Errorf("message content is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return CodingChatResult{}, err
	}
	useModel := normalizeCodingModel(firstNonEmpty(model, session.Model))
	useWorkDir := normalizeWorkDir(firstNonEmpty(workDirOverride, session.WorkDir))
	useSandboxMode := normalizeCodingSandboxMode(firstNonEmpty(sandboxModeOverride, session.SandboxMode))
	resolvedWorkDir, err := expandWorkDir(useWorkDir)
	if err != nil {
		return CodingChatResult{}, err
	}
	if commandMode == "chat" && isStatusSlashCommand(promptInput) {
		return s.handleLocalStatusCommand(ctx, session, useModel, useWorkDir, useSandboxMode, userVisibleContent, onEvent)
	}
	if commandMode == "chat" && isMCPSlashCommand(promptInput) {
		return s.handleLocalMCPCommand(ctx, session, useModel, useWorkDir, useSandboxMode, userVisibleContent, onEvent)
	}
	userMsg, err := s.Store.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: sid,
		Role:      "user",
		Content:   userVisibleContent,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}

	prompt := buildCodingPrompt(commandMode, promptInput)
	resumeID := ""
	if commandMode == "chat" {
		resumeID = strings.TrimSpace(session.CodexThreadID)
	}
	if commandMode == "chat" && resumeID == "" && !isRawSlashCommand(promptInput) && shouldWrapCodingPrompt() {
		history, err := s.Store.ListCodingMessages(ctx, sid)
		if err != nil {
			return CodingChatResult{}, err
		}
		prompt = buildContextHygienePrompt(buildSessionPromptWithIncoming(history, promptInput))
	}
	s.codingExecMu.Lock()
	_ = s.ensureCodingCLIAccountForCoding(ctx)
	codexHome := strings.TrimSpace(s.Cfg.CodexHome)

	streamedParts := make([]string, 0, 4)
	persistedEvents := make([]store.CodingMessage, 0, codingEventPersistMax+1)
	droppedEvents := 0
	reply, err := s.Codex.StreamChatWithOptions(runCtx, provider.ExecOptions{
		CodexHome:   codexHome,
		WorkDir:     resolvedWorkDir,
		Model:       useModel,
		Prompt:      prompt,
		ResumeID:    resumeID,
		Persist:     true,
		SandboxMode: useSandboxMode,
		CommandMode: commandMode,
	}, func(evt provider.ChatEvent) error {
		eventType := strings.TrimSpace(strings.ToLower(evt.Type))
		if eventType != "delta" &&
			eventType != "assistant_message" &&
			eventType != "activity" &&
			eventType != "raw_event" &&
			eventType != "stderr" {
			return nil
		}
		delta := evt.Text
		if delta == "" {
			return nil
		}
		if eventType == "assistant_message" {
			streamedParts = append(streamedParts, delta)
		}
			if role := roleFromCodingStreamEvent(eventType); role != "" {
				item := store.CodingMessage{
					ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
					SessionID: sid,
					Role:      role,
					Content:   truncateRunes(delta, codingEventContentMaxRunes),
					CreatedAt: time.Now().UTC(),
				}
				if len(persistedEvents) >= codingEventPersistMax {
					droppedEvents++
				} else {
					saved, saveErr := s.Store.AppendCodingMessage(runCtx, item)
					if saveErr != nil {
						return saveErr
					}
					persistedEvents = append(persistedEvents, saved)
				}
			}
		if onEvent == nil {
			return nil
		}
		return onEvent(provider.ChatEvent{Type: eventType, Text: delta})
	})
	if err != nil && resumeID != "" && shouldStartNewThreadFromResumeError(err) {
		reply, err = s.Codex.StreamChatWithOptions(runCtx, provider.ExecOptions{
			CodexHome:   codexHome,
			WorkDir:     resolvedWorkDir,
			Model:       useModel,
			Prompt:      buildCodingPrompt(commandMode, promptInput),
			Persist:     true,
			SandboxMode: useSandboxMode,
			CommandMode: commandMode,
		}, func(evt provider.ChatEvent) error {
			if onEvent == nil {
				return nil
			}
			return onEvent(evt)
		})
	}
	s.codingExecMu.Unlock()
	if err != nil {
		return CodingChatResult{}, err
	}

	assistantParts := normalizedAssistantParts(reply.Messages, reply.Text)
	if len(assistantParts) == 0 && len(streamedParts) > 0 {
		assistantParts = normalizedAssistantParts(streamedParts, "")
	}
	if len(assistantParts) == 0 {
		return CodingChatResult{}, fmt.Errorf("empty response from codex")
	}

	if droppedEvents > 0 {
		saved, saveErr := s.Store.AppendCodingMessage(runCtx, store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: sid,
			Role:      "activity",
			Content:   fmt.Sprintf("Event log truncated: %d additional entries omitted.", droppedEvents),
			CreatedAt: time.Now().UTC(),
		})
		if saveErr != nil {
			return CodingChatResult{}, saveErr
		}
		persistedEvents = append(persistedEvents, saved)
	}
	assistants := make([]store.CodingMessage, 0, len(assistantParts))
	for idx, part := range assistantParts {
		msg := store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: sid,
			Role:      "assistant",
			Content:   part,
			CreatedAt: time.Now().UTC(),
		}
		if idx == len(assistantParts)-1 {
			msg.InputTokens = reply.InputTokens
			msg.OutputTokens = reply.OutputTokens
		}
		saved, err := s.Store.AppendCodingMessage(ctx, msg)
		if err != nil {
			return CodingChatResult{}, err
		}
		assistants = append(assistants, saved)
	}
	assistantMsg := assistants[len(assistants)-1]

	session.Model = useModel
	session.WorkDir = useWorkDir
	session.SandboxMode = useSandboxMode
	if tid := strings.TrimSpace(reply.ThreadID); tid != "" {
		session.CodexThreadID = tid
	}
	session.UpdatedAt = time.Now().UTC()
	session.LastMessageAt = session.UpdatedAt
	if strings.EqualFold(strings.TrimSpace(session.Title), "new session") {
		session.Title = deriveSessionTitle(userVisibleContent)
	}
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return CodingChatResult{}, err
	}
	updatedSession, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return CodingChatResult{}, err
	}
	return CodingChatResult{
		Session:       updatedSession,
		User:          userMsg,
		Assistant:     assistantMsg,
		Assistants:    assistants,
		EventMessages: persistedEvents,
	}, nil
}

func (s *Service) beginCodingRun(sessionID string) (func(), error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return func() {}, fmt.Errorf("session_id is required")
	}
	now := time.Now().UTC()
	s.codingRunMu.Lock()
	if _, exists := s.codingRuns[sid]; exists {
		s.codingRunMu.Unlock()
		return nil, ErrCodingSessionBusy
	}
	s.codingRuns[sid] = &codingRunState{startedAt: now}
	s.codingRunMu.Unlock()
	released := false
	return func() {
		s.codingRunMu.Lock()
		if !released {
			delete(s.codingRuns, sid)
			released = true
		}
		s.codingRunMu.Unlock()
	}, nil
}

func (s *Service) CodingRunStatus(sessionID string) (bool, time.Time) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false, time.Time{}
	}
	s.codingRunMu.Lock()
	runState, ok := s.codingRuns[sid]
	s.codingRunMu.Unlock()
	if !ok || runState == nil {
		return false, time.Time{}
	}
	return true, runState.startedAt
}

func (s *Service) StopCodingRun(sessionID string) bool {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false
	}
	s.codingRunMu.Lock()
	runState := s.codingRuns[sid]
	cancel := context.CancelFunc(nil)
	if runState != nil {
		cancel = runState.cancel
	}
	s.codingRunMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Service) setCodingRunCancel(sessionID string, cancel context.CancelFunc) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	s.codingRunMu.Lock()
	defer s.codingRunMu.Unlock()
	runState := s.codingRuns[sid]
	if runState == nil {
		return
	}
	runState.cancel = cancel
}

func roleFromCodingStreamEvent(eventType string) string {
	switch strings.TrimSpace(strings.ToLower(eventType)) {
	case "activity":
		return "activity"
	case "raw_event":
		return "event"
	case "stderr":
		return "stderr"
	default:
		return ""
	}
}

func compactCodingEventMessages(items []store.CodingMessage) []store.CodingMessage {
	if len(items) == 0 {
		return nil
	}
	out := make([]store.CodingMessage, 0, minInt(len(items), codingEventPersistMax))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		item.Content = truncateRunes(content, codingEventContentMaxRunes)
		out = append(out, item)
		if len(out) >= codingEventPersistMax {
			break
		}
	}
	dropped := len(items) - len(out)
	if dropped > 0 {
		out = append(out, store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: firstSessionID(out),
			Role:      "activity",
			Content:   fmt.Sprintf("Event log truncated: %d additional entries omitted.", dropped),
			CreatedAt: time.Now().UTC(),
		})
	}
	return out
}

func firstSessionID(items []store.CodingMessage) string {
	for _, item := range items {
		if strings.TrimSpace(item.SessionID) != "" {
			return strings.TrimSpace(item.SessionID)
		}
	}
	return ""
}

func truncateRunes(v string, maxRunes int) string {
	s := strings.TrimSpace(v)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildContextHygienePrompt(userPrompt string) string {
	trimmed := strings.TrimSpace(userPrompt)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Context Hygiene Rules:\n")
	b.WriteString("1. Keep only context relevant to current user request.\n")
	b.WriteString("2. Ignore stale, unrelated, or superseded context from earlier turns.\n")
	b.WriteString("3. If previous context conflicts with the latest user request, prioritize the latest request.\n")
	b.WriteString("4. Answer concisely and avoid repeating old context unless needed.\n\n")
	b.WriteString(trimmed)
	return b.String()
}

func buildCodingPrompt(commandMode, content string) string {
	trimmed := strings.TrimSpace(content)
	if commandMode == "review" {
		return trimmed
	}
	if isRawSlashCommand(trimmed) {
		return trimmed
	}
	if !shouldWrapCodingPrompt() {
		return trimmed
	}
	return buildContextHygienePrompt(trimmed)
}

func shouldWrapCodingPrompt() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_PROMPT_WRAP")))
	if raw == "" {
		return false
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func resolveCommandContent(commandMode, rawContent string) (promptInput string, userVisibleContent string) {
	trimmed := strings.TrimSpace(rawContent)
	if commandMode != "review" {
		return trimmed, trimmed
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "/review") {
		rest := strings.TrimSpace(trimmed[len("/review"):])
		return rest, firstNonEmpty(trimmed, "/review")
	}
	return trimmed, firstNonEmpty(trimmed, "/review")
}

func shouldStartNewThreadFromResumeError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	hints := []string{
		"session not found",
		"thread not found",
		"unknown session",
		"invalid session",
		"no such session",
		"resume failed",
	}
	for _, hint := range hints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func normalizedAssistantParts(messages []string, fallback string) []string {
	parts := make([]string, 0, len(messages)+1)
	for _, item := range messages {
		text := strings.TrimSpace(item)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) > 0 {
		return parts
	}
	text := strings.TrimSpace(fallback)
	if text == "" {
		return nil
	}
	return []string{text}
}

func isStatusSlashCommand(prompt string) bool {
	raw := strings.TrimSpace(strings.ToLower(prompt))
	return raw == "/status" || strings.HasPrefix(raw, "/status ")
}

func isMCPSlashCommand(prompt string) bool {
	raw := strings.TrimSpace(strings.ToLower(prompt))
	return raw == "/mcp" || strings.HasPrefix(raw, "/mcp ")
}

func isRawSlashCommand(prompt string) bool {
	raw := strings.TrimSpace(prompt)
	if raw == "" {
		return false
	}
	if !strings.HasPrefix(raw, "/") {
		return false
	}
	return !strings.HasPrefix(strings.ToLower(raw), "/review")
}

func (s *Service) handleLocalStatusCommand(
	ctx context.Context,
	session store.CodingSession,
	model string,
	workDir string,
	sandboxMode string,
	userVisibleContent string,
	onEvent func(provider.ChatEvent) error,
) (CodingChatResult, error) {
	statusText := s.buildLocalStatusText(ctx, session, model, workDir, sandboxMode)
	if onEvent != nil {
		if err := onEvent(provider.ChatEvent{Type: "assistant_message", Text: statusText}); err != nil {
			return CodingChatResult{}, err
		}
	}

	userMsg, err := s.Store.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: session.ID,
		Role:      "user",
		Content:   firstNonEmpty(strings.TrimSpace(userVisibleContent), "/status"),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}
	assistantMsg, err := s.Store.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: session.ID,
		Role:      "assistant",
		Content:   statusText,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}

	session.Model = normalizeCodingModel(firstNonEmpty(model, session.Model))
	session.WorkDir = normalizeWorkDir(firstNonEmpty(workDir, session.WorkDir))
	session.SandboxMode = normalizeCodingSandboxMode(firstNonEmpty(sandboxMode, session.SandboxMode))
	session.UpdatedAt = time.Now().UTC()
	session.LastMessageAt = session.UpdatedAt
	if strings.EqualFold(strings.TrimSpace(session.Title), "new session") {
		session.Title = "Status"
	}
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return CodingChatResult{}, err
	}
	updatedSession, err := s.Store.GetCodingSession(ctx, session.ID)
	if err != nil {
		return CodingChatResult{}, err
	}
	return CodingChatResult{
		Session:    updatedSession,
		User:       userMsg,
		Assistant:  assistantMsg,
		Assistants: []store.CodingMessage{assistantMsg},
	}, nil
}

func (s *Service) handleLocalMCPCommand(
	ctx context.Context,
	session store.CodingSession,
	model string,
	workDir string,
	sandboxMode string,
	userVisibleContent string,
	onEvent func(provider.ChatEvent) error,
) (CodingChatResult, error) {
	mcpText, err := s.buildLocalMCPText(ctx)
	if err != nil {
		mcpText = "Failed to load MCP list: " + strings.TrimSpace(err.Error())
	}
	if onEvent != nil {
		if err := onEvent(provider.ChatEvent{Type: "assistant_message", Text: mcpText}); err != nil {
			return CodingChatResult{}, err
		}
	}
	return s.persistLocalCommandResponse(ctx, session, model, workDir, sandboxMode, userVisibleContent, mcpText, "MCP")
}

func (s *Service) persistLocalCommandResponse(
	ctx context.Context,
	session store.CodingSession,
	model string,
	workDir string,
	sandboxMode string,
	userVisibleContent string,
	assistantText string,
	defaultTitle string,
) (CodingChatResult, error) {
	userMsg, err := s.Store.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: session.ID,
		Role:      "user",
		Content:   strings.TrimSpace(userVisibleContent),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}
	assistantMsg, err := s.Store.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: session.ID,
		Role:      "assistant",
		Content:   strings.TrimSpace(assistantText),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}
	session.Model = normalizeCodingModel(firstNonEmpty(model, session.Model))
	session.WorkDir = normalizeWorkDir(firstNonEmpty(workDir, session.WorkDir))
	session.SandboxMode = normalizeCodingSandboxMode(firstNonEmpty(sandboxMode, session.SandboxMode))
	session.UpdatedAt = time.Now().UTC()
	session.LastMessageAt = session.UpdatedAt
	if strings.EqualFold(strings.TrimSpace(session.Title), "new session") {
		session.Title = strings.TrimSpace(defaultTitle)
	}
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return CodingChatResult{}, err
	}
	updatedSession, err := s.Store.GetCodingSession(ctx, session.ID)
	if err != nil {
		return CodingChatResult{}, err
	}
	return CodingChatResult{
		Session:    updatedSession,
		User:       userMsg,
		Assistant:  assistantMsg,
		Assistants: []store.CodingMessage{assistantMsg},
	}, nil
}

func (s *Service) buildLocalStatusText(ctx context.Context, session store.CodingSession, model, workDir, sandboxMode string) string {
	apiActive := "-"
	cliActive := "-"
	if accounts, err := s.Store.ListAccounts(ctx); err == nil {
		for _, acc := range accounts {
			if acc.Active {
				apiActive = firstNonEmpty(strings.TrimSpace(acc.Email), strings.TrimSpace(acc.ID), "-")
				break
			}
		}
		if cliID, err := s.ActiveCLIAccountID(ctx); err == nil && strings.TrimSpace(cliID) != "" {
			for _, acc := range accounts {
				if strings.TrimSpace(acc.ID) == strings.TrimSpace(cliID) {
					cliActive = firstNonEmpty(strings.TrimSpace(acc.Email), strings.TrimSpace(acc.ID), "-")
					break
				}
			}
		}
	}

	threadID := firstNonEmpty(strings.TrimSpace(session.CodexThreadID), "-")
	var b strings.Builder
	b.WriteString("CodexSess Status\n")
	b.WriteString("\n")
	b.WriteString("API Active: ")
	b.WriteString(apiActive)
	b.WriteString("\n")
	b.WriteString("CLI Active: ")
	b.WriteString(cliActive)
	b.WriteString("\n")
	b.WriteString("Model: ")
	b.WriteString(normalizeCodingModel(model))
	b.WriteString("\n")
	b.WriteString("Workspace: ")
	b.WriteString(normalizeWorkDir(workDir))
	b.WriteString("\n")
	b.WriteString("Sandbox: ")
	b.WriteString(normalizeCodingSandboxMode(sandboxMode))
	b.WriteString("\n")
	b.WriteString("Thread: ")
	b.WriteString(threadID)
	return b.String()
}

func (s *Service) buildLocalMCPText(ctx context.Context) (string, error) {
	bin := strings.TrimSpace(s.Cfg.CodexBin)
	if bin == "" {
		bin = "codex"
	}
	runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, "mcp", "list")
	if home := strings.TrimSpace(s.Cfg.CodexHome); home != "" {
		cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	}
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(err.Error())
		}
		return "", fmt.Errorf("%s", msg)
	}
	rawLines := strings.Split(out.String(), "\n")
	enabled := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "WARNING:") {
			continue
		}
		if strings.HasPrefix(trimmed, "Name") {
			continue
		}
		if strings.Trim(trimmed, "-") == "" {
			continue
		}
		flat := " " + strings.ToLower(trimmed) + " "
		if strings.Contains(flat, " enabled ") {
			enabled = append(enabled, trimmed)
		}
	}
	if len(enabled) == 0 {
		return "No active MCP servers found.", nil
	}
	var b strings.Builder
	b.WriteString("Active MCP servers:\n")
	for _, line := range enabled {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func normalizeCodingModel(model string) string {
	clean := strings.TrimSpace(model)
	if clean == "" {
		return "gpt-5.2-codex"
	}
	return clean
}

func normalizeWorkDir(workDir string) string {
	clean := strings.TrimSpace(workDir)
	if clean == "" {
		return "~/"
	}
	return clean
}

func normalizeCodingSandboxMode(v string) string {
	mode := strings.TrimSpace(strings.ToLower(v))
	switch mode {
	case "write", "workspace-write":
		return "workspace-write"
	case "full-access", "danger-full-access", "full":
		return "full-access"
	default:
		return "workspace-write"
	}
}

func normalizeCodingCommandMode(v string) string {
	mode := strings.TrimSpace(strings.ToLower(v))
	switch mode {
	case "review":
		return "review"
	default:
		return "chat"
	}
}

func normalizeSessionTitle(title string) string {
	clean := strings.TrimSpace(title)
	if clean == "" {
		return "New Session"
	}
	return clean
}

func deriveSessionTitle(firstUserMessage string) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(firstUserMessage)), " ")
	if clean == "" {
		return "New Session"
	}
	runes := []rune(clean)
	if len(runes) <= 48 {
		return clean
	}
	return strings.TrimSpace(string(runes[:48])) + "..."
}

func (s *Service) ensureCodingCLIAccountForCoding(ctx context.Context) error {
	currentID, _ := s.ActiveCLIAccountID(ctx)
	currentID = strings.TrimSpace(currentID)
	if currentID == "" {
		return nil
	}

	accounts, err := s.ListAccounts(ctx)
	if err != nil || len(accounts) == 0 {
		return err
	}
	usageMap, _ := s.Store.ListUsageSnapshots(ctx)
	now := time.Now().UTC()

	type candidate struct {
		account store.Account
		score   int
	}
	candidates := make([]candidate, 0, len(accounts))
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" {
			continue
		}
		usage, ok := usageMap[id]
		needsRefresh := !ok || strings.TrimSpace(usage.LastError) != "" || usage.FetchedAt.IsZero() || now.Sub(usage.FetchedAt) > codingUsageFreshnessTTL
		if needsRefresh {
			refreshed, refreshErr := s.RefreshUsage(ctx, id)
			if refreshErr != nil {
				continue
			}
			usage = refreshed
			ok = true
		}
		if !ok {
			continue
		}
		score := codingUsageScore(usage)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, candidate{
			account: account,
			score:   score,
		})
	}
	if len(candidates) == 0 {
		return nil
	}

	currentScore := -1
	for _, item := range candidates {
		if strings.TrimSpace(item.account.ID) == currentID {
			currentScore = item.score
			break
		}
	}
	strategy := config.NormalizeCodingCLIStrategy(s.Cfg.CodingCLIStrategy)
	if strategy == "round_robin" {
		// Round-robin rotation is driven by scheduler (~5 minutes), not per-chat request.
		return nil
	}
	threshold := s.Cfg.UsageAutoSwitchThreshold
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 100 {
		threshold = 100
	}
	if currentScore >= threshold {
		return nil
	}

	selectCandidate := func(minScore int, requireBetterThanCurrent bool) (store.Account, bool) {
		start := -1
		for idx, item := range candidates {
			if strings.TrimSpace(item.account.ID) == currentID {
				start = idx
				break
			}
		}
		for step := 1; step <= len(candidates); step++ {
			idx := (start + step + len(candidates)) % len(candidates)
			item := candidates[idx]
			if strings.TrimSpace(item.account.ID) == currentID {
				continue
			}
			if item.score < minScore {
				continue
			}
			if requireBetterThanCurrent && currentScore > 0 && item.score <= currentScore {
				continue
			}
			return item.account, true
		}
		return store.Account{}, false
	}

	target, ok := selectCandidate(threshold, true)
	if !ok {
		target, ok = selectCandidate(1, true)
	}
	if !ok || strings.TrimSpace(target.ID) == "" {
		return nil
	}
	if strings.TrimSpace(target.ID) == currentID {
		return nil
	}
	_, err = s.UseAccountCLI(WithCLISwitchReason(ctx, "coding"), target.ID)
	return err
}

func codingUsageScore(usage store.UsageSnapshot) int {
	hourly := usage.HourlyPct
	weekly := usage.WeeklyPct
	if hourly < weekly {
		return hourly
	}
	return weekly
}

func buildSessionPrompt(messages []store.CodingMessage) string {
	var b strings.Builder
	b.WriteString("You are Codex running in CodexSess web runtime.\n")
	b.WriteString("Focus on coding tasks, debugging, and practical implementation details.\n")
	b.WriteString("Conversation transcript:\n")

	limit := 24
	start := 0
	if len(messages) > limit {
		start = len(messages) - limit
	}
	for _, msg := range messages[start:] {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func buildSessionPromptWithIncoming(messages []store.CodingMessage, incoming string) string {
	var b strings.Builder
	base := buildSessionPrompt(messages)
	if strings.TrimSpace(base) != "" {
		b.WriteString(base)
		b.WriteString("\n")
	}
	text := strings.TrimSpace(incoming)
	if text != "" {
		b.WriteString("user: ")
		b.WriteString(text)
	}
	return strings.TrimSpace(b.String())
}

func expandWorkDir(workDir string) (string, error) {
	clean := normalizeWorkDir(workDir)
	if raw := strings.TrimSpace(os.Getenv("CODEXSESS_CODEX_WORKDIR")); raw != "" {
		clean = raw
	}
	if strings.HasPrefix(clean, "~/") || clean == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot resolve home directory")
		}
		suffix := strings.TrimPrefix(clean, "~")
		clean = filepath.Join(home, suffix)
	}
	if !filepath.IsAbs(clean) {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cannot resolve current directory")
		}
		clean = filepath.Join(wd, clean)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("workdir not found: %s", clean)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir is not a directory: %s", clean)
	}
	return clean, nil
}
