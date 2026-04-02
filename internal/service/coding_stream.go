package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

const runtimeAppServerResumeRetryLimit = 5

func (s *Service) runCodingRoleAppServer(
	ctx context.Context,
	session store.CodingSession,
	commandMode string,
	resolvedWorkDir string,
	model string,
	reasoningLevel string,
	sandboxMode string,
	prompt string,
	resumeID string,
	bootstrapThreadID string,
	freshPrompt string,
	stream func(provider.ChatEvent) error,
) (provider.ChatResult, error) {
	if commandMode != "chat" {
		return provider.ChatResult{}, fmt.Errorf("unsupported coding command mode %q", commandMode)
	}
	emitRecoveryActivity := func(text string) error {
		return s.emitCodingActivity(ctx, session.ID, codingRuntimeRoleChat, text, stream)
	}
	runOnce := func(runtimeHome, currentPrompt, currentResumeID string) (provider.ChatResult, error) {
		threadID := strings.TrimSpace(currentResumeID)
		if threadID == "" {
			threadID = strings.TrimSpace(bootstrapThreadID)
		}
		opts := provider.ExecOptions{
			CodexHome:       runtimeHome,
			WorkDir:         resolvedWorkDir,
			Model:           model,
			ReasoningEffort: reasoningLevel,
			Prompt:          currentPrompt,
			ThreadID:        threadID,
			ResumeID:        strings.TrimSpace(currentResumeID),
			Persist:         true,
			SandboxMode:     sandboxMode,
			CommandMode:     "chat",
			Actor:           codingRuntimeRoleChat,
			OnProcessStart: func(_ int, forceKill func() error) {
				s.setCodingRunForceStop(session.ID, forceKill)
			},
		}
		if stream == nil {
			return s.Codex.AppServerChatWithOptions(ctx, opts)
		}
		return s.Codex.AppServerStreamChatWithOptions(ctx, opts, stream)
	}
	runWithResumePolicy := func(runtimeHome, currentPrompt, requestedResumeID string) (provider.ChatResult, error) {
		requestedResumeID = strings.TrimSpace(requestedResumeID)
		if requestedResumeID == "" {
			return runOnce(runtimeHome, currentPrompt, "")
		}
		var lastErr error
		resumeAttempts := 0
		for attempt := 1; attempt <= runtimeAppServerResumeRetryLimit; attempt++ {
			reply, err := runOnce(runtimeHome, currentPrompt, requestedResumeID)
			if err == nil {
				if attempt > 1 {
					if emitErr := emitRecoveryActivity(codingRuntimeRecoveryStepText("thread.resume_completed", codingRuntimeRoleChat, fmt.Sprintf("attempts=%d", attempt), "thread_id="+requestedResumeID)); emitErr != nil {
						return provider.ChatResult{}, emitErr
					}
				}
				return reply, nil
			}
			lastErr = err
			resumeAttempts = attempt
			if !shouldStartNewThreadFromResumeError(err) {
				if attempt < runtimeAppServerResumeRetryLimit {
					select {
					case <-ctx.Done():
						return provider.ChatResult{}, ctx.Err()
					case <-time.After(codingRuntimeRecoveryBackoff(attempt)):
					}
					continue
				}
				break
			}
			break
		}
		if resumeAttempts == 0 {
			resumeAttempts = 1
		}
		if err := emitRecoveryActivity(codingRuntimeRecoveryStepText("thread.resume_failed", codingRuntimeRoleChat, fmt.Sprintf("attempts=%d", resumeAttempts), "thread_id="+requestedResumeID)); err != nil {
			return provider.ChatResult{}, err
		}
		if strings.TrimSpace(freshPrompt) != "" {
			if err := emitRecoveryActivity(fmt.Sprintf("thread.rebootstrap_started role=%s previous_thread_id=%s", codingRuntimeRoleChat, requestedResumeID)); err != nil {
				return provider.ChatResult{}, err
			}
			return runOnce(runtimeHome, freshPrompt, "")
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("thread resume failed")
		}
		return provider.ChatResult{}, fmt.Errorf("thread resume failed after %d attempts: %w", resumeAttempts, lastErr)
	}

	runtimeHome, account, err := s.ensureCodingRuntimeHome(ctx, session.ID, codingRuntimeRoleChat)
	if err != nil {
		return provider.ChatResult{}, err
	}
	excludedAccounts := map[string]struct{}{}
	if accountID := strings.TrimSpace(account.ID); accountID != "" {
		excludedAccounts[accountID] = struct{}{}
	}
	requestedResumeID := strings.TrimSpace(resumeID)
	lastErr := error(nil)
	for recoveryAttempt := 0; recoveryAttempt < 5; recoveryAttempt++ {
		reply, err := runWithResumePolicy(runtimeHome, prompt, requestedResumeID)
		if err == nil {
			return reply, nil
		}
		lastErr = err
		if !codingRuntimeFailureRetryable(err) {
			return provider.ChatResult{}, err
		}
		recoveryCode := codingRuntimeFailureRecoveryCode(err)
		for _, step := range []string{
			codingRuntimeRecoveryStepText("runtime.recovery_detected", codingRuntimeRoleChat, "reason="+recoveryCode),
			codingRuntimeRecoveryStepText("turn.interrupt_requested", codingRuntimeRoleChat),
			codingRuntimeRecoveryStepText("runtime.stop_started", codingRuntimeRoleChat),
			codingRuntimeRecoveryStepText("runtime.stop_completed", codingRuntimeRoleChat),
			codingRuntimeRecoveryStepText("account.switch_started", codingRuntimeRoleChat),
			codingRuntimeRecoveryStepText("auth.sync_started", codingRuntimeRoleChat),
		} {
			if emitErr := emitRecoveryActivity(step); emitErr != nil {
				return provider.ChatResult{}, emitErr
			}
		}
		if codingRuntimeUsageExhausted(lastErr) {
			s.markCodingRuntimeAccountUsageLimited(ctx, strings.TrimSpace(account.ID), lastErr)
		}
		runtimeHome, account, err = s.switchCodingRuntimeAccount(ctx, session.ID, codingRuntimeRoleChat, excludedAccounts, codingRuntimeAccountDeactivated(lastErr))
		if err != nil {
			_ = emitRecoveryActivity(codingRuntimeRecoveryStepText("runtime.recovery_failed", codingRuntimeRoleChat, "reason="+recoveryCode))
			return provider.ChatResult{}, err
		}
		if accountID := strings.TrimSpace(account.ID); accountID != "" {
			excludedAccounts[accountID] = struct{}{}
			switchFields := []string{}
			if accountEmail := strings.TrimSpace(account.Email); accountEmail != "" {
				switchFields = append(switchFields, "account_email="+accountEmail)
			} else {
				switchFields = append(switchFields, "account_id="+accountID)
			}
			if emitErr := emitRecoveryActivity(codingRuntimeRecoveryStepText("account.switch_completed", codingRuntimeRoleChat, switchFields...)); emitErr != nil {
				return provider.ChatResult{}, emitErr
			}
		}
		for _, step := range []string{
			codingRuntimeRecoveryStepText("auth.sync_completed", codingRuntimeRoleChat),
			codingRuntimeRecoveryStepText("runtime.restart_started", codingRuntimeRoleChat),
			codingRuntimeRecoveryStepText("runtime.restart_completed", codingRuntimeRoleChat),
		} {
			if emitErr := emitRecoveryActivity(step); emitErr != nil {
				return provider.ChatResult{}, emitErr
			}
		}
		continueFields := []string{}
		if requestedResumeID != "" {
			continueFields = append(continueFields, "thread_id="+requestedResumeID)
		}
		if emitErr := emitRecoveryActivity(codingRuntimeRecoveryStepText("turn.continue_started", codingRuntimeRoleChat, continueFields...)); emitErr != nil {
			return provider.ChatResult{}, emitErr
		}
		select {
		case <-ctx.Done():
			return provider.ChatResult{}, ctx.Err()
		case <-time.After(codingRuntimeRecoveryBackoff(recoveryAttempt + 1)):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("runtime recovery failed")
	}
	_ = emitRecoveryActivity(codingRuntimeRecoveryStepText("runtime.recovery_failed", codingRuntimeRoleChat))
	return provider.ChatResult{}, lastErr
}

func (s *Service) buildCodingPromptInputs(
	ctx context.Context,
	sessionID string,
	session store.CodingSession,
	commandMode string,
	effectivePromptInput string,
	chatHistoryLen int,
) (string, string, string, string, error) {
	_ = ctx
	_ = sessionID
	prompt := buildCodingPrompt(commandMode, effectivePromptInput)
	freshPrompt := buildCodingPrompt(commandMode, effectivePromptInput)
	resumeID := ""
	bootstrapThreadID := ""
	if commandMode == "chat" {
		resumeID = codingChatThreadID(session)
		if chatHistoryLen == 0 {
			bootstrapThreadID = resumeID
			resumeID = ""
		}
	}
	if commandMode == "chat" {
		prompt = strings.TrimSpace(effectivePromptInput)
		freshPrompt = prompt
	}
	return prompt, freshPrompt, resumeID, bootstrapThreadID, nil
}

func (s *Service) persistAssistantParts(
	ctx context.Context,
	sessionID string,
	session store.CodingSession,
	commandMode string,
	assistantParts []string,
	reply provider.ChatResult,
) ([]store.CodingMessage, store.CodingMessage, error) {
	assistants := make([]store.CodingMessage, 0, len(assistantParts))
	for idx, part := range assistantParts {
		msg := store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: sessionID,
			Role:      "assistant",
			Actor:     codingAssistantActor(session, commandMode),
			Content:   part,
			CreatedAt: time.Now().UTC(),
		}
		if idx == len(assistantParts)-1 {
			msg.InputTokens = reply.InputTokens
			msg.OutputTokens = reply.OutputTokens
		}
		saved, err := s.appendCodingMessage(ctx, msg)
		if err != nil {
			return nil, store.CodingMessage{}, err
		}
		assistants = append(assistants, saved)
	}
	return assistants, assistants[len(assistants)-1], nil
}

