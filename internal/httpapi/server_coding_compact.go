package httpapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

var (
	codingActivityFailedPattern  = regexp.MustCompile(`^Command failed \(exit (\d+)\):\s+(.+)$`)
	codingFileOpBracketPattern   = regexp.MustCompile(`^\[(Edited|Read|Created|Deleted|Moved|Renamed)\s+(.+)\]$`)
	codingFileOpTextPattern      = regexp.MustCompile(`^(Edited|Read|Created|Deleted|Moved|Renamed):\s+(.+)$`)
	codingResumeStartedPattern   = regexp.MustCompile(`^thread\.resume_started\s+role=([a-z_]+)(?:\s+thread_id=([^\s]+))?$`)
	codingResumeCompletedPattern = regexp.MustCompile(`^thread\.resume_completed\s+attempts?=(\d+)\s+role=([a-z_]+)(?:\s+thread_id=([^\s]+))?$`)
	codingResumeFailedPattern    = regexp.MustCompile(`^thread\.resume_failed\s+attempts?=(\d+)\s+role=([a-z_]+)(?:\s+thread_id=([^\s]+))?(?:\s+reason=(.+))?$`)
	codingRebootstrapPattern     = regexp.MustCompile(`^thread\.rebootstrap_started\s+role=([a-z_]+)\s+previous_thread_id=([^\s]+)$`)
	codingInterruptPattern       = regexp.MustCompile(`^turn\.interrupt_requested\s+role=([a-z_]+)$`)
	codingContinuePattern        = regexp.MustCompile(`^turn\.continue_started\s+role=([a-z_]+)(?:\s+thread_id=(.+))?$`)
	codingBashCommandPattern     = regexp.MustCompile(`^/(?:usr/bin/|bin/)?bash\s+-lc\s+(.+)$`)
)

const codingCompactRedactedText = "[redacted]"
const codingCompactRuntimeRecoveryRetryLimit = 5

type codingCompactBuilder struct {
	sequence           int
	lastAssistantID    string
	latestAssistantID  string
	lastExecKey        string
	execIndexByKey     map[string]int
	subagentIndexByKey map[string]int
	messages           []map[string]any
}

func newCodingCompactBuilder() *codingCompactBuilder {
	return &codingCompactBuilder{
		execIndexByKey:     make(map[string]int),
		subagentIndexByKey: make(map[string]int),
		messages:           make([]map[string]any, 0, 16),
	}
}

func (b *codingCompactBuilder) Seed(messages []map[string]any) {
	b.messages = make([]map[string]any, 0, len(messages))
	b.lastAssistantID = ""
	b.latestAssistantID = ""
	b.lastExecKey = ""
	b.execIndexByKey = make(map[string]int)
	b.subagentIndexByKey = make(map[string]int)
	for _, item := range messages {
		clone := make(map[string]any, len(item))
		for k, v := range item {
			clone[k] = v
		}
		b.messages = append(b.messages, clone)
		idx := len(b.messages) - 1
		switch strings.ToLower(strings.TrimSpace(stringFromAny(clone["role"]))) {
		case "assistant":
			b.lastAssistantID = stringFromAny(clone["id"])
			b.latestAssistantID = b.lastAssistantID
		default:
			b.lastAssistantID = ""
		}
		if strings.EqualFold(stringFromAny(clone["role"]), "exec") && strings.EqualFold(stringFromAny(clone["exec_status"]), "running") {
			key := normalizeCodingCompactCommandKey(stringFromAny(clone["exec_command"]))
			if key != "" {
				b.execIndexByKey[key] = idx
				b.lastExecKey = key
			}
		}
		if strings.EqualFold(stringFromAny(clone["role"]), "subagent") && strings.EqualFold(stringFromAny(clone["subagent_status"]), "running") {
			key := strings.TrimSpace(stringFromAny(clone["subagent_key"]))
			if key != "" {
				b.subagentIndexByKey[key] = idx
			}
		}
	}
	b.sequence = len(b.messages)
}

func (b *codingCompactBuilder) nextID(prefix string) string {
	b.sequence++
	return fmt.Sprintf("%s-%06d", prefix, b.sequence)
}

func normalizeCodingCompactCommandKey(command string) string {
	text := strings.TrimSpace(command)
	if text == "" {
		return ""
	}
	if m := codingBashCommandPattern.FindStringSubmatch(text); len(m) == 2 {
		text = strings.TrimSpace(m[1])
	}
	if (strings.HasPrefix(text, "\"") && strings.HasSuffix(text, "\"")) ||
		(strings.HasPrefix(text, "'") && strings.HasSuffix(text, "'")) {
		text = strings.TrimSpace(text[1 : len(text)-1])
	}
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

func normalizeCodingCompactPromptKey(prompt string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(prompt))), " ")
}

func normalizeCodingCompactItemType(raw string) string {
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

func normalizeCodingCompactEventType(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "/", ".")
}

func codingCompactSourceKey(entry map[string]any) string {
	return codingCompactSourceKeyFromParts(
		codingCompactSourceRole(entry),
		stringFromAny(entry["source_thread_id"]),
		stringFromAny(entry["source_turn_id"]),
		stringFromAny(entry["source_item_id"]),
		stringFromAny(entry["source_item_type"]),
	)
}

func codingCompactSourceRole(entry map[string]any) string {
	if boolFromAny(entry["internal_runner"]) {
		return "internal_runner"
	}
	return stringFromAny(entry["role"])
}

func codingCompactSourceKeyFromParts(role, threadID, turnID, itemID, itemType string) string {
	cleanItemID := strings.TrimSpace(itemID)
	if cleanItemID == "" {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{
		strings.ToLower(strings.TrimSpace(role)),
		strings.TrimSpace(threadID),
		strings.TrimSpace(turnID),
		cleanItemID,
		normalizeCodingCompactItemType(itemType),
	}, "|"))
}

func applyCodingCompactSourceIdentity(entry map[string]any, evt provider.ChatEvent) {
	if entry == nil {
		return
	}
	if value := strings.TrimSpace(evt.SourceEventType); value != "" {
		entry["source_event_type"] = value
	}
	if value := strings.TrimSpace(evt.SourceThreadID); value != "" {
		entry["source_thread_id"] = value
	}
	if value := strings.TrimSpace(evt.SourceTurnID); value != "" {
		entry["source_turn_id"] = value
	}
	if value := strings.TrimSpace(evt.SourceItemID); value != "" {
		entry["source_item_id"] = value
	}
	if value := strings.TrimSpace(evt.SourceItemType); value != "" {
		entry["source_item_type"] = value
	}
	if evt.EventSeq > 0 {
		entry["event_seq"] = evt.EventSeq
	}
}

func parseCodingCompactFileOp(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	if m := codingFileOpTextPattern.FindStringSubmatch(raw); len(m) == 3 {
		action := strings.TrimSpace(m[1])
		target := strings.TrimSpace(m[2])
		if action != "" && target != "" {
			return fmt.Sprintf("%s: %s", action, target)
		}
	}
	m := codingFileOpBracketPattern.FindStringSubmatch(raw)
	if len(m) != 3 {
		return ""
	}
	action := strings.TrimSpace(m[1])
	target := strings.TrimSpace(m[2])
	if action == "" || target == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s", action, target)
}

func shouldSuppressCodingCompactActivity(text string) bool {
	raw := strings.ToLower(strings.TrimSpace(text))
	if raw == "" {
		return false
	}
	if strings.HasPrefix(raw, "item/reasoning/") {
		return true
	}
	if strings.HasPrefix(raw, "file change:") {
		return true
	}
	if strings.HasPrefix(raw, "turn/diff/updated:") {
		return true
	}
	if parseCodingCompactFileOp(text) != "" {
		return true
	}
	return false
}

func normalizeCodingCompactFileAction(kind string) string {
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

func extractCodingCompactFileKind(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		return firstNonEmpty(
			stringFromAny(v["type"]),
			stringFromAny(v["kind"]),
			stringFromAny(v["action"]),
			stringFromAny(v["name"]),
		)
	default:
		return ""
	}
}

