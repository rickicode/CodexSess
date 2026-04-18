package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type claudeOutput struct {
	Text       string
	ToolCalls  []ChatToolCall
	Content    []ClaudeContentBlock
	StopReason string
}

type claudeTranslator struct{}

var defaultClaudeTranslator = claudeTranslator{}

func (claudeTranslator) NormalizeAnthropicVersion(v string) string {
	return normalizeAnthropicVersion(v)
}

func (claudeTranslator) ClassifyUpstreamError(err error) (int, string) {
	return classifyDirectUpstreamClaudeError(err)
}

func (claudeTranslator) ClassifySetupError(err error) (int, string, string) {
	return classifyClaudeSetupError(err)
}

func (claudeTranslator) NormalizeOutput(text string, defs []ChatToolDef, native []ChatToolCall) claudeOutput {
	return normalizeClaudeOutput(text, defs, native)
}

func (claudeTranslator) MapTools(tools []ClaudeToolDef) []ChatToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ChatToolDef, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if shouldBlockClaudeClientToolName(name) {
			continue
		}
		out = append(out, ChatToolDef{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:        name,
				Description: strings.TrimSpace(t.Description),
				Parameters:  t.InputSchema,
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (claudeTranslator) ApplyResponseDefaults(prompt string) string {
	return applyClaudeResponseDefaults(prompt)
}

func (claudeTranslator) ApplyTokenBudgetGuard(messages []ClaudeMessage, system json.RawMessage) ([]ClaudeMessage, json.RawMessage) {
	return applyClaudeTokenBudgetGuard(messages, system)
}

func (claudeTranslator) DeriveSessionKey(req ClaudeMessagesRequest, r *http.Request) string {
	return deriveClaudeSessionKey(req, r)
}

func (claudeTranslator) BuildPrompt(messages []ClaudeMessage, system json.RawMessage, tools []ChatToolDef, toolChoice json.RawMessage, injectTools bool) string {
	if injectTools {
		return promptFromClaudeMessagesWithSystemAndTools(messages, system, tools, toolChoice)
	}
	return promptFromClaudeMessagesWithSystemAndTools(messages, system, nil, nil)
}

func (claudeTranslator) SanitizeAssistantText(text string) string {
	return sanitizeClaudeAssistantText(text)
}

func (claudeTranslator) Policy(s *Server) claudePolicy {
	if s != nil {
		if s.claudePolicy == nil {
			s.claudePolicy = newClaudeProtocolPolicy()
		}
		return s.claudePolicy
	}
	return newClaudeProtocolPolicy()
}

func normalizeClaudeOutput(text string, defs []ChatToolDef, native []ChatToolCall) claudeOutput {
	sanitizedText := sanitizeClaudeAssistantText(text)
	toolCalls, _ := defaultOpenAITranslator.ResolveToolCalls(sanitizedText, defs, native)
	toolCalls = newClaudeProtocolPolicy().SanitizeClientToolCalls(toolCalls)
	content, stopReason := buildClaudeResponseContent(sanitizedText, toolCalls)
	return claudeOutput{
		Text:       sanitizedText,
		ToolCalls:  toolCalls,
		Content:    content,
		StopReason: stopReason,
	}
}

func (claudeTranslator) FilterToolCallsByDefs(calls []ChatToolCall, defs []ChatToolDef) ([]ChatToolCall, bool) {
	return defaultOpenAITranslator.FilterToolCallsByDefs(calls, defs)
}

func sanitizeClaudePromptText(role, text string) (string, bool) {
	cleaned := stripSystemReminderBlocks(text)
	if strings.TrimSpace(cleaned) == "" {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(role), "assistant") && isLikelyPolicyRefusalText(cleaned) {
		return "", false
	}
	return cleaned, true
}

func stripSystemReminderBlocks(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	for {
		start := strings.Index(lower, "<system-reminder>")
		if start < 0 {
			break
		}
		endRel := strings.Index(lower[start:], "</system-reminder>")
		if endRel < 0 {
			raw = strings.TrimSpace(raw[:start])
			break
		}
		end := start + endRel + len("</system-reminder>")
		raw = strings.TrimSpace(raw[:start] + "\n" + raw[end:])
		lower = strings.ToLower(raw)
	}
	return strings.TrimSpace(raw)
}

func isLikelyPolicyRefusalText(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	patterns := []string{
		"maaf, saya tidak bisa membantu",
		"maaf, saya tidak dapat membantu",
		"i can't help with",
		"i cannot help with",
		"i'm sorry, i can't help",
		"berpotensi disalahgunakan",
		"could be misused",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func sanitizeClaudeAssistantText(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if shouldDropClaudeTraceLine(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func sanitizeClaudeToolResultText(text string) (string, bool) {
	cleaned := stripSystemReminderBlocks(text)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", false
	}
	if isNoisyToolResultText(cleaned) {
		return "", false
	}
	const maxToolResultChars = 2800
	if len(cleaned) > maxToolResultChars {
		cleaned = strings.TrimSpace(cleaned[:maxToolResultChars]) + "\n...[truncated]"
	}
	return cleaned, true
}

func isNoisyToolResultText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "invalid pages parameter") {
		return true
	}
	if strings.Contains(lower, "exceeds maximum allowed tokens") {
		return true
	}
	if strings.Contains(lower, "this is a read-only task") {
		return true
	}
	return false
}

func shouldDropClaudeTraceLine(line string) bool {
	normalized := strings.TrimSpace(line)
	lower := strings.ToLower(normalized)
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "entered plan mode") {
		return true
	}
	if strings.Contains(lower, "successfully loaded skill") {
		return true
	}
	if strings.Contains(lower, "ctrl+b") {
		return true
	}
	if strings.HasPrefix(lower, "1 tasks (") || claudeTaskCountLinePattern.MatchString(lower) {
		return true
	}
	if strings.HasPrefix(lower, "◼ ") {
		return true
	}
	if strings.HasPrefix(lower, "● skill(") || strings.HasPrefix(lower, "skill(") {
		return true
	}
	if strings.HasPrefix(lower, "⎿") {
		return true
	}
	if claudeTraceCallLinePattern.MatchString(normalized) {
		return true
	}
	return false
}

func applyClaudeTokenBudgetGuard(messages []ClaudeMessage, system json.RawMessage) ([]ClaudeMessage, json.RawMessage) {
	msgs := cloneClaudeMessages(messages)
	sys := system
	softLimit := resolveClaudeTokenSoftLimit()
	hardLimit := resolveClaudeTokenHardLimit(softLimit)
	if len(msgs) == 0 {
		return msgs, sys
	}
	estimated := estimateClaudePromptTokens(msgs, sys)
	if estimated <= softLimit {
		return msgs, sys
	}

	msgs = trimClaudeMessagesTail(msgs, 24)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	msgs = trimClaudeMessagesTail(msgs, 16)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	sys = compressClaudeSystem(system, 2800)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	msgs = trimClaudeMessagesTail(msgs, 12)
	sys = compressClaudeSystem(sys, 1800)
	return msgs, sys
}

func resolveClaudeTokenSoftLimit() int {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_TOKEN_SOFT_LIMIT"))
	if raw == "" {
		return claudeTokenSoftLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 4000 {
		return claudeTokenSoftLimitDefault
	}
	return n
}

func resolveClaudeTokenHardLimit(softLimit int) int {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_TOKEN_HARD_LIMIT"))
	if raw == "" {
		if softLimit+4000 > claudeTokenHardLimitDefault {
			return softLimit + 4000
		}
		return claudeTokenHardLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < softLimit+2000 {
		return maxInt(softLimit+2000, claudeTokenHardLimitDefault)
	}
	return n
}

func estimateClaudePromptTokens(messages []ClaudeMessage, system json.RawMessage) int {
	text := promptFromClaudeMessagesWithSystemAndTools(messages, system, nil, nil)
	if strings.TrimSpace(text) == "" {
		return 0
	}
	chars := len([]rune(text))
	return (chars + 3) / 4
}

func trimClaudeMessagesTail(messages []ClaudeMessage, keep int) []ClaudeMessage {
	if keep <= 0 || len(messages) <= keep {
		return cloneClaudeMessages(messages)
	}
	start := len(messages) - keep
	out := make([]ClaudeMessage, 0, keep)
	out = append(out, messages[start:]...)
	return out
}

func cloneClaudeMessages(messages []ClaudeMessage) []ClaudeMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ClaudeMessage, 0, len(messages))
	out = append(out, messages...)
	return out
}

func compressClaudeSystem(system json.RawMessage, maxChars int) json.RawMessage {
	text := strings.TrimSpace(extractClaudeSystemText(system))
	if text == "" || maxChars <= 0 {
		return system
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return system
	}
	truncated := strings.TrimSpace(string(runes[:maxChars]))
	if truncated == "" {
		return system
	}
	b, err := json.Marshal(truncated + "\n...[system context truncated]")
	if err != nil {
		return system
	}
	return json.RawMessage(b)
}

func deriveClaudeSessionKey(req ClaudeMessagesRequest, r *http.Request) string {
	if metadataSession := extractSessionIDFromMetadata(req.Metadata); metadataSession != "" {
		return metadataSession
	}
	if fromHeader := strings.TrimSpace(r.Header.Get("x-claude-session-id")); fromHeader != "" {
		return fromHeader
	}
	ua := strings.TrimSpace(r.UserAgent())
	addr := strings.TrimSpace(r.RemoteAddr)
	if ua == "" && addr == "" {
		return "unknown"
	}
	return ua + "|" + addr
}

func extractSessionIDFromMetadata(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	return extractSessionIDFromMetadataAny(data, 0)
}

func extractSessionIDFromMetadataAny(data any, depth int) string {
	if depth > 3 || data == nil {
		return ""
	}
	switch v := data.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return ""
		}
		var nested any
		if err := json.Unmarshal([]byte(text), &nested); err != nil {
			return text
		}
		return extractSessionIDFromMetadataAny(nested, depth+1)
	case map[string]any:
		for _, key := range []string{"session_id", "sessionId"} {
			if id := strings.TrimSpace(coerceAnyText(v[key])); id != "" && id != "null" && id != "{}" {
				return id
			}
		}
		for _, key := range []string{"user_id", "userId"} {
			if id := extractSessionIDFromMetadataAny(v[key], depth+1); id != "" {
				return id
			}
		}
		for _, key := range []string{"metadata", "meta"} {
			if id := extractSessionIDFromMetadataAny(v[key], depth+1); id != "" {
				return id
			}
		}
		return ""
	default:
		return ""
	}
}