func (s *Service) finalizeCodingTurn(
	ctx context.Context,
	sessionID string,
	session store.CodingSession,
	commandMode string,
	useModel string,
	useReasoningLevel string,
	useWorkDir string,
	useSandboxMode string,
	userVisibleContent string,
	reply provider.ChatResult,
	assistants []store.CodingMessage,
	assistantMsg store.CodingMessage,
	onEvent func(provider.ChatEvent) error,
) (store.CodingSession, []store.CodingMessage, store.CodingMessage, []store.CodingMessage, error) {
	session.Model = useModel
	session.ReasoningLevel = useReasoningLevel
	session.WorkDir = useWorkDir
	session.SandboxMode = useSandboxMode
	updateThreadStateForCommand(&session, commandMode, reply.ThreadID)
	session.UpdatedAt = time.Now().UTC()
	session.LastMessageAt = session.UpdatedAt
	if strings.EqualFold(strings.TrimSpace(session.Title), "new session") {
		titleSource := strings.TrimSpace(assistantMsg.Content)
		if titleSource == "" {
			titleSource = userVisibleContent
		}
		session.Title = deriveSessionTitle(titleSource)
	}
	if err := s.Store.UpdateCodingSession(ctx, session); err != nil {
		return store.CodingSession{}, nil, store.CodingMessage{}, nil, err
	}
	updatedSession, err := s.Store.GetCodingSession(ctx, sessionID)
	if err != nil {
		return store.CodingSession{}, nil, store.CodingMessage{}, nil, err
	}
	return updatedSession, assistants, assistantMsg, nil, nil
}

