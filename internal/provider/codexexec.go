package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type ChatEvent struct {
	Type string
	Text string
}

type ChatResult struct {
	Text         string
	Messages     []string
	ThreadID     string
	InputTokens  int
	OutputTokens int
	ToolCalls    []ToolCall
}

type CodexExec struct {
	Binary string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ExecOptions struct {
	CodexHome       string
	WorkDir         string
	Model           string
	ReasoningEffort string
	Prompt          string
	ResumeID        string
	Persist         bool
	SandboxMode     string
	CommandMode     string
	OnProcessStart  func(pid int, forceKill func() error)
}

func NewCodexExec(binary string) *CodexExec {
	if strings.TrimSpace(binary) == "" {
		binary = "codex"
	}
	return &CodexExec{Binary: binary}
}

func (c *CodexExec) Chat(ctx context.Context, codexHome string, model string, prompt string) (ChatResult, error) {
	return c.ChatWithOptions(ctx, ExecOptions{
		CodexHome:   codexHome,
		WorkDir:     defaultExecWorkDir(codexHome),
		Model:       model,
		Prompt:      prompt,
		Persist:     false,
		SandboxMode: "write",
		CommandMode: "chat",
	})
}

func (c *CodexExec) ChatWithOptions(ctx context.Context, opts ExecOptions) (ChatResult, error) {
	clean := resolveCleanExecMode()
	codexHome := strings.TrimSpace(opts.CodexHome)
	workDir := strings.TrimSpace(opts.WorkDir)

	cmd := exec.CommandContext(ctx, c.Binary, c.buildExecArgs(opts, clean)...)
	if dir := firstNonEmpty(workDir, codexHome); dir != "" {
		cmd.Dir = dir
	}
	if prompt := strings.TrimSpace(opts.Prompt); prompt != "" {
		cmd.Stdin = strings.NewReader(opts.Prompt)
	}
	cmd.Env = c.buildExecEnv(cmd.Environ(), codexHome, clean)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ChatResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ChatResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return ChatResult{}, err
	}
	if opts.OnProcessStart != nil && cmd.Process != nil {
		opts.OnProcessStart(cmd.Process.Pid, func() error {
			return cmd.Process.Kill()
		})
	}
	stderrBuf, stderrDone := drainPipe(stderr)

	var out ChatResult
	var lastExecErr string
	assistantMessages := make([]string, 0, 4)
	toolCalls := make([]ToolCall, 0, 4)
	seenToolCalls := map[string]struct{}{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var evt map[string]any
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if msg := codexEventErrorMessage(evt); msg != "" {
			lastExecErr = msg
		}
		if t, _ := evt["type"].(string); strings.TrimSpace(t) == "thread.started" {
			if tid, _ := evt["thread_id"].(string); strings.TrimSpace(tid) != "" {
				out.ThreadID = strings.TrimSpace(tid)
			}
		}
		t, _ := evt["type"].(string)
		if t == "item.completed" {
			item, _ := evt["item"].(map[string]any)
			if itemType, _ := item["type"].(string); itemType == "agent_message" {
				if text, _ := item["text"].(string); text != "" {
					assistantMessages = append(assistantMessages, text)
				}
			}
		}
		appendUniqueToolCalls(&toolCalls, seenToolCalls, codexEventToolCalls(evt))
		if t == "turn.completed" {
			usage, _ := evt["usage"].(map[string]any)
			out.InputTokens = int(number(usage["input_tokens"]))
			out.OutputTokens = int(number(usage["output_tokens"]))
		}
	}
	if err := sc.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		<-stderrDone
		return ChatResult{}, err
	}
	if err := cmd.Wait(); err != nil {
		<-stderrDone
		stderrBytes := stderrBuf.Bytes()
		msg := firstNonEmpty(strings.TrimSpace(lastExecErr), strings.TrimSpace(string(stderrBytes)), err.Error())
		return ChatResult{}, fmt.Errorf("codex exec failed: %s", msg)
	}
	<-stderrDone
	if len(assistantMessages) > 0 {
		out.Messages = assistantMessages
		out.Text = strings.Join(assistantMessages, "\n\n")
	}
	if len(toolCalls) > 0 {
		out.ToolCalls = toolCalls
	}
	if strings.TrimSpace(out.Text) == "" && len(out.ToolCalls) == 0 {
		return ChatResult{}, errors.New("empty response from codex")
	}
	return out, nil
}