func parseCodingCompactFileOpEvent(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	eventType := normalizeCodingCompactEventType(stringFromAny(payload["type"]))
	item, _ := payload["item"].(map[string]any)
	itemType := normalizeCodingCompactItemType(stringFromAny(item["type"]))

	if strings.Contains(eventType, "compact") || strings.Contains(itemType, "compact") {
		summary := extractSummaryFromAnyCompact(item)
		if summary == "" {
			summary = extractSummaryFromAnyCompact(payload)
		}
		if summary == "" {
			return "Context compacted"
		}
		return "Context compacted\n  └ " + summary
	}

	if itemType == "file_read" || eventType == "file_read" {
		path := firstNonEmpty(stringFromAny(item["path"]), stringFromAny(payload["path"]))
		if path == "" {
			return "Read file"
		}
		return "Read: " + path
	}

	if itemType != "file_change" && eventType != "file_change" {
		return ""
	}

	var changes []any
	switch raw := item["changes"].(type) {
	case []any:
		changes = raw
	}
	if len(changes) == 0 {
		if raw, ok := payload["changes"].([]any); ok {
			changes = raw
		}
	}

	action := ""
	targets := make([]string, 0, len(changes))
	for _, rawChange := range changes {
		change, _ := rawChange.(map[string]any)
		if change == nil {
			continue
		}
		if action == "" {
			action = normalizeCodingCompactFileAction(firstNonEmpty(
				extractCodingCompactFileKind(change["kind"]),
				extractCodingCompactFileKind(change["action"]),
				extractCodingCompactFileKind(change["type"]),
			))
		}
		target := firstNonEmpty(
			stringFromAny(change["path"]),
			stringFromAny(change["file"]),
			stringFromAny(change["target"]),
		)
		added := intFromAny(change["added_lines"])
		if added == 0 {
			added = intFromAny(change["lines_added"])
		}
		if added == 0 {
			added = intFromAny(change["insertions"])
		}
		deleted := intFromAny(change["deleted_lines"])
		if deleted == 0 {
			deleted = intFromAny(change["lines_deleted"])
		}
		if deleted == 0 {
			deleted = intFromAny(change["deletions"])
		}
		if target != "" && (added > 0 || deleted > 0) {
			target = fmt.Sprintf("%s (+%d -%d)", target, added, deleted)
		}
		if target != "" {
			targets = append(targets, target)
		}
	}

	if action == "" {
		action = "Edited"
	}
	if len(targets) == 0 {
		return action
	}
	if len(targets) == 1 {
		return action + ": " + targets[0]
	}
	display := targets
	if len(display) > 3 {
		display = display[:3]
	}
	text := action + "\n  └ " + strings.Join(display, "\n  └ ")
	if len(targets) > 3 {
		text += fmt.Sprintf("\n  └ +%d more", len(targets)-3)
	}
	return text
}

func parseCodingCompactSubagentActivity(text string) map[string]string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil
	}
	if !strings.HasPrefix(raw, "• ") {
		lowerRaw := strings.ToLower(raw)
		isWaiting := strings.Contains(lowerRaw, "timeline event: `waiting`") || strings.Contains(lowerRaw, "timeline event: \"waiting\"")
		isCompleted := strings.Contains(lowerRaw, "timeline event: `completed`") || strings.Contains(lowerRaw, "timeline event: \"completed\"") || strings.Contains(lowerRaw, "the subagent finished")
		if !isWaiting && !isCompleted {
			return nil
		}
		status := "done"
		phase := "completed"
		title := "Subagent wait completed"
		target := raw
		if isWaiting {
			status = "running"
			phase = "started"
			title = "Waiting for agents"
			target = raw
		}
		return map[string]string{
			"title":   title,
			"status":  status,
			"summary": raw,
			"tool":    "wait_agent",
			"phase":   phase,
			"target":  target,
		}
	}
	lines := strings.Split(raw, "\n")
	title := strings.TrimSpace(strings.TrimPrefix(lines[0], "• "))
	if title == "" {
		return nil
	}
	lower := strings.ToLower(title)
	if !strings.HasPrefix(lower, "spawned") &&
		!strings.HasPrefix(lower, "waiting") &&
		!strings.HasPrefix(lower, "sent input to subagent") &&
		!strings.HasPrefix(lower, "resumed subagent") &&
		!strings.HasPrefix(lower, "closed subagent") &&
		!strings.HasPrefix(lower, "subagent wait completed") {
		return nil
	}
	status := "done"
	tool := ""
	phase := "completed"
	target := ""
	if strings.HasPrefix(lower, "waiting") {
		status = "running"
		phase = "started"
	}
	switch {
	case strings.HasPrefix(lower, "spawned"):
		tool = "spawn_agent"
		target = strings.TrimSpace(strings.TrimPrefix(title, "Spawned"))
		target = strings.TrimSpace(strings.TrimPrefix(target, "spawned"))
	case strings.HasPrefix(lower, "waiting"):
		tool = "wait_agent"
		target = strings.TrimSpace(strings.TrimPrefix(title, "Waiting"))
		target = strings.TrimSpace(strings.TrimPrefix(target, "waiting"))
	case strings.HasPrefix(lower, "subagent wait completed"):
		tool = "wait_agent"
		target = strings.TrimSpace(strings.TrimPrefix(title, "Subagent wait completed"))
		target = strings.TrimSpace(strings.TrimPrefix(target, ":"))
	case strings.HasPrefix(lower, "sent input to subagent"):
		tool = "send_input"
		target = title
	case strings.HasPrefix(lower, "resumed subagent"):
		tool = "resume_agent"
		target = title
	case strings.HasPrefix(lower, "closed subagent"):
		tool = "close_agent"
		target = title
	}
	summaryParts := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "└"))
		if clean != "" {
			summaryParts = append(summaryParts, clean)
		}
	}
	return map[string]string{
		"title":   title,
		"status":  status,
		"summary": strings.Join(summaryParts, "\n"),
		"tool":    tool,
		"phase":   phase,
		"target":  target,
	}
}

func parseCodingCompactMCPActivity(text string) map[string]any {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "mcp ") && !strings.HasPrefix(lower, "running mcp:") {
		return nil
	}
	entry := map[string]any{
		"text":    raw,
		"generic": strings.HasPrefix(lower, "mcp server status:"),
	}
	switch {
	case strings.HasPrefix(lower, "mcp done:"):
		entry["status"] = "done"
	case strings.HasPrefix(lower, "mcp failed:"):
		entry["status"] = "failed"
	default:
		entry["status"] = "running"
	}
	return entry
}

func parseCodingCompactArgs(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return v
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return map[string]any{}
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil && parsed != nil {
			return parsed
		}
	}
	return map[string]any{}
}

func inferCompactSubagentIdentityFromPrompt(prompt string) (nickname, role string) {
	text := strings.TrimSpace(prompt)
	if text == "" {
		return "", ""
	}
	if m := regexp.MustCompile(`(?i)\bnickname(?:\s+for\s+this\s+task|\s+anda)?\s*(?::|is|=)\s*([A-Za-z0-9._-]+)`).FindStringSubmatch(text); len(m) == 2 {
		nickname = strings.TrimSpace(m[1])
	}
	if nickname == "" {
		if m := regexp.MustCompile(`(?i)\bnicknamed\s+([A-Za-z0-9._-]+)`).FindStringSubmatch(text); len(m) == 2 {
			nickname = strings.TrimSpace(m[1])
		}
	}
	if nickname == "" {
		if m := regexp.MustCompile(`(?i)\byou are\s+([A-Za-z0-9._-]+)`).FindStringSubmatch(text); len(m) == 2 {
			nickname = strings.TrimSpace(m[1])
		}
	}
	nickname = strings.TrimRight(strings.TrimSpace(nickname), ".,:;!?")
	if m := regexp.MustCompile(`\[(.+?)\]`).FindStringSubmatch(text); len(m) == 2 {
		role = strings.TrimSpace(m[1])
	}
	return nickname, role
}