func (s *Service) beginCodingTurnContext(ctx context.Context, sessionID string) (string, context.Context, func(), error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return "", nil, nil, fmt.Errorf("session_id is required")
	}
	releaseRun, err := s.beginCodingRun(sid)
	if err != nil {
		return "", nil, nil, err
	}
	runCtx, runCancel := context.WithCancel(ctx)
	s.setCodingRunCancel(sid, runCancel)
	cleanup := func() {
		runCancel()
		releaseRun()
	}
	return sid, runCtx, cleanup, nil
}

func (s *Service) beginCodingRuntimeStateScope(runCtx context.Context, sessionID string) func() {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return func() {}
	}
	s.setCodingRuntimeState(runCtx, sid, "starting", nil)
	s.setCodingRuntimeState(runCtx, sid, "running", nil)
	return func() {
		s.setCodingRuntimeState(context.Background(), sid, "idle", nil)
		s.finalizeDeferredCodingRestart(context.Background(), sid)
	}
}

type codingTurnSetup struct {
	commandMode        string
	promptInput        string
	userVisibleContent string
	session            store.CodingSession
	runActor           string
	useModel           string
	useReasoningLevel  string
	useWorkDir         string
	useSandboxMode     string
	resolvedWorkDir    string
}

type codingTurnPreflight struct {
	chatHistory []store.CodingMessage
	handled     bool
	result      CodingChatResult
}