func (c *CodexExec) StreamChat(ctx context.Context, codexHome string, model string, prompt string, onEvent func(ChatEvent) error) (ChatResult, error) {
	return c.StreamChatWithOptions(ctx, ExecOptions{
		CodexHome:   codexHome,
		WorkDir:     defaultExecWorkDir(codexHome),
		Model:       model,
		Prompt:      prompt,
		Persist:     false,
		SandboxMode: "write",
		CommandMode: "chat",
	}, onEvent)
}

func (c *CodexExec) StreamChatWithOptions(ctx context.Context, opts ExecOptions, onEvent func(ChatEvent) error) (ChatResult, error) {
	clean := resolveCleanExecMode()
	codexHome := strings.TrimSpace(opts.CodexHome)
	workDir := strings.TrimSpace(opts.WorkDir)

	cmd := exec.CommandContext(ctx, c.Binary, c.buildExecArgs(opts, clean)...)
	if dir := firstNonEmpty(workDir, codexHome); dir != "" {
		cmd.Dir = dir
	}
	if prompt := strings.TrimSpace(opts.Prompt); prompt != "" {
		cmd.Stdin = strings.NewReader(opts.Prompt)
	}
	cmd.Env = c.buildExecEnv(cmd.Environ(), codexHome, clean)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ChatResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ChatResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return ChatResult{}, err
	}
	if opts.OnProcessStart != nil && cmd.Process != nil {
		opts.OnProcessStart(cmd.Process.Pid, func() error {
			return cmd.Process.Kill()
		})
	}
	stderrBuf, stderrDone := drainPipeWithLine(stderr, func(line string) {
		if onEvent == nil {
			return
		}
		if strings.TrimSpace(line) == "" {
			return
		}
		_ = onEvent(ChatEvent{Type: "stderr", Text: sanitizeSensitiveText(line)})
	})

	var out ChatResult
	var lastExecErr string
	var emittedDelta bool
	assistantMessages := make([]string, 0, 4)
	var streamedDelta strings.Builder
	toolCalls := make([]ToolCall, 0, 4)
	seenToolCalls := map[string]struct{}{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		rawLine := strings.TrimSpace(string(line))
		if rawLine != "" {
			if err := onEvent(ChatEvent{Type: "raw_event", Text: sanitizeSensitiveText(rawLine)}); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				<-stderrDone
				return ChatResult{}, err
			}
		}
		var evt map[string]any
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if msg := codexEventErrorMessage(evt); msg != "" {
			lastExecErr = msg
		}
		if t, _ := evt["type"].(string); strings.TrimSpace(t) == "thread.started" {
			if tid, _ := evt["thread_id"].(string); strings.TrimSpace(tid) != "" {
				out.ThreadID = strings.TrimSpace(tid)
			}
		}
		if activity, ok := codexEventActivityText(evt); ok {
			if err := onEvent(ChatEvent{Type: "activity", Text: sanitizeSensitiveText(activity)}); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				<-stderrDone
				return ChatResult{}, err
			}
		}
		if delta, ok := codexEventDeltaText(evt); ok {
			streamedDelta.WriteString(delta)
			if err := onEvent(ChatEvent{Type: "delta", Text: delta}); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				<-stderrDone
				return ChatResult{}, err
			}
			emittedDelta = true
		}
		t, _ := evt["type"].(string)
		if t == "item.completed" {
			item, _ := evt["item"].(map[string]any)
			if itemType, _ := item["type"].(string); itemType == "agent_message" {
				if text, _ := item["text"].(string); text != "" {
					assistantMessages = append(assistantMessages, text)
					out.Text = text
					if err := onEvent(ChatEvent{Type: "assistant_message", Text: text}); err != nil {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
						<-stderrDone
						return ChatResult{}, err
					}
				}
			}
		}
		appendUniqueToolCalls(&toolCalls, seenToolCalls, codexEventToolCalls(evt))
		if t == "turn.completed" {
			usage, _ := evt["usage"].(map[string]any)
			out.InputTokens = int(number(usage["input_tokens"]))
			out.OutputTokens = int(number(usage["output_tokens"]))
		}
	}
	if err := sc.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		<-stderrDone
		return ChatResult{}, err
	}
	if err := cmd.Wait(); err != nil {
		<-stderrDone
		stderrBytes := stderrBuf.Bytes()
		msg := firstNonEmpty(strings.TrimSpace(lastExecErr), strings.TrimSpace(string(stderrBytes)), err.Error())
		return ChatResult{}, fmt.Errorf("codex exec failed: %s", msg)
	}
	<-stderrDone
	if len(assistantMessages) > 0 {
		out.Messages = assistantMessages
		out.Text = strings.Join(assistantMessages, "\n\n")
	}
	if strings.TrimSpace(out.Text) == "" {
		if merged := strings.TrimSpace(streamedDelta.String()); merged != "" {
			out.Text = merged
			if len(out.Messages) == 0 {
				out.Messages = []string{merged}
			}
		}
	}
	if len(toolCalls) > 0 {
		out.ToolCalls = toolCalls
	}
	if strings.TrimSpace(out.Text) == "" && len(out.ToolCalls) == 0 {
		return ChatResult{}, errors.New("empty response from codex")
	}
	if !emittedDelta && strings.TrimSpace(out.Text) != "" {
		if err := onEvent(ChatEvent{Type: "delta", Text: out.Text}); err != nil {
			return ChatResult{}, err
		}
	}
	return out, nil
}

