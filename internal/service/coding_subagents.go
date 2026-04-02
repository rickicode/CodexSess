package service

import (
	"encoding/json"
	"strings"
)

type subagentIdentity struct {
	Nickname  string
	AgentType string
	Prompt    string
}

type subagentIdentityState struct {
	pendingByPrompt map[string]subagentIdentity
	pendingByCallID map[string]subagentIdentity
	byID            map[string]subagentIdentity
}

func normalizeSubagentIdentityKey(raw string) string {
	return strings.TrimSpace(raw)
}

func firstStringFromMap(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s := strings.TrimSpace(asString(v)); s != "" {
				return s
			}
		}
	}
	return ""
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

func extractStringSliceFromAny(v any) []string {
	switch t := v.(type) {
	case []string:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := strings.TrimSpace(asString(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func parseJSONStringMap(raw string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || (!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, false
	}
	return out, true
}

func parseSubagentIdentityFromToolCall(item map[string]any) (subagentIdentity, string, string, bool) {
	if item == nil {
		return subagentIdentity{}, "", "", false
	}
	itemType := strings.TrimSpace(strings.ToLower(asString(item["type"])))
	if !strings.Contains(itemType, "tool_call") {
		return subagentIdentity{}, "", "", false
	}
	toolName := strings.TrimSpace(strings.ToLower(extractToolName(item)))
	if toolName != "spawn_agent" {
		return subagentIdentity{}, "", "", false
	}
	argsRaw := extractToolArguments(item)
	args := map[string]any{}
	switch v := argsRaw.(type) {
	case map[string]any:
		args = v
	case string:
		if parsed, ok := parseJSONStringMap(v); ok {
			args = parsed
		}
	}
	nickname := strings.TrimSpace(asString(args["nickname"]))
	if nickname == "" {
		nickname = strings.TrimSpace(asString(args["name"]))
	}
	agentType := strings.TrimSpace(asString(args["agent_type"]))
	if agentType == "" {
		agentType = strings.TrimSpace(asString(args["agentType"]))
	}
	prompt := strings.TrimSpace(asString(args["message"]))
	if prompt == "" {
		prompt = strings.TrimSpace(asString(args["prompt"]))
	}
	if prompt == "" {
		prompt = strings.TrimSpace(asString(item["prompt"]))
	}
	if nickname == "" && agentType == "" {
		return subagentIdentity{}, prompt, strings.TrimSpace(firstStringFromMap(item, "call_id", "tool_call_id", "id")), false
	}
	return subagentIdentity{
		Nickname:  nickname,
		AgentType: agentType,
		Prompt:    prompt,
	}, prompt, strings.TrimSpace(firstStringFromMap(item, "call_id", "tool_call_id", "id")), true
}

func enrichSubagentEventRaw(raw string, state *subagentIdentityState) string {
	if state == nil {
		return raw
	}
	evt, ok := parseJSONStringMap(raw)
	if !ok || evt == nil {
		return raw
	}
	item, _ := evt["item"].(map[string]any)
	if item == nil {
		return raw
	}
	itemType := strings.TrimSpace(strings.ToLower(asString(item["type"])))
	toolName := strings.TrimSpace(strings.ToLower(extractToolName(item)))
	if toolName == "" {
		return raw
	}

	if meta, prompt, callID, ok := parseSubagentIdentityFromToolCall(item); ok {
		if state.pendingByPrompt == nil {
			state.pendingByPrompt = map[string]subagentIdentity{}
		}
		if state.pendingByCallID == nil {
			state.pendingByCallID = map[string]subagentIdentity{}
		}
		if key := normalizeSubagentIdentityKey(prompt); key != "" {
			state.pendingByPrompt[key] = meta
		}
		if callID != "" {
			state.pendingByCallID[callID] = meta
		}
	}

	if !strings.Contains(itemType, "tool_call") {
		return raw
	}
	if toolName != "spawn_agent" && toolName != "wait" && toolName != "wait_agent" {
		return raw
	}
	ids := extractStringSliceFromAny(item["receiver_thread_ids"])
	if len(ids) == 0 {
		return raw
	}
	prompt := strings.TrimSpace(asString(item["prompt"]))
	callID := strings.TrimSpace(firstStringFromMap(item, "call_id", "tool_call_id", "id"))

	if state.byID == nil {
		state.byID = map[string]subagentIdentity{}
	}
	lookupKey := normalizeSubagentIdentityKey(prompt)
	meta, ok := state.pendingByPrompt[lookupKey]
	if !ok && callID != "" {
		meta, ok = state.pendingByCallID[callID]
	}
	if ok {
		for _, id := range ids {
			if strings.TrimSpace(id) == "" {
				continue
			}
			state.byID[id] = meta
		}
	}
	for _, id := range ids {
		if _, exists := state.byID[id]; !exists {
			continue
		}
		if state.byID[id].Nickname == "" && state.byID[id].AgentType == "" {
			continue
		}
	}

	agentsStates, _ := item["agents_states"].(map[string]any)
	if agentsStates == nil {
		agentsStates = map[string]any{}
	}
	updated := false
	for _, id := range ids {
		meta, ok := state.byID[id]
		if !ok {
			continue
		}
		if meta.Nickname == "" && meta.AgentType == "" {
			continue
		}
		entry, _ := agentsStates[id].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		if meta.Nickname != "" {
			if _, exists := entry["nickname"]; !exists {
				entry["nickname"] = meta.Nickname
			}
		}
		if meta.AgentType != "" {
			if _, exists := entry["agent_type"]; !exists {
				entry["agent_type"] = meta.AgentType
			}
		}
		agentsStates[id] = entry
		updated = true
	}
	if updated {
		item["agents_states"] = agentsStates
		evt["item"] = item
		if b, err := json.Marshal(evt); err == nil {
			return string(b)
		}
	}
	return raw
}
