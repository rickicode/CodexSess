package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ricki/codexsess/internal/store"
)

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
	return strings.TrimSpace(content)
}

func resolveCommandContent(commandMode, rawContent string) (promptInput string, userVisibleContent string) {
	trimmed := strings.TrimSpace(rawContent)
	return trimmed, trimmed
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
		"no rollout found",
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
		if len(parts) > 0 {
			lastText := parts[len(parts)-1]
			switch {
			case text == lastText:
				continue
			case strings.Contains(text, lastText):
				parts[len(parts)-1] = text
				continue
			case strings.Contains(lastText, text):
				continue
			}
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

func normalizeCodingModel(model string) string {
	clean := strings.TrimSpace(model)
	if clean == "" {
		return "gpt-5.2-codex"
	}
	return clean
}

func normalizeCodingReasoningLevel(level string) string {
	clean := strings.TrimSpace(strings.ToLower(level))
	switch clean {
	case "low", "high":
		return clean
	default:
		return "medium"
	}
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
	case "read-only", "readonly", "read", "workspace-readonly":
		return "read-only"
	case "write", "workspace-write":
		return "workspace-write"
	case "full-access", "danger-full-access", "full":
		return "full-access"
	default:
		return "workspace-write"
	}
}

func normalizeCodingCommandMode(v string) string {
	return "chat"
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
	currentID, err := s.ActiveCLIAccountID(ctx)
	if err != nil {
		return err
	}
	currentID = strings.TrimSpace(currentID)
	if currentID != "" {
		return nil
	}

	accounts, err := s.ListAccounts(ctx)
	if err != nil || len(accounts) == 0 {
		return err
	}
	targetID := ""
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if account.ActiveCLI && id != "" {
			targetID = id
			break
		}
	}
	if targetID == "" {
		for _, account := range accounts {
			id := strings.TrimSpace(account.ID)
			if id == "" {
				continue
			}
			targetID = id
			break
		}
	}
	if targetID == "" {
		return nil
	}
	_, err = s.UseAccountCLI(WithCLISwitchReason(ctx, "coding"), targetID)
	return err
}

func buildSessionPrompt(session store.CodingSession, messages []store.CodingMessage) string {
	var b strings.Builder
	b.WriteString("You are Codex running in CodexSess web runtime.\n")
	b.WriteString("Focus on coding tasks, debugging, and practical implementation details.\n")
	b.WriteString("Conversation transcript:\n")

	limit := 12
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
	base := buildSessionPrompt(store.CodingSession{}, messages)
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

func buildSessionPromptWithSession(session store.CodingSession, messages []store.CodingMessage, incoming string) string {
	var b strings.Builder
	base := buildSessionPrompt(session, messages)
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
