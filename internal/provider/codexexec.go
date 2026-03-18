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
}

type CodexExec struct {
	Binary string
}

type ExecOptions struct {
	CodexHome   string
	WorkDir     string
	Model       string
	Prompt      string
	ResumeID    string
	Persist     bool
	SandboxMode string
	CommandMode string
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
		WorkDir:     codexHome,
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
	stderrBuf, stderrDone := drainPipe(stderr)

	var out ChatResult
	var lastExecErr string
	assistantMessages := make([]string, 0, 4)
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
		return ChatResult{}, errors.New("empty response from codex")
	}
	return out, nil
}

func (c *CodexExec) StreamChat(ctx context.Context, codexHome string, model string, prompt string, onEvent func(ChatEvent) error) (ChatResult, error) {
	return c.StreamChatWithOptions(ctx, ExecOptions{
		CodexHome:   codexHome,
		WorkDir:     codexHome,
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
	stderrBuf, stderrDone := drainPipe(stderr)

	var out ChatResult
	var lastExecErr string
	var emittedDelta bool
	assistantMessages := make([]string, 0, 4)
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
		if activity, ok := codexEventActivityText(evt); ok {
			if err := onEvent(ChatEvent{Type: "activity", Text: activity}); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				<-stderrDone
				return ChatResult{}, err
			}
		}
		if delta, ok := codexEventDeltaText(evt); ok {
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
		return ChatResult{}, errors.New("empty response from codex")
	}
	if !emittedDelta {
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
		if itemType != "" && itemType != "command_execution" && itemType != "tool_call" && itemType != "exec_command" {
			return "", false
		}
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
	if len(v) <= 120 {
		return v
	}
	return v[:120] + "..."
}

func drainPipe(rc io.ReadCloser) (*bytes.Buffer, <-chan struct{}) {
	buf := &bytes.Buffer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if rc == nil {
			return
		}
		defer rc.Close()
		_, _ = io.Copy(buf, io.LimitReader(rc, 512*1024))
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
	prompt := strings.TrimSpace(opts.Prompt)
	resumeID := strings.TrimSpace(opts.ResumeID)
	sandboxMode := normalizeSandboxMode(opts.SandboxMode)
	commandMode := normalizeCommandMode(opts.CommandMode)
	args := []string{"exec"}
	if commandMode == "review" {
		args = append(args, "review", "--json", "--skip-git-repo-check", "--uncommitted")
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
	if prompt != "" {
		args = append(args, prompt)
	}
	if clean && !opts.Persist {
		args = append(args, "--ephemeral")
	}
	return args
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
		return "write"
	default:
		return "full-access"
	}
}

func resolveSandboxMode() string {
	if v := strings.TrimSpace(os.Getenv("CODEXSESS_CODEX_SANDBOX")); v != "" {
		return v
	}
	// default should allow BrowserOS/tooling agents to create local sockets/temp files.
	return "workspace-write"
}

func resolveCleanExecMode() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_CLEAN_EXEC")))
	if v == "" {
		return true
	}
	return v != "0" && v != "false" && v != "no"
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