func (s *Service) prepareCodingTurnSetup(
	ctx context.Context,
	sessionID string,
	content string,
	model string,
	reasoningLevel string,
	workDirOverride string,
	sandboxModeOverride string,
	command string,
) (codingTurnSetup, error) {
	commandMode := normalizeCodingCommandMode(command)
	trimmedContent := strings.TrimSpace(content)
	promptInput, userVisibleContent := resolveCommandContent(commandMode, trimmedContent)
	if commandMode == "chat" && promptInput == "" {
		return codingTurnSetup{}, fmt.Errorf("message content is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sessionID)
	if err != nil {
		return codingTurnSetup{}, err
	}
	session = normalizeChatOnlySession(session)
	runActor := normalizeCodingRunnerRole(initialCodingRunActor(session, commandMode, promptInput))
	s.setCodingRunActor(sessionID, runActor)
	useModel := normalizeCodingModel(firstNonEmpty(model, session.Model))
	useReasoningLevel := normalizeCodingReasoningLevel(firstNonEmpty(reasoningLevel, session.ReasoningLevel))
	useWorkDir := normalizeWorkDir(firstNonEmpty(workDirOverride, session.WorkDir))
	useSandboxMode := normalizeCodingSandboxMode(firstNonEmpty(sandboxModeOverride, session.SandboxMode))
	resolvedWorkDir, err := expandWorkDir(useWorkDir)
	if err != nil {
		return codingTurnSetup{}, err
	}
	return codingTurnSetup{
		commandMode:        commandMode,
		promptInput:        promptInput,
		userVisibleContent: userVisibleContent,
		session:            session,
		runActor:           runActor,
		useModel:           useModel,
		useReasoningLevel:  useReasoningLevel,
		useWorkDir:         useWorkDir,
		useSandboxMode:     useSandboxMode,
		resolvedWorkDir:    resolvedWorkDir,
	}, nil
}

func normalizeChatOnlySession(session store.CodingSession) store.CodingSession {
	session.CodexThreadID = strings.TrimSpace(session.CodexThreadID)
	return session
}

func (s *Service) runCodingRoleWithErrorHandling(
	runCtx context.Context,
	sessionID string,
	session store.CodingSession,
	commandMode string,
	resolvedWorkDir string,
	model string,
	reasoningLevel string,
	sandboxMode string,
	prompt string,
	resumeID string,
	bootstrapThreadID string,
	freshPrompt string,
	stream func(provider.ChatEvent) error,
) (provider.ChatResult, error) {
	reply, err := s.runCodingRoleAppServer(
		runCtx,
		session,
		commandMode,
		resolvedWorkDir,
		model,
		reasoningLevel,
		sandboxMode,
		prompt,
		resumeID,
		bootstrapThreadID,
		freshPrompt,
		stream,
	)
	if err != nil {
		userErr := sanitizeCodingRuntimeUserFacingError(err)
		s.setCodingRuntimeState(runCtx, sessionID, "error", nil)
		_ = s.appendCodingRunFailureMessage(runCtx, sessionID, userErr)
		return provider.ChatResult{}, userErr
	}
	return reply, nil
}

func (s *Service) requireAssistantParts(
	runCtx context.Context,
	sessionID string,
	assistantParts []string,
) error {
	if len(assistantParts) > 0 {
		return nil
	}
	emptyErr := fmt.Errorf("empty response from codex")
	s.setCodingRuntimeState(runCtx, sessionID, "error", nil)
	_ = s.appendCodingRunFailureMessage(runCtx, sessionID, emptyErr)
	return emptyErr
}

func (s *Service) preflightCodingTurn(
	ctx context.Context,
	sessionID string,
	setup codingTurnSetup,
	onEvent func(provider.ChatEvent) error,
) (codingTurnPreflight, error) {
	if localResult, handled, localErr := s.handleLocalCodingCommand(
		ctx,
		setup.session,
		setup.commandMode,
		setup.promptInput,
		setup.userVisibleContent,
		setup.useModel,
		setup.useReasoningLevel,
		setup.useWorkDir,
		setup.useSandboxMode,
		onEvent,
	); handled {
		return codingTurnPreflight{handled: true, result: localResult}, localErr
	}
	chatHistory, err := s.maybeLoadChatHistory(ctx, sessionID, setup.session, setup.commandMode)
	if err != nil {
		return codingTurnPreflight{}, err
	}
	return codingTurnPreflight{chatHistory: chatHistory}, nil
}

func (s *Service) handleLocalCodingCommand(
	ctx context.Context,
	session store.CodingSession,
	commandMode string,
	promptInput string,
	userVisibleContent string,
	useModel string,
	useReasoningLevel string,
	useWorkDir string,
	useSandboxMode string,
	onEvent func(provider.ChatEvent) error,
) (CodingChatResult, bool, error) {
	if commandMode != "chat" {
		return CodingChatResult{}, false, nil
	}
	if isStatusSlashCommand(promptInput) {
		res, err := s.handleLocalStatusCommand(ctx, session, useModel, useWorkDir, useSandboxMode, userVisibleContent, onEvent)
		return res, true, err
	}
	if isMCPSlashCommand(promptInput) {
		res, err := s.handleLocalMCPCommand(ctx, session, useModel, useWorkDir, useSandboxMode, userVisibleContent, onEvent)
		return res, true, err
	}
	if isPlanSlashCommand(promptInput) {
		res, err := s.handleLocalUnknownChatCommand(ctx, session, useModel, useWorkDir, useSandboxMode, userVisibleContent, slashCommandKeyword(promptInput), onEvent)
		return res, true, err
	}
	return CodingChatResult{}, false, nil
}

func (s *Service) maybeLoadChatHistory(
	ctx context.Context,
	sessionID string,
	session store.CodingSession,
	commandMode string,
) ([]store.CodingMessage, error) {
	_ = session
	if commandMode != "chat" {
		return nil, nil
	}
	return s.Store.ListCodingMessages(ctx, sessionID)
}

func (s *Service) SendCodingMessage(ctx context.Context, sessionID, content, model, reasoningLevel, workDirOverride, sandboxModeOverride, command string) (CodingChatResult, error) {
	sid, runCtx, cleanupRun, err := s.beginCodingTurnContext(ctx, sessionID)
	if err != nil {
		return CodingChatResult{}, err
	}
	defer cleanupRun()
	setup, err := s.prepareCodingTurnSetup(ctx, sid, content, model, reasoningLevel, workDirOverride, sandboxModeOverride, command)
	if err != nil {
		return CodingChatResult{}, err
	}
	preflight, err := s.preflightCodingTurn(ctx, sid, setup, nil)
	if err != nil {
		return CodingChatResult{}, err
	}
	if preflight.handled {
		return preflight.result, nil
	}
	effectivePromptInput := setup.promptInput
	prompt, freshPrompt, resumeID, bootstrapThreadID, err := s.buildCodingPromptInputs(ctx, sid, setup.session, setup.commandMode, effectivePromptInput, len(preflight.chatHistory))
	if err != nil {
		return CodingChatResult{}, err
	}
	defer s.beginCodingRuntimeStateScope(runCtx, sid)()
	reply, err := s.runCodingRoleWithErrorHandling(
		runCtx,
		sid,
		setup.session,
		setup.commandMode,
		setup.resolvedWorkDir,
		setup.useModel,
		setup.useReasoningLevel,
		setup.useSandboxMode,
		prompt,
		resumeID,
		bootstrapThreadID,
		freshPrompt,
		nil,
	)
	if err != nil {
		return CodingChatResult{}, err
	}

	userMsg, err := s.appendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: sid,
		Role:      "user",
		Content:   setup.userVisibleContent,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}
	assistantParts := normalizedAssistantParts(reply.Messages, reply.Text)
	if err := s.requireAssistantParts(runCtx, sid, assistantParts); err != nil {
		return CodingChatResult{}, err
	}
	assistants, assistantMsg, err := s.persistAssistantParts(ctx, sid, setup.session, setup.commandMode, assistantParts, reply)
	if err != nil {
		return CodingChatResult{}, err
	}
	updatedSession, assistants, assistantMsg, driverEvents, err := s.finalizeCodingTurn(
		ctx,
		sid,
		setup.session,
		setup.commandMode,
		setup.useModel,
		setup.useReasoningLevel,
		setup.useWorkDir,
		setup.useSandboxMode,
		setup.userVisibleContent,
		reply,
		assistants,
		assistantMsg,
		nil,
	)
	if err != nil {
		return CodingChatResult{}, err
	}
	return CodingChatResult{
		Session:       updatedSession,
		User:          userMsg,
		Assistant:     assistantMsg,
		Assistants:    assistants,
		EventMessages: driverEvents,
	}, nil
}

func (s *Service) SendCodingMessageStream(
	ctx context.Context,
	sessionID,
	content,
	model,
	reasoningLevel,
	workDirOverride string,
	sandboxModeOverride string,
	command string,
	onEvent func(provider.ChatEvent) error,
) (CodingChatResult, error) {
	sid, runCtx, cleanupRun, err := s.beginCodingTurnContext(ctx, sessionID)
	if err != nil {
		return CodingChatResult{}, err
	}
	defer cleanupRun()
	setup, err := s.prepareCodingTurnSetup(ctx, sid, content, model, reasoningLevel, workDirOverride, sandboxModeOverride, command)
	if err != nil {
		return CodingChatResult{}, err
	}
	preflight, err := s.preflightCodingTurn(ctx, sid, setup, onEvent)
	if err != nil {
		return CodingChatResult{}, err
	}
	if preflight.handled {
		return preflight.result, nil
	}
	effectivePromptInput := setup.promptInput
	userMsg, err := s.appendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: sid,
		Role:      "user",
		Content:   setup.userVisibleContent,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}

	prompt, freshPrompt, resumeID, bootstrapThreadID, err := s.buildCodingPromptInputs(ctx, sid, setup.session, setup.commandMode, effectivePromptInput, len(preflight.chatHistory))
	if err != nil {
		return CodingChatResult{}, err
	}
	defer s.beginCodingRuntimeStateScope(runCtx, sid)()

	streamedParts := make([]string, 0, 4)
	var streamedText strings.Builder
	persistedEvents := make([]store.CodingMessage, 0, codingEventPersistMax+1)
	droppedEvents := 0
	subagentState := &subagentIdentityState{}
	streamHandler := func(evt provider.ChatEvent) error {
		eventType := strings.TrimSpace(strings.ToLower(evt.Type))
		if eventType != "delta" &&
			eventType != "assistant_message" &&
			eventType != "activity" &&
			eventType != "raw_event" &&
			eventType != "stderr" {
			return nil
		}
		delta := evt.Text
		if eventType == "raw_event" {
			delta = enrichSubagentEventRaw(delta, subagentState)
		}
		if delta == "" {
			return nil
		}
		if eventType == "assistant_message" {
			streamedParts = append(streamedParts, delta)
		}
		if eventType == "delta" {
			streamedText.WriteString(delta)
		}
		if eventType != "assistant_message" {
			if role := roleFromCodingStreamEvent(eventType); role != "" {
				item := store.CodingMessage{
					ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
					SessionID: sid,
					Role:      role,
					Actor:     firstNonEmpty(normalizeCodingRunnerRole(evt.Actor), setup.runActor),
					Content:   truncateRunes(delta, codingEventContentMaxRunes),
					CreatedAt: time.Now().UTC(),
				}
				if len(persistedEvents) >= codingEventPersistMax {
					droppedEvents++
				} else {
					saved, saveErr := s.appendCodingMessage(runCtx, item)
					if saveErr != nil {
						return saveErr
					}
					persistedEvents = append(persistedEvents, saved)
				}
			}
		}
		if onEvent == nil {
			return nil
		}
		return onEvent(provider.ChatEvent{Type: eventType, Text: delta, Actor: evt.Actor})
	}
	reply, err := s.runCodingRoleWithErrorHandling(
		runCtx,
		sid,
		setup.session,
		setup.commandMode,
		setup.resolvedWorkDir,
		setup.useModel,
		setup.useReasoningLevel,
		setup.useSandboxMode,
		prompt,
		resumeID,
		bootstrapThreadID,
		freshPrompt,
		streamHandler,
	)
	if err != nil {
		return CodingChatResult{}, err
	}

	assistantParts := resolveStreamAssistantParts(reply, streamedText.String(), streamedParts)
	if err := s.requireAssistantParts(runCtx, sid, assistantParts); err != nil {
		return CodingChatResult{}, err
	}

	if droppedEvents > 0 {
		saved, saveErr := s.appendCodingMessage(runCtx, store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: sid,
			Role:      "activity",
			Actor:     setup.runActor,
			Content:   fmt.Sprintf("Event log truncated: %d additional entries omitted.", droppedEvents),
			CreatedAt: time.Now().UTC(),
		})
		if saveErr != nil {
			return CodingChatResult{}, saveErr
		}
		persistedEvents = append(persistedEvents, saved)
	}
	assistants, assistantMsg, err := s.persistAssistantParts(ctx, sid, setup.session, setup.commandMode, assistantParts, reply)
	if err != nil {
		return CodingChatResult{}, err
	}
	updatedSession, assistants, assistantMsg, driverEvents, err := s.finalizeCodingTurn(
		ctx,
		sid,
		setup.session,
		setup.commandMode,
		setup.useModel,
		setup.useReasoningLevel,
		setup.useWorkDir,
		setup.useSandboxMode,
		setup.userVisibleContent,
		reply,
		assistants,
		assistantMsg,
		onEvent,
	)
	if err != nil {
		return CodingChatResult{}, err
	}
	if len(driverEvents) > 0 {
		persistedEvents = append(persistedEvents, driverEvents...)
	}
	return CodingChatResult{
		Session:           updatedSession,
		User:              userMsg,
		Assistant:         assistantMsg,
		Assistants:        assistants,
		EventMessages:     persistedEvents,
		CachedInputTokens: reply.CachedInputTokens,
	}, nil
}