func firstStringFromAnyMap(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if m == nil || strings.TrimSpace(key) == "" {
			continue
		}
		if value, ok := m[key]; ok {
			if text := stringFromAny(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractStringSliceFromAnyCompact(v any) []string {
	switch t := v.(type) {
	case []string:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if clean := strings.TrimSpace(item); clean != "" {
				out = append(out, clean)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if clean := stringFromAny(item); clean != "" {
				out = append(out, clean)
			}
		}
		return out
	default:
		return nil
	}
}

func extractSummaryFromAnyCompact(v any) string {
	switch t := v.(type) {
	case string:
		raw := strings.TrimSpace(t)
		if raw == "" {
			return ""
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			return extractSummaryFromAnyCompact(parsed)
		}
		return raw
	case map[string]any:
		for _, key := range []string{"final_message", "message", "summary", "text", "status", "nickname", "agent_id", "id"} {
			if text := firstStringFromAnyMap(t, key); text != "" {
				return text
			}
		}
		for _, key := range []string{"result", "output", "data", "response"} {
			if nested := extractSummaryFromAnyCompact(t[key]); nested != "" {
				return nested
			}
		}
	case []any:
		for _, item := range t {
			if nested := extractSummaryFromAnyCompact(item); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func parseCodingCompactMCPEvent(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	eventType := normalizeCodingCompactEventType(stringFromAny(payload["type"]))
	item, _ := payload["item"].(map[string]any)
	itemType := normalizeCodingCompactItemType(stringFromAny(item["type"]))
	if itemType != "tool_call" && itemType != "function_call" && itemType != "collab_tool_call" {
		return nil
	}
	args := parseCodingCompactArgs(item["arguments"])
	if len(args) == 0 {
		args = parseCodingCompactArgs(item["input"])
	}
	if len(args) == 0 {
		args = parseCodingCompactArgs(item["params"])
	}
	if len(args) == 0 {
		if fn, _ := item["function"].(map[string]any); fn != nil {
			args = parseCodingCompactArgs(fn["arguments"])
			if len(args) == 0 {
				args = parseCodingCompactArgs(fn["input"])
			}
		}
	}
	fn, _ := item["function"].(map[string]any)
	rawToolName := firstNonEmpty(
		firstStringFromAnyMap(item, "tool", "tool_name", "name"),
		firstStringFromAnyMap(fn, "name", "tool_name"),
		firstStringFromAnyMap(args, "tool", "tool_name", "toolName", "mcp_tool", "mcpTool"),
	)
	lowerToolName := strings.ToLower(strings.TrimSpace(rawToolName))
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
		firstStringFromAnyMap(args, "server", "server_name", "mcp_server", "mcpServer"),
		firstStringFromAnyMap(item, "server", "server_name", "mcp_server"),
	)
	toolName = firstNonEmpty(
		toolName,
		firstStringFromAnyMap(args, "tool", "tool_name", "toolName", "mcp_tool", "mcpTool"),
	)
	if !strings.HasPrefix(lowerToolName, "mcp__") && serverName == "" && toolName == "" {
		return nil
	}
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
	status := "running"
	switch eventType {
	case "item.completed", "tool.completed", "tool.call.completed":
		if item != nil && item["error"] != nil {
			status = "failed"
		} else {
			status = "done"
		}
	}
	text := ""
	switch status {
	case "failed":
		text = "MCP failed: " + target
	case "done":
		text = "MCP done: " + target
	default:
		text = "Running MCP: " + target
	}
	var summarySource any
	for _, candidate := range []any{
		item["output"],
		item["result"],
		item["response"],
		item["content"],
		item["data"],
		item["output_text"],
		item["text"],
	} {
		switch value := candidate.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(value) == "" {
				continue
			}
		}
		summarySource = candidate
		break
	}
	summary := extractSummaryFromAnyCompact(summarySource)
	if summary != "" {
		normalizedSummary := strings.ToLower(strings.Join(strings.Fields(summary), " "))
		if normalizedSummary != strings.ToLower(strings.TrimSpace(target)) &&
			normalizedSummary != strings.ToLower(strings.TrimSpace(rawToolName)) {
			text += "\n  └ " + strings.Join(strings.Fields(summary), " ")
		}
	}
	return map[string]any{
		"text":   text,
		"status": status,
		"server": serverName,
		"tool":   toolName,
		"target": target,
	}
}

func parseCodingCompactSubagentEvent(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	eventType := normalizeCodingCompactEventType(stringFromAny(payload["type"]))
	item, _ := payload["item"].(map[string]any)
	itemType := normalizeCodingCompactItemType(stringFromAny(item["type"]))
	if itemType != "tool_call" && itemType != "function_call" && itemType != "collab_tool_call" {
		return nil
	}
	toolName := strings.ToLower(firstStringFromAnyMap(item, "tool", "tool_name", "name"))
	if toolName == "" {
		if fn, _ := item["function"].(map[string]any); fn != nil {
			toolName = strings.ToLower(firstStringFromAnyMap(fn, "name", "tool_name"))
		}
	}
	switch toolName {
	case "spawn_agent", "wait_agent", "wait", "send_input", "resume_agent", "close_agent":
	default:
		return nil
	}
	args := parseCodingCompactArgs(item["arguments"])
	if len(args) == 0 {
		args = parseCodingCompactArgs(item["input"])
	}
	if len(args) == 0 {
		args = parseCodingCompactArgs(item["params"])
	}
	if len(args) == 0 {
		args = parseCodingCompactArgs(item["payload"])
	}
	if len(args) == 0 {
		if fn, _ := item["function"].(map[string]any); fn != nil {
			args = parseCodingCompactArgs(fn["arguments"])
			if len(args) == 0 {
				args = parseCodingCompactArgs(fn["input"])
			}
		}
	}
	target := firstStringFromAnyMap(args, "id", "agent_id", "subagent_id", "thread_id", "receiver_thread_id")
	callID := firstStringFromAnyMap(item, "call_id", "tool_call_id", "id")
	nickname := firstStringFromAnyMap(args, "nickname")
	agentName := firstStringFromAnyMap(args, "name", "agent_name")
	role := firstStringFromAnyMap(args, "agent_type", "role", "type")
	model := firstStringFromAnyMap(args, "model")
	reasoning := firstStringFromAnyMap(args, "reasoning_effort", "reasoning")
	prompt := firstStringFromAnyMap(args, "message", "prompt")
	if inferredNickname, inferredRole := inferCompactSubagentIdentityFromPrompt(prompt); strings.TrimSpace(nickname) == "" && strings.TrimSpace(inferredNickname) != "" {
		nickname = inferredNickname
		if strings.TrimSpace(role) == "" {
			role = inferredRole
		}
	}
	ids := extractStringSliceFromAnyCompact(args["ids"])
	if len(ids) == 0 {
		ids = extractStringSliceFromAnyCompact(args["agent_ids"])
	}
	if len(ids) == 0 {
		ids = extractStringSliceFromAnyCompact(args["subagent_ids"])
	}
	if len(ids) == 0 {
		ids = extractStringSliceFromAnyCompact(args["receiver_thread_ids"])
	}
	if len(ids) == 0 {
		ids = extractStringSliceFromAnyCompact(item["receiver_thread_ids"])
	}
	summary := ""
	for _, key := range []string{"output_text", "text", "output", "result", "response", "agents_states"} {
		if nested := extractSummaryFromAnyCompact(item[key]); nested != "" {
			summary = nested
			break
		}
	}
	status := "done"
	if eventType == "item.started" || eventType == "item.updated" || eventType == "tool.started" || eventType == "tool.call.started" {
		status = "running"
	}
	title := "Subagent Activity"
	phase := toolName
	switch toolName {
	case "spawn_agent":
		title = "Spawned subagent"
		displayName := firstNonEmpty(nickname, agentName)
		if displayName != "" {
			title = "Spawned " + displayName
		}
		if role != "" {
			title += " [" + role + "]"
		}
	case "wait_agent", "wait":
		displayName := firstNonEmpty(nickname, agentName, target)
		if status == "running" {
			if displayName != "" {
				title = "Waiting for " + displayName
			} else {
				title = "Waiting for agents"
			}
		} else {
			if displayName != "" {
				title = "Finished waiting for " + displayName
			} else {
				title = "Subagent wait completed"
			}
		}
	case "send_input":
		title = "Sent input to subagent"
	case "resume_agent":
		title = "Resumed subagent"
	case "close_agent":
		title = "Closed subagent"
	}
	key := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		callID,
		strings.Join([]string{toolName, target, normalizeCodingCompactPromptKey(prompt), firstNonEmpty(nickname, agentName), role}, "|"),
	)))
	return map[string]any{
		"key":           key,
		"lifecycle_key": key,
		"call_id":       callID,
		"title":         title,
		"summary":       summary,
		"status":        status,
		"phase":         phase,
		"tool":          toolName,
		"target":        target,
		"ids":           ids,
		"prompt":        prompt,
		"nickname":      nickname,
		"name":          agentName,
		"role":          role,
		"model":         model,
		"reasoning":     reasoning,
		"raw":           payload,
	}
}

func parseCodingCompactActivity(text string) (kind, command string, exitCode int) {
	raw := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(raw, "Running: "):
		return "running", strings.TrimSpace(strings.TrimPrefix(raw, "Running: ")), 0
	case strings.HasPrefix(raw, "Command done: "):
		return "done", strings.TrimSpace(strings.TrimPrefix(raw, "Command done: ")), 0
	}
	if m := codingActivityFailedPattern.FindStringSubmatch(raw); len(m) == 3 {
		code, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		return "failed", strings.TrimSpace(m[2]), code
	}
	return "other", "", 0
}

