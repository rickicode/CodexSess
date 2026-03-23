package provider

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCodexEventToolCalls_ItemCompletedFunctionCall(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "function_call",
			"call_id":   "call_abc",
			"name":      "read_file",
			"arguments": map[string]any{"path": "README.md"},
		},
	}

	calls := codexEventToolCalls(evt)
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_abc" {
		t.Fatalf("unexpected call id: %q", calls[0].ID)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("unexpected tool name: %q", calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Arguments), &args); err != nil {
		t.Fatalf("arguments must be valid JSON: %v", err)
	}
	if got, _ := args["path"].(string); got != "README.md" {
		t.Fatalf("unexpected argument path: %q", got)
	}
}

func TestCodexEventToolCalls_ResponseCompletedOutput(t *testing.T) {
	evt := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"output": []any{
				map[string]any{
					"type":      "function_call",
					"id":        "fc_1",
					"call_id":   "call_xyz",
					"name":      "navigate_page",
					"arguments": `{"url":"https://example.com"}`,
				},
			},
		},
	}

	calls := codexEventToolCalls(evt)
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_xyz" {
		t.Fatalf("unexpected call id: %q", calls[0].ID)
	}
	if calls[0].Name != "navigate_page" {
		t.Fatalf("unexpected tool name: %q", calls[0].Name)
	}
	if !strings.Contains(calls[0].Arguments, "example.com") {
		t.Fatalf("unexpected arguments: %q", calls[0].Arguments)
	}
}

func TestCodexEventToolCalls_IgnoresNonToolItems(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "agent_message",
			"text": "hello",
		},
	}

	calls := codexEventToolCalls(evt)
	if len(calls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(calls))
	}
}

func TestCodexEventToolCalls_RequiresExplicitToolType(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			// missing item.type on purpose; should not be treated as tool call
			"name":      "read_file",
			"arguments": map[string]any{"path": "README.md"},
		},
	}

	calls := codexEventToolCalls(evt)
	if len(calls) != 0 {
		t.Fatalf("expected no tool calls when item.type is missing, got %d", len(calls))
	}
}

func TestCodexEventActivityText_SubagentCompletedSummary(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "tool_call",
			"name": "wait_agent",
			"output": map[string]any{
				"status":  "completed",
				"message": "Subagent finished mapping settings flow",
			},
		},
	}

	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected subagent activity to be emitted")
	}
	if !strings.Contains(strings.ToLower(text), "subagent wait completed") {
		t.Fatalf("unexpected activity prefix: %q", text)
	}
	if !strings.Contains(strings.ToLower(text), "finished mapping settings flow") {
		t.Fatalf("unexpected summary text: %q", text)
	}
}

func TestCodexEventActivityText_SubagentStarted(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "tool_call",
			"name": "spawn_agent",
			"arguments": map[string]any{
				"nickname":   "Goodall",
				"agent_type": "code-mapper",
				"message":    "Analyze backend architecture",
			},
		},
	}

	text, ok := codexEventActivityText(evt)
	if !ok || strings.TrimSpace(text) == "" {
		t.Fatalf("expected subagent spawned activity to be emitted")
	}
	if !strings.Contains(text, "Spawned Goodall [code-mapper]") {
		t.Fatalf("unexpected spawned header: %q", text)
	}
	if !strings.Contains(text, "Analyze backend architecture") {
		t.Fatalf("unexpected spawned detail: %q", text)
	}
}

func TestCodexEventActivityText_WaitAgentStarted(t *testing.T) {
	evt := map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type": "tool_call",
			"name": "wait_agent",
			"arguments": map[string]any{
				"ids": []any{"a1", "a2", "a3"},
			},
		},
	}
	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected wait_agent activity to be emitted")
	}
	if got, want := strings.Split(text, "\n")[0], "• Waiting for 3 agents"; got != want {
		t.Fatalf("unexpected wait text: got %q want %q", got, want)
	}
}

func TestCodexEventActivityText_CollabSpawnAndWait(t *testing.T) {
	spawnEvt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":                "collab_tool_call",
			"tool":                "spawn_agent",
			"prompt":              "Inspect backend architecture and summarize",
			"receiver_thread_ids": []any{"agent-thread-1"},
		},
	}
	spawnText, spawnOK := codexEventActivityText(spawnEvt)
	if !spawnOK {
		t.Fatalf("expected collab spawn activity to be emitted")
	}
	if !strings.Contains(spawnText, "Spawned subagent") {
		t.Fatalf("unexpected collab spawn text: %q", spawnText)
	}
	if !strings.Contains(spawnText, "Inspect backend architecture") {
		t.Fatalf("missing collab spawn prompt: %q", spawnText)
	}

	waitEvt := map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":                "collab_tool_call",
			"tool":                "wait",
			"receiver_thread_ids": []any{"agent-thread-1", "agent-thread-2"},
		},
	}
	waitText, waitOK := codexEventActivityText(waitEvt)
	if !waitOK {
		t.Fatalf("expected collab wait activity to be emitted")
	}
	if got, want := strings.Split(waitText, "\n")[0], "• Waiting for 2 agents"; got != want {
		t.Fatalf("unexpected wait text: got %q want %q", got, want)
	}
}