func resolveStreamAssistantParts(reply provider.ChatResult, streamedText string, streamedParts []string) []string {
	assistantParts := normalizedAssistantParts(reply.Messages, reply.Text)
	if len(assistantParts) > 0 {
		return assistantParts
	}
	if len(streamedParts) > 0 {
		assistantParts = normalizedAssistantParts(streamedParts, "")
		if len(assistantParts) > 0 {
			return assistantParts
		}
	}
	if merged := strings.TrimSpace(streamedText); merged != "" {
		return []string{merged}
	}
	return nil
}

func codingRuntimeAccountDeactivated(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "account_deactivated") ||
		strings.Contains(msg, "auth error code: account_deactivated") ||
		strings.Contains(msg, "account suspended") ||
		strings.Contains(msg, "account has been deactivated") ||
		(strings.Contains(msg, "deactivated") && (strings.Contains(msg, "401") || strings.Contains(msg, "unauthorized"))) ||
		strings.Contains(msg, "account_suspended")
}

func codingRuntimeUsageExhausted(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return (strings.Contains(msg, "429") && (strings.Contains(msg, "usage") || strings.Contains(msg, "quota") || strings.Contains(msg, "rate limit"))) ||
		strings.Contains(msg, "rate limit exceeded") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "hit your usage limit") ||
		strings.Contains(msg, "usage limit. upgrade to plus") ||
		strings.Contains(msg, "quota exceeded") ||
		strings.Contains(msg, "quota reached") ||
		strings.Contains(msg, "usage limit reached") ||
		strings.Contains(msg, "usage exhausted") ||
		strings.Contains(msg, "billing hard limit") ||
		strings.Contains(msg, "insufficient_quota")
}