func normalizeCodingCompactRuntimeRoleLabel(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "executor":
		return "executor"
	case "chat":
		return "chat"
	default:
		return strings.TrimSpace(strings.ToLower(role))
	}
}

func parseCodingCompactRuntimeRecoveryActivity(text string) map[string]any {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil
	}
	if m := codingResumeStartedPattern.FindStringSubmatch(raw); len(m) == 3 {
		role := normalizeCodingCompactRuntimeRoleLabel(m[1])
		threadID := strings.TrimSpace(m[2])
		content := fmt.Sprintf("Resuming %s thread", firstNonEmpty(role, "runtime"))
		if threadID != "" {
			content += ": " + threadID
		}
		return map[string]any{
			"content":         content,
			"recovery_kind":   "resume_started",
			"recovery_role":   role,
			"recovery_thread": threadID,
		}
	}
	if m := codingResumeCompletedPattern.FindStringSubmatch(raw); len(m) == 4 {
		attempts, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		role := normalizeCodingCompactRuntimeRoleLabel(m[2])
		threadID := strings.TrimSpace(m[3])
		content := fmt.Sprintf("Resume completed for %s thread", firstNonEmpty(role, "runtime"))
		if threadID != "" {
			content += ": " + threadID
		}
		if attempts > 0 {
			suffix := "s"
			if attempts == 1 {
				suffix = ""
			}
			content += fmt.Sprintf(" after %d attempt%s", attempts, suffix)
		}
		return map[string]any{
			"content":           content,
			"recovery_kind":     "resume_completed",
			"recovery_role":     role,
			"recovery_attempts": attempts,
			"recovery_thread":   threadID,
		}
	}
	if m := codingResumeFailedPattern.FindStringSubmatch(raw); len(m) == 5 {
		attempts, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		role := normalizeCodingCompactRuntimeRoleLabel(m[2])
		threadID := strings.TrimSpace(m[3])
		reason := strings.TrimSpace(m[4])
		content := fmt.Sprintf("Resume failed for %s thread", firstNonEmpty(role, "runtime"))
		if threadID != "" {
			content += ": " + threadID
		}
		if attempts > 0 {
			suffix := "s"
			if attempts == 1 {
				suffix = ""
			}
			content += fmt.Sprintf(" after %d attempt%s", attempts, suffix)
		}
		if reason != "" {
			content += " (" + reason + ")"
		}
		return map[string]any{
			"content":           content,
			"recovery_kind":     "resume_failed",
			"recovery_role":     role,
			"recovery_attempts": attempts,
			"recovery_thread":   threadID,
			"recovery_reason":   reason,
		}
	}
	if m := codingRebootstrapPattern.FindStringSubmatch(raw); len(m) == 3 {
		role := normalizeCodingCompactRuntimeRoleLabel(m[1])
		threadID := strings.TrimSpace(m[2])
		content := fmt.Sprintf("Starting a fresh %s thread after resume failure", firstNonEmpty(role, "runtime"))
		if threadID != "" {
			content += ": " + threadID
		}
		return map[string]any{
			"content":           content,
			"recovery_kind":     "rebootstrap_started",
			"recovery_role":     role,
			"recovery_previous": threadID,
		}
	}
	if m := codingInterruptPattern.FindStringSubmatch(raw); len(m) == 2 {
		role := normalizeCodingCompactRuntimeRoleLabel(m[1])
		return map[string]any{
			"content":       fmt.Sprintf("Interrupt requested for %s runtime", firstNonEmpty(role, "runtime")),
			"recovery_kind": "interrupt_requested",
			"recovery_role": role,
		}
	}
	if m := codingContinuePattern.FindStringSubmatch(raw); len(m) == 3 {
		role := normalizeCodingCompactRuntimeRoleLabel(m[1])
		threadID := strings.TrimSpace(m[2])
		content := fmt.Sprintf("Continuing %s runtime after recovery", firstNonEmpty(role, "runtime"))
		if threadID != "" {
			content += ": " + threadID
		}
		return map[string]any{
			"content":         content,
			"recovery_kind":   "continue_started",
			"recovery_role":   role,
			"recovery_thread": threadID,
		}
	}
	return nil
}

func decodeCodingCompactRawEvent(text string) map[string]any {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	if method := strings.TrimSpace(stringFromAny(payload["method"])); method != "" {
		params, _ := payload["params"].(map[string]any)
		flattened := make(map[string]any, len(params)+1)
		flattened["type"] = method
		for key, value := range params {
			flattened[key] = value
		}
		return flattened
	}
	return payload
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func boolFromAny(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func parseCodingCompactExecEvent(payload map[string]any) (command, status, output string, exitCode int) {
	if payload == nil {
		return "", "", "", 0
	}
	eventType := normalizeCodingCompactEventType(stringFromAny(payload["type"]))
	item, _ := payload["item"].(map[string]any)
	itemType := normalizeCodingCompactItemType(stringFromAny(item["type"]))
	fn, _ := item["function"].(map[string]any)
	toolName := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		stringFromAny(item["tool"]),
		stringFromAny(item["tool_name"]),
		stringFromAny(item["name"]),
		stringFromAny(fn["name"]),
	)))
	isExplicitExec := itemType == "command_execution" ||
		itemType == "exec_command" ||
		((itemType == "function_call" || itemType == "tool_call") && toolName == "exec_command")
	if itemType != "" && !isExplicitExec {
		return "", "", "", 0
	}
	command = stringFromAny(item["command"])
	if command == "" {
		command = stringFromAny(payload["command"])
	}
	if command == "" {
		args := parseCodingCompactArgs(item["arguments"])
		if len(args) == 0 {
			args = parseCodingCompactArgs(item["input"])
		}
		if len(args) == 0 {
			args = parseCodingCompactArgs(item["params"])
		}
		if len(args) == 0 {
			args = parseCodingCompactArgs(item["payload"])
		}
		if len(args) > 0 {
			command = firstStringFromAnyMap(args, "command", "cmd", "shell_command")
		}
	}
	if command == "" && fn != nil {
		command = firstStringFromAnyMap(fn, "command", "cmd")
		if command == "" {
			args := parseCodingCompactArgs(fn["arguments"])
			if len(args) == 0 {
				args = parseCodingCompactArgs(fn["input"])
			}
			command = firstStringFromAnyMap(args, "command", "cmd", "shell_command")
		}
	}
	if command == "" {
		return "", "", "", 0
	}
	switch eventType {
	case "item.started", "item.updated", "tool.started", "tool.call.started":
		status = "running"
	case "item.completed", "rawresponseitem.completed", "tool.completed", "tool.call.completed":
		exitCode = intFromAny(item["exit_code"])
		if exitCode == 0 {
			exitCode = intFromAny(item["exitCode"])
		}
		if exitCode != 0 {
			status = "failed"
		} else {
			status = "done"
		}
	default:
		status = "running"
	}
	for _, key := range []string{"aggregated_output", "aggregatedOutput", "output", "text"} {
		if v := stringFromAny(item[key]); v != "" {
			output = v
			break
		}
	}
	if output == "" {
		for _, key := range []string{"aggregated_output", "aggregatedOutput", "output", "text"} {
			if v := stringFromAny(payload[key]); v != "" {
				output = v
				break
			}
		}
	}
	return command, status, sanitizeCodingCompactExecOutput(output), exitCode
}

func mergeCodingCompactOutput(prev, next string) string {
	a := strings.TrimSpace(prev)
	b := strings.TrimSpace(next)
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case strings.Contains(a, b):
		return a
	default:
		return a + "\n" + b
	}
}

func codingCompactRecentEnough(prev, next string, window time.Duration) bool {
	if strings.TrimSpace(prev) == "" || strings.TrimSpace(next) == "" {
		return false
	}
	a, errA := time.Parse(time.RFC3339Nano, prev)
	b, errB := time.Parse(time.RFC3339Nano, next)
	if errA != nil || errB != nil {
		return false
	}
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= window
}

func (b *codingCompactBuilder) appendActivity(content, fileOp, actor, createdAt string) {
	entry := map[string]any{
		"id":         b.nextID("activity"),
		"role":       "activity",
		"actor":      strings.TrimSpace(actor),
		"content":    content,
		"created_at": createdAt,
		"updated_at": createdAt,
		"pending":    false,
	}
	if fileOp != "" {
		prev := map[string]any(nil)
		if len(b.messages) > 0 {
			prev = b.messages[len(b.messages)-1]
		}
		if prev != nil && strings.EqualFold(stringFromAny(prev["role"]), "activity") && strings.EqualFold(stringFromAny(prev["file_op"]), fileOp) {
			prev["updated_at"] = createdAt
			if strings.TrimSpace(actor) != "" {
				prev["actor"] = strings.TrimSpace(actor)
			}
			return
		}
		entry["file_op"] = fileOp
		entry["content"] = fileOp
	}
	b.messages = append(b.messages, entry)
	b.lastAssistantID = ""
}

