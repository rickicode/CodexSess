package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var skillPathPattern = regexp.MustCompile(`/skills/([^/]+)/SKILL\.md`)

func normalizeAppServerItemType(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "agentmessage", "agent_message":
		return "agentmessage"
	case "usermessage", "user_message":
		return "usermessage"
	case "commandexecution", "command_execution":
		return "command_execution"
	case "filechange", "file_change":
		return "file_change"
	case "fileread", "file_read":
		return "file_read"
	case "functioncall", "function_call":
		return "function_call"
	case "functioncalloutput", "function_call_output":
		return "function_call_output"
	default:
		return value
	}
}

func normalizeAppServerEventType(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "/", ".")
}

func codexEventDeltaText(evt map[string]any) (string, bool) {
	if evt == nil {
		return "", false
	}
	t, _ := evt["type"].(string)
	t = normalizeAppServerEventType(t)
	switch t {
	case "response.output_text.delta", "item.delta", "message.delta", "item.agentmessage.delta", "item.agent_message.delta", "item.message.delta":
		if v, _ := evt["delta"].(string); v != "" {
			return v, true
		}
		if item, _ := evt["item"].(map[string]any); item != nil {
			if v, _ := item["delta"].(string); v != "" {
				return v, true
			}
			if v, _ := item["text"].(string); v != "" {
				return v, true
			}
		}
	}
	return "", false
}

func codexEventActivityText(evt map[string]any) (string, bool) {
	if evt == nil {
		return "", false
	}
	t, _ := evt["type"].(string)
	t = normalizeAppServerEventType(t)
	item, _ := evt["item"].(map[string]any)
	itemType := ""
	if item != nil {
		itemType, _ = item["type"].(string)
		itemType = normalizeAppServerItemType(itemType)
		if itemType == "file_change" || t == "file_change" || itemType == "file_read" || t == "file_read" {
			return formatFileOperationActivityText(evt)
		}
		if itemType != "" &&
			itemType != "command_execution" &&
			itemType != "tool_call" &&
			itemType != "function_call" &&
			itemType != "exec_command" &&
			itemType != "collab_tool_call" {
			return "", false
		}
	}
	if shouldSuppressStructuredExecActivity(t, itemType, item) {
		return "", false
	}
	toolName := strings.ToLower(strings.TrimSpace(extractSubagentToolName(item)))
	if toolName == "wait" && itemType != "collab_tool_call" {
		toolName = ""
	}
	if isSubagentToolName(toolName) {
		if text := formatSubagentActivityText(toolName, t, item); text != "" {
			return text, true
		}
		return "", false
	}

	cmd := extractActivityCommand(item)
	if cmd == "" {
		// Some codex events carry command/tool metadata on top-level.
		cmd = extractActivityCommand(evt)
	}
	if cmd == "" {
		switch t {
		case "item.started", "item.updated", "tool.started", "tool.call.started":
			return "Running command", true
		case "item.completed", "rawresponseitem.completed", "tool.completed", "tool.call.completed":
			return "Command done", true
		default:
			return "", false
		}
	}

	if m := skillPathPattern.FindStringSubmatch(cmd); len(m) == 2 {
		return "Using skill: " + strings.TrimSpace(m[1]), true
	}
	if mcpText := formatMCPActivityText(t, item, cmd); mcpText != "" {
		return mcpText, true
	}

	switch t {
	case "item.started", "item.updated", "tool.started", "tool.call.started":
		return "Running: " + truncateActivityText(cmd), true
	case "item.completed", "rawresponseitem.completed", "tool.completed", "tool.call.completed":
		exitCode := int(number(item["exit_code"]))
		if exitCode != 0 {
			return fmt.Sprintf("Command failed (exit %d): %s", exitCode, truncateActivityText(cmd)), true
		}
		return "Command done: " + truncateActivityText(cmd), true
	default:
		return "", false
	}
}

func shouldSuppressStructuredExecActivity(eventType, itemType string, item map[string]any) bool {
	switch itemType {
	case "command_execution", "function_call", "exec_command":
	default:
		return false
	}
	cmd := extractActivityCommand(item)
	if strings.TrimSpace(cmd) == "" {
		return false
	}
	switch eventType {
	case "item.started", "item.updated", "item.completed", "rawresponseitem.completed", "tool.started", "tool.call.started", "tool.completed", "tool.call.completed":
		return true
	default:
		return false
	}
}

