package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

func isStatusSlashCommand(prompt string) bool {
	raw := strings.TrimSpace(strings.ToLower(prompt))
	return raw == "/status" || strings.HasPrefix(raw, "/status ")
}

func isMCPSlashCommand(prompt string) bool {
	raw := strings.TrimSpace(strings.ToLower(prompt))
	return raw == "/mcp" || strings.HasPrefix(raw, "/mcp ")
}

func isPlanSlashCommand(prompt string) bool {
	raw := strings.TrimSpace(strings.ToLower(prompt))
	return raw == "/plan" || strings.HasPrefix(raw, "/plan ")
}

func isRawSlashCommand(prompt string) bool {
	raw := strings.TrimSpace(prompt)
	if raw == "" {
		return false
	}
	if !strings.HasPrefix(raw, "/") {
		return false
	}
	return true
}

func unrecognizedChatCommandText(command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return "Perintah ini tidak dikenali di mode chat."
	}
	return fmt.Sprintf("Perintah %s tidak dikenali di mode chat.", cmd)
}

func slashCommandKeyword(input string) string {
	raw := strings.TrimSpace(input)
	if raw == "" || !strings.HasPrefix(raw, "/") {
		return ""
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func (s *Service) handleLocalUnknownChatCommand(
	ctx context.Context,
	session store.CodingSession,
	model string,
	workDir string,
	sandboxMode string,
	userVisibleContent string,
	command string,
	onEvent func(provider.ChatEvent) error,
) (CodingChatResult, error) {
	assistantText := unrecognizedChatCommandText(command)
	if onEvent != nil {
		if err := onEvent(provider.ChatEvent{Type: "assistant_message", Text: assistantText}); err != nil {
			return CodingChatResult{}, err
		}
	}
	return s.persistLocalCommandResponse(ctx, session, model, workDir, sandboxMode, userVisibleContent, assistantText, "")
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

	userMsg, err := s.appendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: session.ID,
		Role:      "user",
		Content:   firstNonEmpty(strings.TrimSpace(userVisibleContent), "/status"),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}
	assistantMsg, err := s.appendCodingMessage(ctx, store.CodingMessage{
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
	session.ReasoningLevel = normalizeCodingReasoningLevel(session.ReasoningLevel)
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
	userMsg, err := s.appendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: session.ID,
		Role:      "user",
		Content:   strings.TrimSpace(userVisibleContent),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return CodingChatResult{}, err
	}
	assistantMsg, err := s.appendCodingMessage(ctx, store.CodingMessage{
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
	session.ReasoningLevel = normalizeCodingReasoningLevel(session.ReasoningLevel)
	session.WorkDir = normalizeWorkDir(firstNonEmpty(workDir, session.WorkDir))
	session.SandboxMode = normalizeCodingSandboxMode(firstNonEmpty(sandboxMode, session.SandboxMode))
	session.UpdatedAt = time.Now().UTC()
	session.LastMessageAt = session.UpdatedAt
	if strings.EqualFold(strings.TrimSpace(session.Title), "new session") && strings.TrimSpace(defaultTitle) != "" {
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