var skillPathPattern = regexp.MustCompile(`/skills/([^/]+)/SKILL\.md`)

func codexEventDeltaText(evt map[string]any) (string, bool) {
	if evt == nil {
		return "", false
	}
	t, _ := evt["type"].(string)
	t = strings.TrimSpace(strings.ToLower(t))
	switch t {
	case "response.output_text.delta", "item.delta", "message.delta":
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
	t = strings.TrimSpace(strings.ToLower(t))
	item, _ := evt["item"].(map[string]any)
	itemType := ""
	if item != nil {
		itemType, _ = item["type"].(string)
		itemType = strings.TrimSpace(strings.ToLower(itemType))
		if itemType != "" &&
			itemType != "command_execution" &&
			itemType != "tool_call" &&
			itemType != "exec_command" &&
			itemType != "collab_tool_call" {
			return "", false
		}
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
		case "item.completed", "tool.completed", "tool.call.completed":
			return "Command done", true
		default:
			return "", false
		}
	}

	if m := skillPathPattern.FindStringSubmatch(cmd); len(m) == 2 {
		return "Using skill: " + strings.TrimSpace(m[1]), true
	}
	if strings.Contains(strings.ToLower(cmd), "mcp") {
		return "Running MCP call", true
	}

	switch t {
	case "item.started", "item.updated", "tool.started", "tool.call.started":
		return "Running: " + truncateActivityText(cmd), true
	case "item.completed", "tool.completed", "tool.call.completed":
		exitCode := int(number(item["exit_code"]))
		if exitCode != 0 {
			return fmt.Sprintf("Command failed (exit %d): %s", exitCode, truncateActivityText(cmd)), true
		}
		return "Command done: " + truncateActivityText(cmd), true
	default:
		return "", false
	}
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
	candidates := []string{"command", "cmd", "description", "tool", "tool_name", "name"}
	for _, key := range candidates {
		if v, _ := src[key].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
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

func drainPipe(rc io.ReadCloser) (*bytes.Buffer, <-chan struct{}) {
	return drainPipeWithLine(rc, nil)
}

func drainPipeWithLine(rc io.ReadCloser, onLine func(string)) (*bytes.Buffer, <-chan struct{}) {
	buf := &bytes.Buffer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if rc == nil {
			return
		}
		defer rc.Close()
		reader := bufio.NewReader(io.LimitReader(rc, 512*1024))
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				_, _ = buf.WriteString(line)
				if onLine != nil {
					trimmed := strings.TrimSpace(line)
					if trimmed != "" {
						onLine(trimmed)
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				return
			}
		}
	}()
	return buf, done
}

func codexEventErrorMessage(evt map[string]any) string {
	if evt == nil {
		return ""
	}
	if t, _ := evt["type"].(string); strings.TrimSpace(t) == "error" {
		if msg, _ := evt["message"].(string); strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	if t, _ := evt["type"].(string); strings.TrimSpace(t) == "turn.failed" {
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

func (c *CodexExec) buildExecArgs(opts ExecOptions, clean bool) []string {
	model := strings.TrimSpace(opts.Model)
	reasoningEffort := normalizeReasoningEffort(opts.ReasoningEffort)
	prompt := strings.TrimSpace(opts.Prompt)
	resumeID := strings.TrimSpace(opts.ResumeID)
	sandboxMode := normalizeSandboxMode(opts.SandboxMode)
	commandMode := normalizeCommandMode(opts.CommandMode)
	args := []string{"exec"}
	if commandMode == "review" {
		args = append(args, "review", "--json", "--skip-git-repo-check")
		if prompt == "" {
			args = append(args, "--uncommitted")
		}
		if sandboxMode == "full-access" {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			args = append(args, "--full-auto")
		}
		if model != "" {
			args = append(args, "-m", model)
		}
	} else if resumeID != "" {
		args = append(args, "resume", resumeID)
		args = append(args, "--json", "--skip-git-repo-check")
		if sandboxMode == "full-access" {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			args = append(args, "--full-auto")
		}
		if model != "" {
			args = append(args, "-m", model)
		}
	} else {
		args = append(args,
			"--json",
			"--skip-git-repo-check",
		)
		if sandboxMode == "full-access" {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			args = append(args, "--sandbox", "workspace-write")
		}
		if model != "" {
			args = append(args, "-m", model)
		}
	}
	args = append(args, "-c", fmt.Sprintf(`model_reasoning_effort="%s"`, reasoningEffort))
	if prompt != "" {
		// Use stdin prompt mode to avoid "argument list too long" when prompt/context is large.
		args = append(args, "-")
	}
	if clean && !opts.Persist {
		args = append(args, "--ephemeral")
	}
	return args
}

func normalizeReasoningEffort(v string) string {
	effort := strings.TrimSpace(strings.ToLower(v))
	switch effort {
	case "low", "high":
		return effort
	default:
		return "medium"
	}
}

func normalizeCommandMode(v string) string {
	mode := strings.TrimSpace(strings.ToLower(v))
	switch mode {
	case "review":
		return "review"
	default:
		return "chat"
	}
}

func normalizeSandboxMode(v string) string {
	mode := strings.TrimSpace(strings.ToLower(v))
	switch mode {
	case "full", "full-access", "danger-full-access":
		return "full-access"
	case "write", "workspace-write":
		return "workspace-write"
	default:
		return "workspace-write"
	}
}

func resolveSandboxMode() string {
	if v := strings.TrimSpace(os.Getenv("CODEXSESS_CODEX_SANDBOX")); v != "" {
		return v
	}
	return "workspace-write"
}

func resolveCleanExecMode() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_CLEAN_EXEC")))
	if v == "" {
		return false
	}
	return v != "0" && v != "false" && v != "no"
}

func defaultExecWorkDir(codexHome string) string {
	if v := strings.TrimSpace(os.Getenv("CODEXSESS_CODEX_WORKDIR")); v != "" {
		return v
	}
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		return strings.TrimSpace(wd)
	}
	return strings.TrimSpace(codexHome)
}

func (c *CodexExec) buildExecEnv(base []string, codexHome string, clean bool) []string {
	env := append([]string{}, base...)
	env = append(env, "CODEX_HOME="+codexHome)
	if !clean {
		return env
	}
	homeRoot := filepath.Join(strings.TrimSpace(codexHome), ".codexsess-clean-home")
	if strings.TrimSpace(codexHome) == "" {
		homeRoot = filepath.Join(os.TempDir(), "codexsess-clean-home")
	}
	_ = os.MkdirAll(filepath.Join(homeRoot, ".config"), 0o700)
	_ = os.MkdirAll(filepath.Join(homeRoot, ".local", "share"), 0o700)
	env = append(env,
		"HOME="+homeRoot,
		"XDG_CONFIG_HOME="+filepath.Join(homeRoot, ".config"),
		"XDG_DATA_HOME="+filepath.Join(homeRoot, ".local", "share"),
	)
	return env
}