func codingRuntimeModelCapacity(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "selected model is at capacity") ||
		strings.Contains(msg, "model is at capacity") ||
		strings.Contains(msg, "model capacity") ||
		strings.Contains(msg, "model_capacity")
}

func sanitizeCodingRuntimeUserFacingError(err error) error {
	if err == nil {
		return nil
	}
	var runtimeErr *CodingRuntimeError
	if errors.As(err, &runtimeErr) && runtimeErr != nil {
		return runtimeErr
	}
	if codingRuntimeShellLiveAccessUnavailable(err) {
		return fmt.Errorf("runtime shell live access unavailable for network/download work in this shell")
	}
	if codingRuntimeAccountDeactivated(err) {
		return fmt.Errorf("runtime auth failed: account deactivated (401)")
	}
	if codingRuntimeModelCapacity(err) {
		return fmt.Errorf("runtime model capacity reached")
	}
	if codingRuntimeUsageExhausted(err) {
		return fmt.Errorf("runtime rate limited or quota exhausted (429)")
	}
	return err
}

func codingRuntimeShellLiveAccessUnavailable(err error) bool {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "live access is still not available in this shell") ||
		(strings.Contains(msg, "live access") &&
			strings.Contains(msg, "not available in this shell"))
}

func codingChatThreadID(session store.CodingSession) string {
	return strings.TrimSpace(session.CodexThreadID)
}