func formatFileOperationActivityText(evt map[string]any) (string, bool) {
	if evt == nil {
		return "", false
	}
	t, _ := evt["type"].(string)
	t = normalizeAppServerEventType(t)
	item, _ := evt["item"].(map[string]any)
	itemType := ""
	if item != nil {
		itemType, _ = item["type"].(string)
		itemType = normalizeAppServerItemType(itemType)
	}

	if itemType == "file_read" || t == "file_read" {
		path := firstNonEmpty(
			firstStringFromMap(item, "path"),
			firstStringFromMap(evt, "path"),
		)
		if path == "" {
			return "", false
		}
		return fmt.Sprintf("[Read %s]", truncateActivityText(path)), true
	}

	if itemType != "file_change" && t != "file_change" {
		return "", false
	}

	changes := mapSliceFromAny(item, evt)
	targets := make([]string, 0, len(changes))
	action := ""
	for _, change := range changes {
		changeMap, _ := change.(map[string]any)
		if changeMap == nil {
			continue
		}
		if action == "" {
			action = normalizeFileOperationActionName(firstNonEmpty(
				extractFileOperationActionName(changeMap["kind"]),
				extractFileOperationActionName(changeMap["action"]),
				extractFileOperationActionName(changeMap["type"]),
			))
		}
		target := firstNonEmpty(
			firstStringFromMap(changeMap, "path"),
			firstStringFromMap(changeMap, "file"),
			firstStringFromMap(changeMap, "target"),
		)
		if target == "" {
			continue
		}
		target = sanitizeSensitiveText(target)
		added := int(number(firstNonEmptyAny(
			changeMap["added_lines"],
			changeMap["lines_added"],
			changeMap["insertions"],
		)))
		deleted := int(number(firstNonEmptyAny(
			changeMap["deleted_lines"],
			changeMap["lines_deleted"],
			changeMap["deletions"],
		)))
		if added > 0 || deleted > 0 {
			target = fmt.Sprintf("%s (+%d -%d)", target, added, deleted)
		}
		targets = append(targets, target)
	}

	action = firstNonEmpty(action, "Edited")
	if len(targets) == 0 {
		return fmt.Sprintf("[%s]", action), true
	}
	uniqueTargets := uniqueStrings(targets)
	return fmt.Sprintf("[%s %s]", action, strings.Join(uniqueTargets, " | ")), true
}

func normalizeFileOperationActionName(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "read", "read_file", "open", "opened":
		return "Read"
	case "create", "created", "add", "added", "new":
		return "Created"
	case "delete", "deleted", "remove", "removed":
		return "Deleted"
	case "move", "moved":
		return "Moved"
	case "rename", "renamed":
		return "Renamed"
	case "edit", "edited", "update", "updated", "modify", "modified", "write", "written":
		return "Edited"
	default:
		return ""
	}
}

func extractFileOperationActionName(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		return firstNonEmpty(
			firstStringFromMap(v, "type"),
			firstStringFromMap(v, "kind"),
			firstStringFromMap(v, "action"),
			firstStringFromMap(v, "name"),
		)
	default:
		return ""
	}
}