func codingCompactActorUsesInternalRunnerRows(actor string) bool {
	return strings.EqualFold(strings.TrimSpace(actor), "internal_runner")
}

func codingCompactActorCanEmitExec(actor string) bool {
	switch strings.ToLower(strings.TrimSpace(actor)) {
	case "", "chat", "executor":
		return true
	default:
		return false
	}
}

func formatCodingCompactSupervisorExecActivity(actor, command, status string, exitCode int) string {
	label := strings.TrimSpace(actor)
	if label == "" {
		label = "supervisor"
	}
	label = strings.Title(strings.ToLower(label))
	cmd := strings.TrimSpace(command)
	state := strings.TrimSpace(strings.ToLower(status))
	switch state {
	case "running":
		return fmt.Sprintf("%s reviewed command activity: %s", label, cmd)
	case "failed":
		if exitCode != 0 {
			return fmt.Sprintf("%s command activity failed (exit %d): %s", label, exitCode, cmd)
		}
		return fmt.Sprintf("%s command activity failed: %s", label, cmd)
	default:
		return fmt.Sprintf("%s completed command activity: %s", label, cmd)
	}
}

func (b *codingCompactBuilder) resumableAssistantIndex(actor string) int {
	if strings.TrimSpace(b.lastAssistantID) == "" {
		return -1
	}
	for idx := len(b.messages) - 1; idx >= 0; idx-- {
		entry := b.messages[idx]
		role := strings.ToLower(strings.TrimSpace(stringFromAny(entry["role"])))
		switch role {
		case "assistant":
			if stringFromAny(entry["id"]) == b.lastAssistantID {
				if codingCompactSourceKey(entry) != "" {
					return -1
				}
				nextActor := strings.TrimSpace(actor)
				if nextActor != "" && !strings.EqualFold(stringFromAny(entry["actor"]), nextActor) {
					return -1
				}
				return idx
			}
			return -1
		case "activity":
			if strings.TrimSpace(stringFromAny(entry["file_op"])) != "" {
				return -1
			}
			kind, command, _ := parseCodingCompactActivity(stringFromAny(entry["content"]))
			if kind != "other" || command != "" {
				return -1
			}
			continue
		default:
			return -1
		}
	}
	return -1
}

func (b *codingCompactBuilder) resumableAssistantIndexBySourceIdentity(turnID, itemID, itemType string) int {
	targetKey := codingCompactSourceKeyFromParts("assistant", "", turnID, itemID, itemType)
	if targetKey == "" {
		return -1
	}
	for idx := len(b.messages) - 1; idx >= 0; idx-- {
		entry := b.messages[idx]
		if !strings.EqualFold(stringFromAny(entry["role"]), "assistant") {
			continue
		}
		if codingCompactSourceKey(entry) == targetKey {
			return idx
		}
	}
	return -1
}

func (b *codingCompactBuilder) resumableInternalRunnerIndex(actor string) int {
	normalizedActor := strings.TrimSpace(actor)
	if normalizedActor == "" {
		return -1
	}
	for idx := len(b.messages) - 1; idx >= 0; idx-- {
		entry := b.messages[idx]
		role := strings.ToLower(strings.TrimSpace(stringFromAny(entry["role"])))
		switch role {
		case "activity":
			if !boolFromAny(entry["internal_runner"]) {
				if strings.TrimSpace(stringFromAny(entry["file_op"])) != "" {
					return -1
				}
				continue
			}
			if codingCompactSourceKey(entry) != "" {
				return -1
			}
			if !strings.EqualFold(stringFromAny(entry["actor"]), normalizedActor) {
				return -1
			}
			return idx
		case "assistant":
			return -1
		default:
			if strings.TrimSpace(stringFromAny(entry["file_op"])) != "" {
				return -1
			}
			continue
		}
	}
	return -1
}

func (b *codingCompactBuilder) resumableInternalRunnerIndexBySourceIdentity(threadID, turnID, itemID, itemType string) int {
	targetKey := codingCompactSourceKeyFromParts("internal_runner", threadID, turnID, itemID, itemType)
	if targetKey == "" {
		return -1
	}
	for idx := len(b.messages) - 1; idx >= 0; idx-- {
		entry := b.messages[idx]
		if !boolFromAny(entry["internal_runner"]) {
			continue
		}
		if codingCompactSourceKey(entry) == targetKey {
			return idx
		}
	}
	return -1
}

func (b *codingCompactBuilder) knownSubagentDisplayName(target string, ids []string) string {
	target = strings.TrimSpace(target)
	idSet := map[string]struct{}{}
	for _, id := range ids {
		clean := strings.TrimSpace(id)
		if clean != "" {
			idSet[clean] = struct{}{}
		}
	}
	for idx := len(b.messages) - 1; idx >= 0; idx-- {
		entry := b.messages[idx]
		if !strings.EqualFold(stringFromAny(entry["role"]), "subagent") {
			continue
		}
		if target != "" && strings.EqualFold(stringFromAny(entry["subagent_target_id"]), target) {
			displayName := firstNonEmpty(stringFromAny(entry["subagent_nickname"]), stringFromAny(entry["subagent_name"]))
			if strings.TrimSpace(displayName) == "" {
				inferredNickname, _ := inferCompactSubagentIdentityFromPrompt(stringFromAny(entry["subagent_prompt"]))
				displayName = inferredNickname
			}
			if strings.TrimSpace(displayName) != "" {
				return displayName
			}
		}
		rawIDs, _ := entry["subagent_ids"].([]string)
		for _, existingID := range rawIDs {
			if _, ok := idSet[strings.TrimSpace(existingID)]; ok {
				displayName := firstNonEmpty(stringFromAny(entry["subagent_nickname"]), stringFromAny(entry["subagent_name"]))
				if strings.TrimSpace(displayName) == "" {
					inferredNickname, _ := inferCompactSubagentIdentityFromPrompt(stringFromAny(entry["subagent_prompt"]))
					displayName = inferredNickname
				}
				if strings.TrimSpace(displayName) != "" {
					return displayName
				}
			}
		}
	}
	for idx := len(b.messages) - 1; idx >= 0; idx-- {
		entry := b.messages[idx]
		if !strings.EqualFold(stringFromAny(entry["role"]), "subagent") {
			continue
		}
		if !strings.EqualFold(stringFromAny(entry["subagent_tool"]), "spawn_agent") {
			continue
		}
		displayName := firstNonEmpty(stringFromAny(entry["subagent_nickname"]), stringFromAny(entry["subagent_name"]))
		if strings.TrimSpace(displayName) == "" {
			inferredNickname, _ := inferCompactSubagentIdentityFromPrompt(stringFromAny(entry["subagent_prompt"]))
			displayName = inferredNickname
		}
		if strings.TrimSpace(displayName) != "" {
			return displayName
		}
	}
	return ""
}