func initialCodingRunActor(session store.CodingSession, commandMode, promptInput string) string {
	_ = session
	_ = commandMode
	_ = promptInput
	return "chat"
}

func updateChatThreadState(session *store.CodingSession, threadID string) {
	if session == nil {
		return
	}
	tid := strings.TrimSpace(threadID)
	if tid == "" {
		return
	}
	session.CodexThreadID = tid
}

func updateThreadStateForCommand(session *store.CodingSession, commandMode, threadID string) {
	switch strings.TrimSpace(strings.ToLower(commandMode)) {
	default:
		updateChatThreadState(session, threadID)
	}
}

func (s *Service) beginCodingRun(sessionID string) (func(), error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return func() {}, fmt.Errorf("session_id is required")
	}
	now := time.Now().UTC()
	s.codingRunMu.Lock()
	if s.codingRuns == nil {
		s.codingRuns = map[string]*codingRunState{}
	}
	if _, exists := s.codingRuns[sid]; exists {
		s.codingRunMu.Unlock()
		return nil, ErrCodingSessionBusy
	}
	s.codingRunSeq++
	runID := s.codingRunSeq
	s.codingRuns[sid] = &codingRunState{id: runID, startedAt: now}
	s.codingRunMu.Unlock()
	released := false
	return func() {
		s.codingRunMu.Lock()
		if !released {
			if current := s.codingRuns[sid]; current != nil && current.id == runID {
				delete(s.codingRuns, sid)
			}
			released = true
		}
		s.codingRunMu.Unlock()
	}, nil
}