func mapSliceFromAny(srcs ...map[string]any) []any {
	for _, src := range srcs {
		if src == nil {
			continue
		}
		if raw, ok := src["changes"]; ok {
			switch v := raw.(type) {
			case []any:
				return v
			case []map[string]any:
				out := make([]any, 0, len(v))
				for _, item := range v {
					out = append(out, item)
				}
				return out
			}
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func isSubagentToolName(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "spawn_agent", "wait_agent", "wait", "send_input", "resume_agent", "close_agent":
		return true
	default:
		return false
	}
}

func extractSubagentToolName(item map[string]any) string {
	if item == nil {
		return ""
	}
	if v, _ := item["tool"].(string); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(extractToolName(item))
}

func formatSubagentActivityText(toolName, eventType string, item map[string]any) string {
	args := extractToolArgumentMap(item)
	isCompleted := eventType == "item.completed" || eventType == "tool.completed" || eventType == "tool.call.completed"
	isStarted := eventType == "item.started" || eventType == "item.updated" || eventType == "tool.started" || eventType == "tool.call.started"

	switch toolName {
	case "spawn_agent":
		// Show spawn card once on completion to avoid started/completed duplication.
		if !isCompleted {
			return ""
		}
		nickname := firstStringFromMap(args, "nickname", "name")
		role := firstStringFromMap(args, "agent_type")
		task := firstStringFromMap(args, "message", "prompt")
		if task == "" {
			task = firstStringFromMap(item, "prompt")
		}
		header := "• Spawned subagent"
		if nickname != "" {
			header = "• Spawned " + nickname
		}
		if role != "" {
			header += " [" + role + "]"
		}
		details := make([]string, 0, 2)
		if nickname == "" {
			ids := extractStringSliceFromAny(item["receiver_thread_ids"])
			if len(ids) > 0 {
				details = append(details, truncateActivityText(ids[0]))
			}
		}
		if task != "" {
			details = append(details, truncateActivityText(task))
		}
		if len(details) == 0 {
			if summary := extractSubagentActivitySummary(item); summary != "" {
				details = append(details, truncateActivityText(summary))
			}
		}
		if len(details) > 0 {
			return header + "\n  └ " + strings.Join(details, "\n    ")
		}
		return header
	case "wait_agent", "wait":
		if isStarted {
			ids := extractStringSliceFromAny(args["ids"])
			if len(ids) == 0 {
				ids = extractStringSliceFromAny(item["receiver_thread_ids"])
			}
			if len(ids) > 0 {
				return fmt.Sprintf("• Waiting for %d agents\n  └ %s", len(ids), truncateActivityText(strings.Join(ids, ", ")))
			}
			return "• Waiting for agents"
		}
		if isCompleted {
			if summary := extractSubagentActivitySummary(item); summary != "" {
				return "• Subagent wait completed\n  └ " + truncateActivityText(summary)
			}
			return "• Subagent wait completed"
		}
	case "send_input":
		if isCompleted {
			target := firstStringFromMap(args, "id")
			if target != "" {
				return "• Sent input to subagent\n  └ " + truncateActivityText(target)
			}
			return "• Sent input to subagent"
		}
	case "resume_agent":
		if isCompleted {
			target := firstStringFromMap(args, "id")
			if target != "" {
				return "• Resumed subagent\n  └ " + truncateActivityText(target)
			}
			return "• Resumed subagent"
		}
	case "close_agent":
		if isCompleted {
			target := firstStringFromMap(args, "id")
			if target != "" {
				return "• Closed subagent\n  └ " + truncateActivityText(target)
			}
			return "• Closed subagent"
		}
	}
	return ""
}

func formatMCPActivityText(eventType string, item map[string]any, fallbackCommand string) string {
	args := extractToolArgumentMap(item)
	rawToolName := strings.TrimSpace(extractToolName(item))
	lowerToolName := strings.ToLower(rawToolName)
	lowerFallback := strings.ToLower(strings.TrimSpace(fallbackCommand))
	if !strings.HasPrefix(lowerToolName, "mcp__") &&
		!strings.Contains(lowerFallback, "mcp") &&
		firstStringFromMap(args, "server", "server_name", "mcp_server", "tool", "tool_name", "mcp_tool") == "" {
		return ""
	}

	serverName := ""
	toolName := ""
	if strings.HasPrefix(lowerToolName, "mcp__") {
		parts := strings.Split(rawToolName, "__")
		if len(parts) >= 2 {
			serverName = strings.TrimSpace(parts[1])
		}
		if len(parts) >= 3 {
			toolName = strings.TrimSpace(strings.Join(parts[2:], "__"))
		}
	}
	serverName = firstNonEmpty(
		serverName,
		firstStringFromMap(args, "server", "server_name", "mcp_server", "mcpServer"),
		firstStringFromMap(item, "server", "server_name", "mcp_server"),
	)
	toolName = firstNonEmpty(
		toolName,
		firstStringFromMap(args, "tool", "tool_name", "toolName", "mcp_tool", "mcpTool"),
	)

	target := rawToolName
	switch {
	case serverName != "" && toolName != "":
		target = serverName + "." + toolName
	case toolName != "":
		target = toolName
	case serverName != "":
		target = serverName
	case target == "":
		target = "call"
	}

	switch eventType {
	case "item.completed", "tool.completed", "tool.call.completed":
		if item != nil && item["error"] != nil {
			if summary := extractMCPActivitySummary(item, target, rawToolName); summary != "" {
				return "MCP failed: " + truncateActivityText(target) + "\n  └ " + truncateActivityText(summary)
			}
			return "MCP failed: " + truncateActivityText(target)
		}
		if summary := extractMCPActivitySummary(item, target, rawToolName); summary != "" {
			return "MCP done: " + truncateActivityText(target) + "\n  └ " + truncateActivityText(summary)
		}
		return "MCP done: " + truncateActivityText(target)
	case "item.started", "item.updated", "tool.started", "tool.call.started":
		return "Running MCP: " + truncateActivityText(target)
	default:
		return "Running MCP: " + truncateActivityText(target)
	}
}

func extractMCPActivitySummary(item map[string]any, target, rawToolName string) string {
	if item == nil {
		return ""
	}
	summary := strings.TrimSpace(extractSummaryFromAny(firstNonEmptyAny(
		item["output"],
		item["result"],
		item["response"],
		item["content"],
		item["data"],
		item["output_text"],
		item["text"],
	)))
	if summary == "" {
		return ""
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(summary), " "))
	if normalized == "" {
		return ""
	}
	if normalized == strings.ToLower(strings.TrimSpace(target)) {
		return ""
	}
	if normalized == strings.ToLower(strings.TrimSpace(rawToolName)) {
		return ""
	}
	return strings.Join(strings.Fields(summary), " ")
}

func extractToolArgumentMap(item map[string]any) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	if itemType, _ := item["type"].(string); strings.EqualFold(strings.TrimSpace(itemType), "collab_tool_call") {
		out := map[string]any{}
		if prompt, _ := item["prompt"].(string); strings.TrimSpace(prompt) != "" {
			out["prompt"] = strings.TrimSpace(prompt)
		}
		if ids := extractStringSliceFromAny(item["receiver_thread_ids"]); len(ids) > 0 {
			raw := make([]any, 0, len(ids))
			for _, id := range ids {
				raw = append(raw, id)
			}
			out["ids"] = raw
		}
		return out
	}
	raw := extractToolArguments(item)
	switch t := raw.(type) {
	case map[string]any:
		return t
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return map[string]any{}
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil && parsed != nil {
			return parsed
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

func extractSubagentActivitySummary(item map[string]any) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"output_text", "text"} {
		if v, _ := item[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, key := range []string{"output", "result", "response", "agents_states"} {
		if text := extractSummaryFromAny(item[key]); text != "" {
			return text
		}
	}
	return ""
}

func extractSummaryFromAny(v any) string {
	switch t := v.(type) {
	case string:
		raw := strings.TrimSpace(t)
		if raw == "" {
			return ""
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			return extractSummaryFromAny(parsed)
		}
		return raw
	case map[string]any:
		for _, key := range []string{"final_message", "message", "summary", "text", "status", "nickname", "agent_id", "id"} {
			if s, _ := t[key].(string); strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		for _, key := range []string{"result", "output", "data"} {
			if nested := extractSummaryFromAny(t[key]); nested != "" {
				return nested
			}
		}
		for _, v := range t {
			if nested := extractSummaryFromAny(v); nested != "" {
				return nested
			}
		}
	case []any:
		for _, item := range t {
			if nested := extractSummaryFromAny(item); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func extractActivityCommand(src map[string]any) string {
	if src == nil {
		return ""
	}
	args := extractToolArgumentMap(src)
	for _, key := range []string{"command", "cmd", "shell_command"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if fn, _ := src["function"].(map[string]any); fn != nil {
		if nestedArgs := extractToolArgumentMap(fn); len(nestedArgs) > 0 {
			for _, key := range []string{"command", "cmd", "shell_command"} {
				if v, ok := nestedArgs[key].(string); ok && strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
	}
	candidates := []string{"command", "cmd", "description", "tool", "tool_name", "name"}
	for _, key := range candidates {
		if v, _ := src[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if fn, _ := src["function"].(map[string]any); fn != nil {
		for _, key := range []string{"name", "tool_name"} {
			if v, _ := fn[key].(string); strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func truncateActivityText(s string) string {
	v := strings.TrimSpace(s)
	runes := []rune(v)
	if len(runes) <= 120 {
		return v
	}
	return string(runes[:120]) + "..."
}

func codexEventErrorMessage(evt map[string]any) string {
	if evt == nil {
		return ""
	}
	if t, _ := evt["type"].(string); normalizeAppServerEventType(t) == "error" {
		if msg, _ := evt["message"].(string); strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	if t, _ := evt["type"].(string); normalizeAppServerEventType(t) == "turn.failed" {
		if errObj, _ := evt["error"].(map[string]any); errObj != nil {
			if msg, _ := errObj["message"].(string); strings.TrimSpace(msg) != "" {
				return strings.TrimSpace(msg)
			}
		}
	}
	return ""
}

var (
	quotedSecretPattern = regexp.MustCompile(`(?i)("(?:(?:access|refresh|id)_token|api[_-]?key|authorization|anthropic_auth_token)"\s*:\s*")([^"]+)(")`)
	assignedSecretRegex = regexp.MustCompile(`(?i)\b((?:(?:access|refresh|id)_token|api[_-]?key|authorization|anthropic_auth_token)\s*=\s*)(\S+)`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]+`)
	skTokenPattern      = regexp.MustCompile(`\bsk-[A-Za-z0-9]{12,}\b`)
	homePathPattern     = regexp.MustCompile(`/home/[^/\s]+`)
)

func sanitizeSensitiveText(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	s = quotedSecretPattern.ReplaceAllString(s, `${1}[REDACTED]${3}`)
	s = assignedSecretRegex.ReplaceAllString(s, `${1}[REDACTED]`)
	s = bearerTokenPattern.ReplaceAllString(s, `Bearer [REDACTED]`)
	s = skTokenPattern.ReplaceAllString(s, `sk-[REDACTED]`)
	s = homePathPattern.ReplaceAllString(s, `/home/[user]`)
	return s
}