func (b *codingCompactBuilder) upsertSubagent(evt provider.ChatEvent, key, lifecycleKey, title, summary, status, phase, tool, target, prompt, nickname, agentName, role, model, reasoning string, ids []string, _ any, createdAt string) {
	normalizedActor := strings.TrimSpace(evt.Actor)
	if idx, ok := b.subagentIndexByKey[key]; ok && idx >= 0 && idx < len(b.messages) && strings.EqualFold(stringFromAny(b.messages[idx]["role"]), "subagent") {
		entry := b.messages[idx]
		if (strings.EqualFold(tool, "wait_agent") || strings.EqualFold(tool, "wait")) && !strings.EqualFold(status, "running") {
			knownName := b.knownSubagentDisplayName(target, ids)
			displayName := firstNonEmpty(
				nickname,
				agentName,
				stringFromAny(entry["subagent_nickname"]),
				stringFromAny(entry["subagent_name"]),
				knownName,
				target,
				stringFromAny(entry["subagent_target_id"]),
			)
			if strings.TrimSpace(displayName) != "" {
				title = "Finished waiting for " + displayName
			}
		}
		entry["updated_at"] = createdAt
		if normalizedActor != "" {
			entry["actor"] = normalizedActor
		}
		entry["subagent_status"] = firstNonEmpty(status, stringFromAny(entry["subagent_status"]), "done")
		entry["subagent_phase"] = firstNonEmpty(phase, stringFromAny(entry["subagent_phase"]))
		entry["subagent_key"] = firstNonEmpty(key, stringFromAny(entry["subagent_key"]))
		entry["subagent_lifecycle_key"] = firstNonEmpty(lifecycleKey, stringFromAny(entry["subagent_lifecycle_key"]))
		entry["subagent_title"] = firstNonEmpty(title, stringFromAny(entry["subagent_title"]), "Subagent Activity")
		entry["content"] = firstNonEmpty(title, stringFromAny(entry["content"]), "Subagent Activity")
		if strings.TrimSpace(summary) != "" {
			entry["subagent_summary"] = summary
		}
		if strings.TrimSpace(tool) != "" {
			entry["subagent_tool"] = tool
		}
		if strings.TrimSpace(target) != "" {
			entry["subagent_target_id"] = target
		}
		if strings.TrimSpace(prompt) != "" {
			entry["subagent_prompt"] = prompt
		}
		if strings.TrimSpace(nickname) != "" {
			entry["subagent_nickname"] = nickname
		}
		if strings.TrimSpace(agentName) != "" {
			entry["subagent_name"] = agentName
		}
		if strings.TrimSpace(role) != "" {
			entry["subagent_role"] = role
		}
		if strings.TrimSpace(model) != "" {
			entry["subagent_model"] = model
		}
		if strings.TrimSpace(reasoning) != "" {
			entry["subagent_reasoning"] = reasoning
		}
		if len(ids) > 0 {
			entry["subagent_ids"] = ids
		}
		applyCodingCompactSourceIdentity(entry, evt)
		if status != "running" {
			delete(b.subagentIndexByKey, key)
		}
		b.lastAssistantID = ""
		return
	}
	if (strings.EqualFold(tool, "wait_agent") || strings.EqualFold(tool, "wait")) && !strings.EqualFold(status, "running") {
		for idx := len(b.messages) - 1; idx >= 0; idx-- {
			entry := b.messages[idx]
			if !strings.EqualFold(stringFromAny(entry["role"]), "subagent") {
				continue
			}
			if !strings.EqualFold(stringFromAny(entry["subagent_tool"]), tool) {
				continue
			}
			if !strings.EqualFold(stringFromAny(entry["subagent_status"]), "running") {
				continue
			}
			if target != "" && strings.EqualFold(stringFromAny(entry["subagent_target_id"]), target) {
				b.subagentIndexByKey[key] = idx
				b.upsertSubagent(evt, key, lifecycleKey, title, summary, status, phase, tool, target, prompt, nickname, agentName, role, model, reasoning, ids, nil, createdAt)
				return
			}
		}
	}
	if strings.EqualFold(tool, "spawn_agent") && !strings.EqualFold(status, "running") {
		incomingPrompt := normalizeCodingCompactPromptKey(prompt)
		if incomingPrompt != "" {
			for idx := len(b.messages) - 1; idx >= 0; idx-- {
				entry := b.messages[idx]
				if !strings.EqualFold(stringFromAny(entry["role"]), "subagent") {
					continue
				}
				if !strings.EqualFold(stringFromAny(entry["subagent_tool"]), "spawn_agent") {
					continue
				}
				if !strings.EqualFold(stringFromAny(entry["subagent_status"]), "running") {
					continue
				}
				if normalizeCodingCompactPromptKey(stringFromAny(entry["subagent_prompt"])) != incomingPrompt {
					continue
				}
				b.subagentIndexByKey[key] = idx
				b.upsertSubagent(evt, key, lifecycleKey, title, summary, status, phase, tool, target, prompt, nickname, agentName, role, model, reasoning, ids, nil, createdAt)
				return
			}
		}
	}
	entry := map[string]any{
		"id":                     b.nextID("subagent"),
		"role":                   "subagent",
		"actor":                  normalizedActor,
		"content":                firstNonEmpty(title, "Subagent Activity"),
		"created_at":             createdAt,
		"updated_at":             createdAt,
		"pending":                false,
		"subagent_status":        firstNonEmpty(status, "done"),
		"subagent_phase":         phase,
		"subagent_key":           key,
		"subagent_lifecycle_key": lifecycleKey,
		"subagent_title":         firstNonEmpty(title, "Subagent Activity"),
		"subagent_tool":          tool,
		"subagent_ids":           ids,
		"subagent_target_id":     target,
		"subagent_nickname":      nickname,
		"subagent_name":          agentName,
		"subagent_role":          role,
		"subagent_model":         model,
		"subagent_reasoning":     reasoning,
		"subagent_prompt":        prompt,
		"subagent_summary":       summary,
	}
	applyCodingCompactSourceIdentity(entry, evt)
	b.messages = append(b.messages, entry)
	if key != "" && status == "running" {
		b.subagentIndexByKey[key] = len(b.messages) - 1
	}
	b.lastAssistantID = ""
}

func (b *codingCompactBuilder) upsertAssistant(evt provider.ChatEvent, content, createdAt string, appendDelta bool) {
	actor := evt.Actor
	if appendDelta {
		if idx := b.resumableAssistantIndexBySourceIdentity(evt.SourceTurnID, evt.SourceItemID, evt.SourceItemType); idx >= 0 && idx < len(b.messages) {
			last := b.messages[idx]
			if strings.EqualFold(stringFromAny(last["role"]), "assistant") {
				current := stringFromAny(last["content"])
				nextActor := strings.TrimSpace(actor)
				if nextActor == "" {
					nextActor = stringFromAny(last["actor"])
				}
				last["content"] = current + content
				last["actor"] = nextActor
				last["updated_at"] = createdAt
				last["pending"] = true
				applyCodingCompactSourceIdentity(last, evt)
				b.lastAssistantID = stringFromAny(last["id"])
				b.latestAssistantID = b.lastAssistantID
				return
			}
		}
		if codingCompactSourceKeyFromParts("assistant", evt.SourceThreadID, evt.SourceTurnID, evt.SourceItemID, evt.SourceItemType) == "" {
			if idx := b.resumableAssistantIndex(actor); idx >= 0 && idx < len(b.messages) {
				last := b.messages[idx]
				if strings.EqualFold(stringFromAny(last["role"]), "assistant") {
					current := stringFromAny(last["content"])
					nextActor := strings.TrimSpace(actor)
					if nextActor == "" {
						nextActor = stringFromAny(last["actor"])
					}
					last["content"] = current + content
					last["actor"] = nextActor
					last["updated_at"] = createdAt
					last["pending"] = true
					applyCodingCompactSourceIdentity(last, evt)
					b.latestAssistantID = b.lastAssistantID
					return
				}
			}
		}
	}
	id := b.nextID("assistant")
	b.lastAssistantID = id
	b.latestAssistantID = id
	entry := map[string]any{
		"id":         id,
		"role":       "assistant",
		"actor":      strings.TrimSpace(actor),
		"content":    content,
		"created_at": createdAt,
		"updated_at": createdAt,
		"pending":    appendDelta,
	}
	applyCodingCompactSourceIdentity(entry, evt)
	b.messages = append(b.messages, entry)
}

