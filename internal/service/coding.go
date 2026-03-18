package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

type CodingChatResult struct {
	Session    store.CodingSession
	User       store.CodingMessage
	Assistant  store.CodingMessage
	Assistants []store.CodingMessage
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

	prompt := buildCodingPrompt(commandMode, promptInput)
	resumeID := ""
	if commandMode == "chat" {
		resumeID = strings.TrimSpace(session.CodexThreadID)
	}
	if commandMode == "chat" && resumeID == "" && !isRawSlashCommand(promptInput) {
		history, err := s.Store.ListCodingMessages(ctx, sid)
		if err != nil {
			return CodingChatResult{}, err
		}
		prompt = buildContextHygienePrompt(buildSessionPromptWithIncoming(history, promptInput))
	}
	codexHome := strings.TrimSpace(s.Cfg.CodexHome)

	reply, err := s.Codex.ChatWithOptions(ctx, provider.ExecOptions{
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
		reply, err = s.Codex.ChatWithOptions(ctx, provider.ExecOptions{
			CodexHome:   codexHome,
			WorkDir:     resolvedWorkDir,
			Model:       useModel,
			Prompt:      buildCodingPrompt(commandMode, promptInput),
			Persist:     true,
			SandboxMode: useSandboxMode,
			CommandMode: commandMode,
		})
	}
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

	prompt := buildCodingPrompt(commandMode, promptInput)
	resumeID := ""
	if commandMode == "chat" {
		resumeID = strings.TrimSpace(session.CodexThreadID)
	}
	if commandMode == "chat" && resumeID == "" && !isRawSlashCommand(promptInput) {
		history, err := s.Store.ListCodingMessages(ctx, sid)
		if err != nil {
			return CodingChatResult{}, err
		}
		prompt = buildContextHygienePrompt(buildSessionPromptWithIncoming(history, promptInput))
	}
	codexHome := strings.TrimSpace(s.Cfg.CodexHome)

	streamedParts := make([]string, 0, 4)
	reply, err := s.Codex.StreamChatWithOptions(ctx, provider.ExecOptions{
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
		if eventType != "delta" && eventType != "assistant_message" && eventType != "activity" {
			return nil
		}
		delta := evt.Text
		if delta == "" {
			return nil
		}
		if eventType == "assistant_message" {
			streamedParts = append(streamedParts, delta)
		}
		if onEvent == nil {
			return nil
		}
		return onEvent(provider.ChatEvent{Type: eventType, Text: delta})
	})
	if err != nil && resumeID != "" && shouldStartNewThreadFromResumeError(err) {
		reply, err = s.Codex.StreamChatWithOptions(ctx, provider.ExecOptions{
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
	return buildContextHygienePrompt(trimmed)
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
		return "write"
	case "full-access", "danger-full-access", "full":
		return "full-access"
	default:
		return "full-access"
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