func TestCodexEventActivityText_NonCollabWaitNotForcedAsSubagent(t *testing.T) {
	evt := map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type": "tool_call",
			"name": "wait",
		},
	}
	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected generic wait command activity")
	}
	if strings.Contains(strings.ToLower(text), "waiting for agents") {
		t.Fatalf("unexpected subagent wait formatting for non-collab wait: %q", text)
	}
}

func TestTruncateActivityText_UTF8Safe(t *testing.T) {
	src := strings.Repeat("🙂", 140)
	got := truncateActivityText(src)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated text must remain valid UTF-8: %q", got)
	}
}

func TestCodexEventToolCalls_CollabToolFieldName(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "collab_tool_call",
			"tool": "spawn_agent",
			"id":   "c_1",
		},
	}
	calls := codexEventToolCalls(evt)
	if len(calls) != 1 {
		t.Fatalf("expected 1 collab tool call, got %d", len(calls))
	}
	if got, want := calls[0].Name, "spawn_agent"; got != want {
		t.Fatalf("unexpected collab tool name: got %q want %q", got, want)
	}
}

func TestBuildExecArgs_UsesStdinPromptMarker(t *testing.T) {
	c := NewCodexExec("codex")
	args := c.buildExecArgs(ExecOptions{
		Model:  "gpt-5",
		Prompt: strings.Repeat("x", 200000),
	}, false)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, " -") && (len(args) == 0 || args[len(args)-1] != "-") {
		t.Fatalf("expected stdin marker '-' in args, got: %v", args)
	}
	for _, a := range args {
		if len(a) > 4096 {
			t.Fatalf("unexpected large inline argument length=%d (prompt should not be argv)", len(a))
		}
	}
}

func TestBuildExecArgs_ReviewCommandMode(t *testing.T) {
	c := NewCodexExec("codex")
	args := c.buildExecArgs(ExecOptions{
		Model:       "gpt-5.2-codex",
		Prompt:      "Review changes in auth handler",
		CommandMode: "review",
		SandboxMode: "full-access",
	}, false)
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "exec review") {
		t.Fatalf("expected exec review args, got: %v", args)
	}
	if !strings.Contains(joined, "--json") || !strings.Contains(joined, "--skip-git-repo-check") {
		t.Fatalf("missing review flags in args: %v", args)
	}
	if strings.Contains(joined, "--uncommitted") {
		t.Fatalf("review prompt mode must not include --uncommitted, args: %v", args)
	}
	if !strings.Contains(joined, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("expected full-access review to bypass sandbox, args: %v", args)
	}
	if !strings.Contains(joined, "-m gpt-5.2-codex") {
		t.Fatalf("expected model flag in args: %v", args)
	}
	if args[len(args)-1] != "-" {
		t.Fatalf("expected stdin prompt marker '-' at end, got: %v", args)
	}
}

func TestBuildExecArgs_ReviewModeUsesReviewSubcommand(t *testing.T) {
	c := NewCodexExec("codex")
	args := c.buildExecArgs(ExecOptions{
		Model:       "gpt-5.2-codex",
		Prompt:      "focus on auth flow",
		CommandMode: "review",
		SandboxMode: "write",
	}, false)

	if len(args) < 5 {
		t.Fatalf("unexpected args length for review mode: %v", args)
	}
	if got, want := args[0], "exec"; got != want {
		t.Fatalf("unexpected first arg: got %q want %q", got, want)
	}
	if got, want := args[1], "review"; got != want {
		t.Fatalf("expected review subcommand: got %q want %q (args=%v)", got, want, args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--uncommitted") {
		t.Fatalf("prompted review should not include --uncommitted, got: %v", args)
	}
	if !strings.Contains(joined, "--full-auto") {
		t.Fatalf("expected --full-auto in review args for write sandbox, got: %v", args)
	}
	if args[len(args)-1] != "-" {
		t.Fatalf("expected stdin prompt marker '-' at end, got: %v", args)
	}
}

func TestBuildExecArgs_ReviewModeWithoutPromptUsesUncommitted(t *testing.T) {
	c := NewCodexExec("codex")
	args := c.buildExecArgs(ExecOptions{
		Model:       "gpt-5.2-codex",
		Prompt:      "",
		CommandMode: "review",
		SandboxMode: "write",
	}, false)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--uncommitted") {
		t.Fatalf("expected --uncommitted in bare review args, got: %v", args)
	}
	if len(args) > 0 && args[len(args)-1] == "-" {
		t.Fatalf("bare review must not pass stdin marker '-', got: %v", args)
	}
}