func (b *codingCompactBuilder) upsertInternalRunnerOutput(evt provider.ChatEvent, content, createdAt string, appendDelta bool) {
	actor := evt.Actor
	if appendDelta {
		if idx := b.resumableInternalRunnerIndexBySourceIdentity(evt.SourceThreadID, evt.SourceTurnID, evt.SourceItemID, evt.SourceItemType); idx >= 0 && idx < len(b.messages) {
			last := b.messages[idx]
			current := stringFromAny(last["content"])
			last["content"] = current + content
			last["actor"] = strings.TrimSpace(actor)
			last["updated_at"] = createdAt
			last["pending"] = true
			last["internal_runner"] = true
			applyCodingCompactSourceIdentity(last, evt)
			b.lastAssistantID = ""
			return
		}
		if codingCompactSourceKeyFromParts("internal_runner", evt.SourceThreadID, evt.SourceTurnID, evt.SourceItemID, evt.SourceItemType) == "" {
			if idx := b.resumableInternalRunnerIndex(actor); idx >= 0 && idx < len(b.messages) {
				last := b.messages[idx]
				current := stringFromAny(last["content"])
				last["content"] = current + content
				last["actor"] = strings.TrimSpace(actor)
				last["updated_at"] = createdAt
				last["pending"] = true
				last["internal_runner"] = true
				applyCodingCompactSourceIdentity(last, evt)
				b.lastAssistantID = ""
				return
			}
		}
	} else {
		if idx := b.resumableInternalRunnerIndexBySourceIdentity(evt.SourceThreadID, evt.SourceTurnID, evt.SourceItemID, evt.SourceItemType); idx >= 0 && idx < len(b.messages) {
			last := b.messages[idx]
			last["content"] = content
			last["actor"] = strings.TrimSpace(actor)
			last["updated_at"] = createdAt
			last["pending"] = false
			last["internal_runner"] = true
			applyCodingCompactSourceIdentity(last, evt)
			b.lastAssistantID = ""
			return
		}
		if codingCompactSourceKeyFromParts("internal_runner", evt.SourceThreadID, evt.SourceTurnID, evt.SourceItemID, evt.SourceItemType) == "" {
			if idx := b.resumableInternalRunnerIndex(actor); idx >= 0 && idx < len(b.messages) {
				last := b.messages[idx]
				last["content"] = content
				last["actor"] = strings.TrimSpace(actor)
				last["updated_at"] = createdAt
				last["pending"] = false
				last["internal_runner"] = true
				applyCodingCompactSourceIdentity(last, evt)
				b.lastAssistantID = ""
				return
			}
		}
	}
	entry := map[string]any{
		"id":              b.nextID("activity"),
		"role":            "activity",
		"actor":           strings.TrimSpace(actor),
		"content":         content,
		"created_at":      createdAt,
		"updated_at":      createdAt,
		"pending":         appendDelta,
		"internal_runner": true,
	}
	applyCodingCompactSourceIdentity(entry, evt)
	b.messages = append(b.messages, entry)
	b.lastAssistantID = ""
}

func (b *codingCompactBuilder) upsertExec(evt provider.ChatEvent, command, status string, exitCode int, output, createdAt string) {
	actor := evt.Actor
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return
	}
	normalizedActor := strings.TrimSpace(actor)
	key := normalizeCodingCompactCommandKey(cmd)
	idx, ok := b.execIndexByKey[key]
	if (!ok || idx < 0 || idx >= len(b.messages) || !strings.EqualFold(stringFromAny(b.messages[idx]["role"]), "exec")) && status != "running" {
		for i := len(b.messages) - 1; i >= 0; i-- {
			entry := b.messages[i]
			if !strings.EqualFold(stringFromAny(entry["role"]), "exec") {
				continue
			}
			if normalizeCodingCompactCommandKey(stringFromAny(entry["exec_command"])) != key {
				continue
			}
			if strings.EqualFold(stringFromAny(entry["exec_status"]), "running") {
				idx = i
				ok = true
				break
			}
			if codingCompactRecentEnough(stringFromAny(entry["updated_at"]), createdAt, 2*time.Second) {
				idx = i
				ok = true
				break
			}
			break
		}
	}
	if !ok || idx < 0 || idx >= len(b.messages) || !strings.EqualFold(stringFromAny(b.messages[idx]["role"]), "exec") {
		idx = len(b.messages)
		entry := map[string]any{
			"id":             b.nextID("exec"),
			"role":           "exec",
			"actor":          normalizedActor,
			"content":        cmd,
			"exec_command":   cmd,
			"exec_status":    "running",
			"exec_exit_code": 0,
			"exec_output":    "",
			"created_at":     createdAt,
			"updated_at":     createdAt,
			"pending":        false,
		}
		applyCodingCompactSourceIdentity(entry, evt)
		b.messages = append(b.messages, entry)
		b.execIndexByKey[key] = idx
	}
	entry := b.messages[idx]
	if normalizedActor != "" {
		entry["actor"] = normalizedActor
	}
	entry["exec_command"] = cmd
	entry["content"] = cmd
	if strings.TrimSpace(status) != "" {
		entry["exec_status"] = status
	}
	entry["exec_exit_code"] = exitCode
	entry["exec_output"] = mergeCodingCompactOutput(stringFromAny(entry["exec_output"]), output)
	entry["updated_at"] = createdAt
	applyCodingCompactSourceIdentity(entry, evt)
	b.lastExecKey = key
	if status != "running" {
		delete(b.execIndexByKey, key)
	}
	b.lastAssistantID = ""
}

func (b *codingCompactBuilder) appendStderr(actor, text, createdAt string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	normalizedActor := strings.TrimSpace(actor)
	if strings.TrimSpace(b.lastExecKey) == "" {
		content := codingCompactRedactedText
		if strings.HasPrefix(strings.ToLower(trimmed), "run failed:") {
			content = trimmed
		}
		b.messages = append(b.messages, map[string]any{
			"id":         b.nextID("stderr"),
			"role":       "stderr",
			"actor":      normalizedActor,
			"content":    content,
			"created_at": createdAt,
			"updated_at": createdAt,
			"pending":    false,
		})
		b.lastAssistantID = ""
		return
	}
	idx, ok := b.execIndexByKey[b.lastExecKey]
	if !ok || idx < 0 || idx >= len(b.messages) {
		return
	}
	entry := b.messages[idx]
	if normalizedActor != "" {
		entry["actor"] = normalizedActor
	}
	entry["exec_output"] = mergeCodingCompactOutput(stringFromAny(entry["exec_output"]), sanitizeCodingCompactExecOutput(text))
	entry["updated_at"] = createdAt
	b.lastAssistantID = ""
}

