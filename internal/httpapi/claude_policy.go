package httpapi

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type claudePolicy interface {
	SanitizeMessagesForPrompt(messages []ClaudeMessage, toolDefs []ChatToolDef, sessionKey string) []ClaudeMessage
	SanitizeClientToolCalls(calls []ChatToolCall) []ChatToolCall
	SanitizePromptText(role, text string) (string, bool)
	SanitizeToolResultText(text string) (string, bool)
}

type claudeProtocolPolicy struct {
	mu               sync.Mutex
	invalidToolCache map[string]map[string]time.Time
}

func newClaudeProtocolPolicy() *claudeProtocolPolicy {
	return &claudeProtocolPolicy{invalidToolCache: make(map[string]map[string]time.Time)}
}

func (p *claudeProtocolPolicy) SanitizeMessagesForPrompt(messages []ClaudeMessage, toolDefs []ChatToolDef, sessionKey string) []ClaudeMessage {
	if len(messages) == 0 {
		return messages
	}
	droppedToolUseIDs := map[string]struct{}{}
	out := make([]ClaudeMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		contentRaw := strings.TrimSpace(string(msg.Content))
		if contentRaw == "" {
			continue
		}
		var items []map[string]any
		if err := json.Unmarshal(msg.Content, &items); err != nil {
			out = append(out, msg)
			continue
		}
		kept := make([]map[string]any, 0, len(items))
		for _, item := range items {
			typ := strings.ToLower(strings.TrimSpace(coerceAnyText(item["type"])))
			if typ == "" || typ == "text" {
				text := coerceAnyText(item["text"])
				cleaned, keep := p.SanitizePromptText(role, text)
				if !keep {
					continue
				}
				item["text"] = cleaned
			}
			if role == "assistant" && typ == "tool_use" {
				name := strings.TrimSpace(coerceAnyText(item["name"]))
				toolUseID := strings.TrimSpace(coerceAnyText(item["id"]))
				if strings.EqualFold(name, "skill") {
					if toolUseID != "" {
						droppedToolUseIDs[toolUseID] = struct{}{}
					}
					continue
				}
				args := coerceAnyJSON(item["input"])
				if def, ok := defaultOpenAITranslator.FindToolDefByName(toolDefs, name); ok {
					missing := defaultOpenAITranslator.MissingRequiredToolFields(def, args)
					if len(missing) > 0 {
						if toolUseID != "" {
							droppedToolUseIDs[toolUseID] = struct{}{}
						}
						p.rememberInvalidToolPattern(sessionKey, name, missing)
						continue
					}
				}
				if p.hasInvalidToolPattern(sessionKey, name, nil) {
					if toolUseID != "" {
						droppedToolUseIDs[toolUseID] = struct{}{}
					}
					continue
				}
			}
			if role == "user" && typ == "tool_result" {
				toolUseID := strings.TrimSpace(coerceAnyText(item["tool_use_id"]))
				if _, dropped := droppedToolUseIDs[toolUseID]; dropped {
					continue
				}
				cleanedResult, keep := p.SanitizeToolResultText(extractClaudeToolResultValue(item["content"]))
				if !keep {
					continue
				}
				item["content"] = cleanedResult
				content := strings.ToLower(strings.TrimSpace(cleanedResult))
				if strings.Contains(content, "required parameter") && strings.Contains(content, "missing") {
					continue
				}
			}
			kept = append(kept, item)
		}
		if len(kept) == 0 {
			continue
		}
		b, err := json.Marshal(kept)
		if err != nil {
			continue
		}
		out = append(out, ClaudeMessage{Role: msg.Role, Content: json.RawMessage(b)})
	}
	if len(out) == 0 {
		return messages
	}
	return out
}

func (p *claudeProtocolPolicy) SanitizeClientToolCalls(calls []ChatToolCall) []ChatToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		if shouldBlockClaudeClientToolName(name) {
			continue
		}
		call.Function.Arguments = sanitizeClaudeToolCallArguments(name, call.Function.Arguments)
		out = append(out, call)
	}
	return out
}

func (p *claudeProtocolPolicy) SanitizePromptText(role, text string) (string, bool) {
	return sanitizeClaudePromptText(role, text)
}

func (p *claudeProtocolPolicy) SanitizeToolResultText(text string) (string, bool) {
	return sanitizeClaudeToolResultText(text)
}

func (p *claudeProtocolPolicy) rememberInvalidToolPattern(sessionKey string, name string, missing []string) {
	session := strings.TrimSpace(sessionKey)
	tool := strings.ToLower(strings.TrimSpace(name))
	if session == "" || tool == "" {
		return
	}
	now := time.Now()
	signature := invalidToolPatternSignature(tool, missing)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.invalidToolCache == nil {
		p.invalidToolCache = make(map[string]map[string]time.Time)
	}
	p.pruneInvalidToolCacheLocked(now)
	entry := p.invalidToolCache[session]
	if entry == nil {
		entry = make(map[string]time.Time)
		p.invalidToolCache[session] = entry
	}
	exp := now.Add(invalidToolCacheTTL)
	entry[signature] = exp
	entry[invalidToolPatternSignature(tool, nil)] = exp
}

func (p *claudeProtocolPolicy) hasInvalidToolPattern(sessionKey string, name string, missing []string) bool {
	session := strings.TrimSpace(sessionKey)
	tool := strings.ToLower(strings.TrimSpace(name))
	if session == "" || tool == "" {
		return false
	}
	now := time.Now()
	signature := invalidToolPatternSignature(tool, missing)
	anySignature := invalidToolPatternSignature(tool, nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneInvalidToolCacheLocked(now)
	entry := p.invalidToolCache[session]
	if entry == nil {
		return false
	}
	if exp, ok := entry[signature]; ok && exp.After(now) {
		return true
	}
	if exp, ok := entry[anySignature]; ok && exp.After(now) {
		return true
	}
	return false
}

func (p *claudeProtocolPolicy) pruneInvalidToolCacheLocked(now time.Time) {
	if p.invalidToolCache == nil {
		return
	}
	for session, entries := range p.invalidToolCache {
		for sig, exp := range entries {
			if !exp.After(now) {
				delete(entries, sig)
			}
		}
		if len(entries) == 0 {
			delete(p.invalidToolCache, session)
		}
	}
}

func shouldBlockClaudeClientToolName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if !shouldBlockClaudeTaskTools() {
		return false
	}
	return strings.HasPrefix(lower, "task")
}

func shouldBlockClaudeTaskTools() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_BLOCK_TASK_TOOLS")))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func sanitizeClaudeToolCallArguments(name string, raw string) string {
	args := strings.TrimSpace(raw)
	if args == "" || !json.Valid([]byte(args)) {
		return raw
	}
	if strings.ToLower(strings.TrimSpace(name)) != "read" {
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil || obj == nil {
		return raw
	}
	if pages := strings.TrimSpace(coerceAnyText(obj["pages"])); pages == "" {
		delete(obj, "pages")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return string(b)
}

func invalidToolPatternSignature(tool string, missing []string) string {
	if len(missing) == 0 {
		return tool + "|any"
	}
	clean := make([]string, 0, len(missing))
	for _, item := range missing {
		field := strings.ToLower(strings.TrimSpace(item))
		if field != "" {
			clean = append(clean, field)
		}
	}
	if len(clean) == 0 {
		return tool + "|any"
	}
	sort.Strings(clean)
	return tool + "|" + strings.Join(clean, ",")
}