func (s *Service) forceReleaseCodingRun(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	s.codingRunMu.Lock()
	delete(s.codingRuns, sid)
	s.codingRunMu.Unlock()
}

func (s *Service) CodingRunStatus(sessionID string) (bool, time.Time, string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false, time.Time{}, ""
	}
	s.codingRunMu.Lock()
	runState, ok := s.codingRuns[sid]
	s.codingRunMu.Unlock()
	if !ok || runState == nil {
		return false, time.Time{}, ""
	}
	return true, runState.startedAt, normalizeCodingRunnerRole(runState.actor)
}

func (s *Service) StopCodingRun(sessionID string, force bool) bool {
	stopped, _, _ := s.stopCodingRun(sessionID, force)
	return stopped
}

func (s *Service) stopCodingRun(sessionID string, force bool) (bool, string, bool) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false, "", false
	}
	s.codingRunMu.Lock()
	runState := s.codingRuns[sid]
	cancel := context.CancelFunc(nil)
	forceKill := func() error { return nil }
	activeRole := ""
	if runState != nil {
		activeRole = normalizeCodingRunnerRole(runState.actor)
		cancel = runState.cancel
		if runState.forceKill != nil {
			forceKill = runState.forceKill
		}
	}
	s.codingRunMu.Unlock()
	if cancel == nil {
		return false, activeRole, false
	}
	if force {
		_ = forceKill()
	}
	cancel()
	return true, activeRole, false
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

func (s *Service) setCodingRunForceStop(sessionID string, forceKill func() error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" || forceKill == nil {
		return
	}
	s.codingRunMu.Lock()
	defer s.codingRunMu.Unlock()
	runState := s.codingRuns[sid]
	if runState == nil {
		return
	}
	if existing := runState.forceKill; existing != nil {
		runState.forceKill = func() error {
			if err := existing(); err != nil {
				return err
			}
			return forceKill()
		}
		return
	}
	runState.forceKill = forceKill
}

func (s *Service) clearCodingRunForceStop(sessionID string) {
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
	runState.forceKill = nil
}

func (s *Service) setCodingRunActor(sessionID, actor string) {
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
	runState.actor = normalizeCodingRunnerRole(actor)
}

func codingAssistantActor(session store.CodingSession, commandMode string) string {
	_ = session
	_ = commandMode
	return ""
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

func (s *Service) emitCodingActivity(ctx context.Context, sessionID, actor, text string, stream func(provider.ChatEvent) error) error {
	sid := strings.TrimSpace(sessionID)
	content := strings.TrimSpace(text)
	runner := normalizeCodingRunnerRole(actor)
	if sid == "" || content == "" {
		return nil
	}
	if stream != nil {
		return stream(provider.ChatEvent{Type: "activity", Text: content, Actor: runner})
	}
	_, err := s.appendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: sid,
		Role:      "activity",
		Actor:     runner,
		Content:   truncateRunes(content, codingEventContentMaxRunes),
		CreatedAt: time.Now().UTC(),
	})
	return err
}

func (s *Service) appendCodingRunFailureMessage(ctx context.Context, sessionID string, runErr error) error {
	sid := strings.TrimSpace(sessionID)
	if sid == "" || runErr == nil {
		return nil
	}

	persistCtx := ctx
	if persistCtx == nil || persistCtx.Err() != nil {
		persistCtx = context.Background()
	}
	appendFailure := func(pctx context.Context) error {
		_, err := s.appendCodingMessage(pctx, store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: sid,
			Role:      "stderr",
			Content:   truncateRunes(fmt.Sprintf("Run failed: %s", strings.TrimSpace(runErr.Error())), codingEventContentMaxRunes),
			CreatedAt: time.Now().UTC(),
		})
		return err
	}
	if err := appendFailure(persistCtx); err != nil {
		if persistCtx != context.Background() {
			if retryErr := appendFailure(context.Background()); retryErr != nil {
				return retryErr
			}
		} else {
			return err
		}
	}

	session, getErr := s.Store.GetCodingSession(persistCtx, sid)
	if getErr != nil && persistCtx != context.Background() {
		session, getErr = s.Store.GetCodingSession(context.Background(), sid)
	}
	if getErr != nil {
		return nil
	}
	session.UpdatedAt = time.Now().UTC()
	session.LastMessageAt = session.UpdatedAt
	_ = s.Store.UpdateCodingSession(context.Background(), session)
	return nil
}
