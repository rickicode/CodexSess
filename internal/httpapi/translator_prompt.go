package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func mapClaudeToolsToChatTools(tools []ClaudeToolDef) []ChatToolDef {
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

func parseToolArgumentsForClaude(arguments string) any {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return map[string]any{}
	}
	var out any
	if json.Unmarshal([]byte(raw), &out) == nil {
		return out
	}
	return map[string]any{"raw": raw}
}

func promptFromMessages(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		if role == "tool" && strings.TrimSpace(m.ToolCallID) != "" {
			sb.WriteString("(")
			sb.WriteString(strings.TrimSpace(m.ToolCallID))
			sb.WriteString(")")
		}
		sb.WriteString(": ")
		sb.WriteString(extractOpenAIContentText(m.Content))
		if len(m.ToolCalls) > 0 {
			sb.WriteString("\nassistant_tool_calls: ")
			for i, tc := range m.ToolCalls {
				if i > 0 {
					sb.WriteString(" | ")
				}
				sb.WriteString(strings.TrimSpace(tc.Function.Name))
				sb.WriteString("(")
				sb.WriteString(strings.TrimSpace(tc.Function.Arguments))
				sb.WriteString(")")
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func extractOpenAIContentText(raw any) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, it := range v {
			obj, ok := it.(map[string]any)
			if !ok {
				continue
			}
			t, _ := obj["type"].(string)
			if t != "" && t != "text" {
				continue
			}
			text, _ := obj["text"].(string)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		text, _ := v["text"].(string)
		return strings.TrimSpace(text)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			return strings.TrimSpace(s)
		}
		var items []map[string]any
		if err := json.Unmarshal(b, &items); err == nil {
			var parts []string
			for _, it := range items {
				t, _ := it["type"].(string)
				if t != "" && t != "text" {
					continue
				}
				text, _ := it["text"].(string)
				if strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			return strings.TrimSpace(strings.Join(parts, "\n"))
		}
		return ""
	}
}

func promptFromMessagesWithTools(msgs []ChatMessage, tools []ChatToolDef, toolChoice json.RawMessage) string {
	base := promptFromMessages(msgs)
	return promptFromTextWithTools(base, tools, toolChoice)
}

func promptFromTextWithTools(base string, tools []ChatToolDef, toolChoice json.RawMessage) string {
	if len(tools) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\nAVAILABLE_TOOLS_JSON:\n")
	sb.WriteString("[\n")
	for i, t := range tools {
		if i > 0 {
			sb.WriteString(",\n")
		}
		b, _ := json.Marshal(defaultOpenAITranslator.toolDefPromptValue(t))
		sb.WriteString(string(b))
	}
	sb.WriteString("\n]\n")
	if len(bytes.TrimSpace(toolChoice)) > 0 {
		sb.WriteString("TOOL_CHOICE_JSON:\n")
		sb.WriteString(strings.TrimSpace(string(toolChoice)))
		sb.WriteString("\n")
	}
	sb.WriteString("TOOL_OUTPUT_RULES:\n")
	sb.WriteString("- If a tool is required, respond with JSON only.\n")
	sb.WriteString("- JSON format must be exactly: {\"tool_calls\":[{\"name\":\"<tool_name>\",\"arguments\":{...}}]}.\n")
	sb.WriteString("- Do not wrap JSON in markdown fences.\n")
	sb.WriteString("- If no tool is needed, respond normally with plain assistant text.\n")
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessages(msgs []ClaudeMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		textParts, toolCallParts, toolResultParts := extractClaudeMessageParts(m.Content)
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(strings.TrimSpace(strings.Join(textParts, "\n")))
		if len(toolCallParts) > 0 {
			sb.WriteString("\nassistant_tool_calls: ")
			sb.WriteString(strings.Join(toolCallParts, " | "))
		}
		for _, tr := range toolResultParts {
			sb.WriteString("\n")
			sb.WriteString(tr)
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessagesWithSystemAndTools(
	msgs []ClaudeMessage,
	system json.RawMessage,
	tools []ChatToolDef,
	toolChoice json.RawMessage,
) string {
	base := promptFromClaudeMessages(msgs)
	if systemText := extractClaudeSystemText(system); systemText != "" {
		if strings.TrimSpace(base) != "" {
			base = "system: " + systemText + "\n" + base
		} else {
			base = "system: " + systemText
		}
	}
	return promptFromTextWithTools(base, tools, toolChoice)
}

func applyClaudeResponseDefaults(prompt string) string {
	base := strings.TrimSpace(prompt)
	if base == "" {
		return ""
	}
	defaults := strings.Join([]string{
		"system: Response defaults:",
		"- For broad analysis/debug requests, begin with a best-effort system analysis immediately.",
		"- Make reasonable assumptions and proceed; avoid asking scope-first questions unless truly blocked.",
		"- Prefer actionable findings first, then assumptions/open questions.",
	}, "\n")
	return strings.TrimSpace(defaults + "\n\n" + base)
}

func extractClaudeSystemText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	textParts, _, _ := extractClaudeMessageParts(raw)
	return strings.TrimSpace(strings.Join(textParts, "\n"))
}

func extractClaudeMessageParts(raw json.RawMessage) ([]string, []string, []string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		text := strings.TrimSpace(asString)
		if text == "" {
			return nil, nil, nil
		}
		return []string{text}, nil, nil
	}

	var asItems []map[string]any
	if err := json.Unmarshal(raw, &asItems); err != nil {
		return nil, nil, nil
	}
	textParts := make([]string, 0, len(asItems))
	toolCalls := make([]string, 0, 2)
	toolResults := make([]string, 0, 2)
	for _, it := range asItems {
		typ := strings.ToLower(strings.TrimSpace(coerceAnyText(it["type"])))
		switch typ {
		case "", "text":
			if text := strings.TrimSpace(coerceAnyText(it["text"])); text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			name := strings.TrimSpace(coerceAnyText(it["name"]))
			if name == "" {
				continue
			}
			args := coerceAnyJSON(it["input"])
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", name, args))
		case "tool_result":
			toolUseID := strings.TrimSpace(coerceAnyText(it["tool_use_id"]))
			if toolUseID == "" {
				toolUseID = "unknown"
			}
			result := extractClaudeToolResultValue(it["content"])
			toolResults = append(toolResults, fmt.Sprintf("tool(%s): %s", toolUseID, result))
		}
	}
	return textParts, toolCalls, toolResults
}

func extractClaudeToolResultValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "{}"
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return "{}"
		}
		return trimmed
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t := strings.ToLower(strings.TrimSpace(coerceAnyText(obj["type"])))
			if t != "" && t != "text" {
				continue
			}
			text := strings.TrimSpace(coerceAnyText(obj["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		b, _ := json.Marshal(x)
		return strings.TrimSpace(string(b))
	default:
		b, _ := json.Marshal(x)
		raw := strings.TrimSpace(string(b))
		if raw == "" {
			return "{}"
		}
		return raw
	}
}

func buildClaudeResponseContent(text string, calls []ChatToolCall) ([]ClaudeContentBlock, string) {
	content := make([]ClaudeContentBlock, 0, len(calls)+1)
	trimmedText := strings.TrimSpace(text)
	if trimmedText != "" {
		content = append(content, ClaudeContentBlock{
			Type: "text",
			Text: trimmedText,
		})
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		content = append(content, ClaudeContentBlock{
			Type:  "tool_use",
			ID:    callID,
			Name:  name,
			Input: parseToolArgumentsForClaude(call.Function.Arguments),
		})
	}
	if len(content) == 0 {
		content = append(content, ClaudeContentBlock{
			Type: "text",
			Text: "",
		})
		return content, "end_turn"
	}
	if len(calls) > 0 {
		return content, "tool_use"
	}
	return content, "end_turn"
}
