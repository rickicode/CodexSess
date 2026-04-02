package provider

import (
	"bytes"
	"encoding/json"
	"strings"
)

func appendUniqueToolCalls(dst *[]ToolCall, seen map[string]struct{}, incoming []ToolCall) {
	if len(incoming) == 0 || dst == nil {
		return
	}
	for _, tc := range incoming {
		name := strings.TrimSpace(tc.Name)
		if name == "" {
			continue
		}
		tc.Name = name
		tc.ID = strings.TrimSpace(tc.ID)
		tc.Arguments = strings.TrimSpace(tc.Arguments)
		if tc.Arguments == "" {
			tc.Arguments = "{}"
		}
		key := tc.ID + "|" + tc.Name + "|" + tc.Arguments
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		*dst = append(*dst, tc)
	}
}

func codexEventToolCalls(evt map[string]any) []ToolCall {
	if evt == nil {
		return nil
	}
	eventType := strings.TrimSpace(strings.ToLower(asString(evt["type"])))
	switch eventType {
	case "item.completed", "item.updated", "item.added", "response.output_item.done":
		if item, _ := evt["item"].(map[string]any); item != nil {
			return extractToolCallsFromAny(item)
		}
	case "response.completed":
		if response, _ := evt["response"].(map[string]any); response != nil {
			if output, _ := response["output"].([]any); len(output) > 0 {
				return extractToolCallsFromAny(output)
			}
		}
	}
	if output, _ := evt["output"].([]any); len(output) > 0 {
		return extractToolCallsFromAny(output)
	}
	if item, _ := evt["item"].(map[string]any); item != nil {
		return extractToolCallsFromAny(item)
	}
	return nil
}

func extractToolCallsFromAny(v any) []ToolCall {
	switch t := v.(type) {
	case []any:
		out := make([]ToolCall, 0, len(t))
		for _, raw := range t {
			out = append(out, extractToolCallsFromAny(raw)...)
		}
		return out
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		itemType := strings.TrimSpace(strings.ToLower(asString(t["type"])))
		if itemType == "" {
			return nil
		}
		if !strings.Contains(itemType, "tool_call") && !strings.Contains(itemType, "function_call") {
			return nil
		}
		name := strings.TrimSpace(extractToolName(t))
		if name == "" {
			return nil
		}
		id := strings.TrimSpace(firstStringFromMap(t, "call_id", "tool_call_id", "id"))
		args := normalizeToolCallArguments(extractToolArguments(t))
		return []ToolCall{{
			ID:        id,
			Name:      name,
			Arguments: args,
		}}
	default:
		return nil
	}
}

func extractToolName(m map[string]any) string {
	if m == nil {
		return ""
	}
	if fn, _ := m["function"].(map[string]any); fn != nil {
		if name := strings.TrimSpace(firstStringFromMap(fn, "name", "tool_name")); name != "" {
			return name
		}
	}
	return strings.TrimSpace(firstStringFromMap(m, "name", "tool_name", "tool"))
}

func extractToolArguments(m map[string]any) any {
	if m == nil {
		return map[string]any{}
	}
	if fn, _ := m["function"].(map[string]any); fn != nil {
		if v, ok := fn["arguments"]; ok {
			return v
		}
		if v, ok := fn["input"]; ok {
			return v
		}
	}
	for _, key := range []string{"arguments", "input", "params", "payload"} {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return map[string]any{}
}

func normalizeToolCallArguments(v any) string {
	switch t := v.(type) {
	case string:
		raw := strings.TrimSpace(t)
		if raw == "" {
			return "{}"
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			b, _ := json.Marshal(parsed)
			return string(b)
		}
		return raw
	default:
		b, err := json.Marshal(t)
		if err != nil || len(bytes.TrimSpace(b)) == 0 || string(bytes.TrimSpace(b)) == "null" {
			return "{}"
		}
		return string(b)
	}
}

func firstStringFromMap(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if v, ok := m[key]; ok {
			if s := strings.TrimSpace(asString(v)); s != "" {
				return s
			}
		}
	}
	return ""
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		switch t := value.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(t) != "" {
				return t
			}
		default:
			return value
		}
	}
	return nil
}

func number(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return 0
	}
}