func (b *codingCompactBuilder) Apply(evt provider.ChatEvent, createdAt time.Time) bool {
	when := createdAt.UTC().Format(time.RFC3339Nano)
	switch strings.ToLower(strings.TrimSpace(evt.Type)) {
	case "assistant_message":
		if strings.TrimSpace(evt.Text) == "" {
			return false
		}
		if codingCompactActorUsesInternalRunnerRows(evt.Actor) {
			b.upsertInternalRunnerOutput(evt, evt.Text, when, false)
			return true
		}
		b.upsertAssistant(evt, evt.Text, when, false)
		return true
	case "delta":
		if strings.TrimSpace(evt.Text) == "" {
			return false
		}
		if codingCompactActorUsesInternalRunnerRows(evt.Actor) {
			b.upsertInternalRunnerOutput(evt, evt.Text, when, true)
			return true
		}
		b.upsertAssistant(evt, evt.Text, when, true)
		return true
	case "activity":
		text := strings.TrimSpace(evt.Text)
		if text == "" {
			return false
		}
		if shouldSuppressCodingCompactActivity(text) {
			return false
		}
		if recovery := parseCodingCompactRuntimeRecoveryActivity(text); recovery != nil {
			entry := map[string]any{
				"id":         b.nextID("activity"),
				"role":       "activity",
				"actor":      firstNonEmpty(stringFromAny(recovery["recovery_role"]), strings.TrimSpace(evt.Actor)),
				"content":    stringFromAny(recovery["content"]),
				"created_at": when,
				"updated_at": when,
				"pending":    false,
			}
			for _, key := range []string{"recovery_kind", "recovery_role", "recovery_attempt", "recovery_thread", "recovery_previous"} {
				if value, ok := recovery[key]; ok {
					entry[key] = value
				}
			}
			b.messages = append(b.messages, entry)
			b.lastAssistantID = ""
			return true
		}
		if parsed := parseCodingCompactSubagentActivity(text); parsed != nil {
			tool := firstNonEmpty(parsed["tool"], "subagent_activity")
			target := firstNonEmpty(parsed["target"], parsed["summary"], parsed["title"])
			key := strings.ToLower(strings.TrimSpace(strings.Join([]string{
				tool,
				target,
				normalizeCodingCompactPromptKey(parsed["summary"]),
			}, "|")))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(strings.Join([]string{tool, parsed["title"]}, "|")))
			}
			b.upsertSubagent(
				evt,
				key,
				key,
				parsed["title"],
				parsed["summary"],
				firstNonEmpty(parsed["status"], "done"),
				firstNonEmpty(parsed["phase"], tool),
				tool,
				target,
				parsed["summary"],
				"",
				"",
				"",
				"",
				"",
				nil,
				map[string]any{"text": text},
				when,
			)
			return true
		}
		if parsedMCP := parseCodingCompactMCPActivity(text); parsedMCP != nil {
			if boolFromAny(parsedMCP["generic"]) {
				return false
			}
			entry := map[string]any{
				"id":                   b.nextID("activity"),
				"role":                 "activity",
				"actor":                strings.TrimSpace(evt.Actor),
				"content":              stringFromAny(parsedMCP["text"]),
				"mcp_activity":         true,
				"mcp_activity_generic": boolFromAny(parsedMCP["generic"]),
				"created_at":           when,
				"updated_at":           when,
				"pending":              false,
			}
			if value := stringFromAny(parsedMCP["status"]); value != "" {
				entry["mcp_activity_status"] = value
			}
			b.messages = append(b.messages, entry)
			b.lastAssistantID = ""
			return true
		}
		if fileOp := parseCodingCompactFileOp(text); fileOp != "" {
			b.appendActivity(fileOp, fileOp, evt.Actor, when)
			return true
		}
		kind, command, exitCode := parseCodingCompactActivity(text)
		if command != "" {
			if !codingCompactActorCanEmitExec(evt.Actor) {
				b.appendActivity(formatCodingCompactSupervisorExecActivity(evt.Actor, command, kind, exitCode), "", evt.Actor, when)
				return true
			}
			b.upsertExec(evt, command, kind, exitCode, "", when)
			return true
		}
		lastAssistantID := b.lastAssistantID
		latestAssistantID := b.latestAssistantID
		b.appendActivity(text, "", evt.Actor, when)
		b.lastAssistantID = lastAssistantID
		b.latestAssistantID = latestAssistantID
		return true
	case "raw_event":
		payload := decodeCodingCompactRawEvent(evt.Text)
		if subagent := parseCodingCompactSubagentEvent(payload); subagent != nil {
			ids, _ := subagent["ids"].([]string)
			tool := stringFromAny(subagent["tool"])
			status := stringFromAny(subagent["status"])
			target := stringFromAny(subagent["target"])
			nickname := stringFromAny(subagent["nickname"])
			if (strings.EqualFold(tool, "wait_agent") || strings.EqualFold(tool, "wait")) && !strings.EqualFold(status, "running") && strings.TrimSpace(nickname) == "" {
				if knownName := b.knownSubagentDisplayName(target, ids); strings.TrimSpace(knownName) != "" {
					subagent["nickname"] = knownName
					subagent["title"] = "Finished waiting for " + knownName
				}
			}
			b.upsertSubagent(
				evt,
				stringFromAny(subagent["key"]),
				stringFromAny(subagent["lifecycle_key"]),
				stringFromAny(subagent["title"]),
				stringFromAny(subagent["summary"]),
				stringFromAny(subagent["status"]),
				stringFromAny(subagent["phase"]),
				stringFromAny(subagent["tool"]),
				stringFromAny(subagent["target"]),
				stringFromAny(subagent["prompt"]),
				stringFromAny(subagent["nickname"]),
				stringFromAny(subagent["name"]),
				stringFromAny(subagent["role"]),
				stringFromAny(subagent["model"]),
				stringFromAny(subagent["reasoning"]),
				ids,
				subagent["raw"],
				when,
			)
			return true
		}
		if mcp := parseCodingCompactMCPEvent(payload); mcp != nil {
			entry := map[string]any{
				"id":                   b.nextID("activity"),
				"role":                 "activity",
				"actor":                strings.TrimSpace(evt.Actor),
				"content":              stringFromAny(mcp["text"]),
				"mcp_activity":         true,
				"mcp_activity_generic": false,
				"created_at":           when,
				"updated_at":           when,
				"pending":              false,
			}
			for _, key := range []string{"status", "server", "tool", "target"} {
				if value, ok := mcp[key]; ok {
					entry["mcp_activity_"+key] = value
				}
			}
			applyCodingCompactSourceIdentity(entry, evt)
			b.messages = append(b.messages, entry)
			b.lastAssistantID = ""
			return true
		}
		if fileOp := parseCodingCompactFileOpEvent(payload); fileOp != "" {
			b.appendActivity(fileOp, fileOp, evt.Actor, when)
			return true
		}
		command, status, output, exitCode := parseCodingCompactExecEvent(payload)
		if command == "" {
			return false
		}
		if !codingCompactActorCanEmitExec(evt.Actor) {
			b.appendActivity(formatCodingCompactSupervisorExecActivity(evt.Actor, command, status, exitCode), "", evt.Actor, when)
			return true
		}
		b.upsertExec(evt, command, status, exitCode, output, when)
		return true
	case "stderr":
		text := strings.TrimSpace(evt.Text)
		if text == "" {
			return false
		}
		b.appendStderr(evt.Actor, text, when)
		return true
	}
	return false
}

func (b *codingCompactBuilder) Snapshot() []map[string]any {
	out := make([]map[string]any, 0, len(b.messages))
	for _, item := range b.messages {
		clone := make(map[string]any, len(item))
		for k, v := range item {
			clone[k] = v
		}
		out = append(out, clone)
	}
	return out
}

func (b *codingCompactBuilder) AppendUser(content, createdAt string) {
	text := strings.TrimSpace(content)
	if text == "" {
		return
	}
	b.messages = append(b.messages, map[string]any{
		"id":         b.nextID("user"),
		"role":       "user",
		"content":    text,
		"created_at": createdAt,
		"updated_at": createdAt,
		"pending":    false,
	})
	b.lastAssistantID = ""
	b.latestAssistantID = ""
}

func (b *codingCompactBuilder) SetLastAssistantUsage(inputTokens, cachedInputTokens, outputTokens int) {
	if strings.TrimSpace(b.latestAssistantID) == "" {
		return
	}
	for i := len(b.messages) - 1; i >= 0; i-- {
		entry := b.messages[i]
		if !strings.EqualFold(stringFromAny(entry["role"]), "assistant") || stringFromAny(entry["id"]) != b.latestAssistantID {
			continue
		}
		entry["input_tokens"] = inputTokens
		entry["cached_input_tokens"] = cachedInputTokens
		entry["output_tokens"] = outputTokens
		return
	}
}

func (b *codingCompactBuilder) AppendStoredUser(id, content, createdAt string) {
	text := strings.TrimSpace(content)
	if text == "" {
		return
	}
	entryID := strings.TrimSpace(id)
	if entryID == "" {
		entryID = b.nextID("user")
	}
	b.messages = append(b.messages, map[string]any{
		"id":         entryID,
		"role":       "user",
		"content":    text,
		"created_at": createdAt,
		"updated_at": createdAt,
		"pending":    false,
	})
	b.lastAssistantID = ""
	b.latestAssistantID = ""
}

func (b *codingCompactBuilder) AppendStoredAssistant(id, actor, content, createdAt string, inputTokens, outputTokens int) {
	text := strings.TrimSpace(content)
	if text == "" {
		return
	}
	if codingCompactActorUsesInternalRunnerRows(actor) {
		b.upsertInternalRunnerOutput(provider.ChatEvent{Actor: actor}, text, createdAt, false)
		return
	}
	entryID := strings.TrimSpace(id)
	if entryID == "" {
		entryID = b.nextID("assistant")
	}
	entry := map[string]any{
		"id":         entryID,
		"role":       "assistant",
		"actor":      strings.TrimSpace(actor),
		"content":    text,
		"created_at": createdAt,
		"updated_at": createdAt,
		"pending":    false,
	}
	if inputTokens > 0 {
		entry["input_tokens"] = inputTokens
	}
	if outputTokens > 0 {
		entry["output_tokens"] = outputTokens
	}
	b.messages = append(b.messages, entry)
	b.lastAssistantID = entryID
	b.latestAssistantID = entryID
}

func (b *codingCompactBuilder) SeedFromRawMessages(history []store.CodingMessage) {
	b.messages = make([]map[string]any, 0, len(history))
	b.lastAssistantID = ""
	b.latestAssistantID = ""
	b.lastExecKey = ""
	b.execIndexByKey = make(map[string]int)
	b.subagentIndexByKey = make(map[string]int)
	for _, msg := range history {
		createdAt := msg.CreatedAt.UTC().Format(time.RFC3339Nano)
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "user":
			b.AppendStoredUser(msg.ID, msg.Content, createdAt)
		case "assistant":
			b.AppendStoredAssistant(msg.ID, msg.Actor, msg.Content, createdAt, msg.InputTokens, msg.OutputTokens)
		case "activity":
			_ = b.Apply(provider.ChatEvent{Type: "activity", Text: msg.Content, Actor: msg.Actor}, msg.CreatedAt)
		case "event":
			_ = b.Apply(provider.ChatEvent{Type: "raw_event", Text: msg.Content, Actor: msg.Actor}, msg.CreatedAt)
		case "stderr":
			_ = b.Apply(provider.ChatEvent{Type: "stderr", Text: msg.Content, Actor: msg.Actor}, msg.CreatedAt)
		}
	}
	b.sequence = len(b.messages)
}

func sanitizeCodingCompactExecOutput(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return codingCompactRedactedText
}
