package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
			"name":      "read_file",
			"arguments": map[string]any{"path": "README.md"},
		},
	}

	calls := codexEventToolCalls(evt)
	if len(calls) != 0 {
		t.Fatalf("expected no tool calls when item.type is missing, got %d", len(calls))
	}
}

func TestCodexEventDeltaText_SupportsAgentMessageVariants(t *testing.T) {
	for _, tc := range []map[string]any{
		{
			"type":  "item/agentMessage/delta",
			"delta": "hello from agent",
		},
		{
			"type": "item/message/delta",
			"item": map[string]any{
				"delta": "hello from message",
			},
		},
	} {
		got, ok := codexEventDeltaText(tc)
		if !ok {
			t.Fatalf("expected delta text for %#v", tc)
		}
		if got == "" {
			t.Fatalf("expected non-empty delta text for %#v", tc)
		}
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

func TestSummarizeAppServerEvent_SuppressesReasoningAndPlanTokenDeltas(t *testing.T) {
	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{
			method: "item/reasoning/textDelta",
			params: map[string]any{"delta": "hello"},
		},
		{
			method: "item/reasoning/summaryTextDelta",
			params: map[string]any{"delta": "summary"},
		},
		{
			method: "item/plan/delta",
			params: map[string]any{"delta": "step"},
		},
	} {
		if got := summarizeAppServerEvent(tc.method, tc.params); strings.TrimSpace(got) != "" {
			t.Fatalf("expected %s to be suppressed, got %q", tc.method, got)
		}
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

func TestCodexEventActivityText_FileChangeEmitsStructuredSummary(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "fileChange",
			"changes": []map[string]any{
				{
					"path": "/home/ricki/workspaces/codexsess/docs/_bubble_fileop_test.md",
					"kind": map[string]any{"type": "delete"},
				},
			},
		},
	}

	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected fileChange activity summary to be emitted")
	}
	if !strings.HasPrefix(text, "[Deleted ") {
		t.Fatalf("expected deleted fileChange summary, got %q", text)
	}
	if !strings.Contains(text, "/home/[user]/workspaces/codexsess/docs/_bubble_fileop_test.md") {
		t.Fatalf("expected sanitized file path in activity summary, got %q", text)
	}
}

func TestCodexAppServerStreamChatWithOptions_EmitsStderrEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_stderr"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_stderr"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      printf '%s\n' 'terminal stderr line' >&2
      echo '{"id":"3","result":{"turn":{"id":"turn_stderr","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_stderr","turn":{"id":"turn_stderr"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_stderr","turnId":"turn_stderr","item":{"type":"agentMessage","id":"item_agent","text":"hello"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_stderr","turn":{"id":"turn_stderr","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	var events []ChatEvent
	reply, err := NewCodexAppServer(script).StreamChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
	}, func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithOptions: %v", err)
	}
	if got := strings.TrimSpace(reply.Text); got != "hello" {
		t.Fatalf("expected assistant text hello, got %q", got)
	}
	foundStderr := false
	for _, evt := range events {
		if strings.ToLower(strings.TrimSpace(evt.Type)) != "stderr" {
			continue
		}
		if strings.Contains(strings.ToLower(evt.Text), "terminal stderr line") {
			foundStderr = true
			break
		}
	}
	if !foundStderr {
		t.Fatalf("expected stderr event in stream, got %#v", events)
	}
}

func TestCodexAppServerStreamChatWithOptions_EmitsRawEventIdentityMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_raw"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_raw"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_raw","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_raw","turn":{"id":"turn_raw"}}}'
      echo '{"method":"item/agentMessage/delta","params":{"threadId":"thread_raw","turnId":"turn_raw","itemId":"item_raw","sequence":9,"createdAt":"2026-04-02T12:00:00Z","item":{"id":"item_raw","type":"agent_message"},"delta":"hello"}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_raw","turnId":"turn_raw","item":{"type":"agentMessage","id":"item_raw","text":"hello"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_raw","turn":{"id":"turn_raw","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	var events []ChatEvent
	_, err := NewCodexAppServer(script).StreamChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
	}, func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithOptions: %v", err)
	}
	for _, evt := range events {
		if strings.TrimSpace(evt.Type) != "raw_event" {
			continue
		}
		if !strings.Contains(evt.Text, `"method":"item/agentMessage/delta"`) {
			continue
		}
		if evt.SourceEventType != "item/agentMessage/delta" ||
			evt.SourceThreadID != "thread_raw" ||
			evt.SourceTurnID != "turn_raw" ||
			evt.SourceItemID != "item_raw" ||
			evt.SourceItemType != "agent_message" ||
			evt.EventSeq != 9 ||
			evt.CreatedAt != "2026-04-02T12:00:00Z" {
			t.Fatalf("expected raw_event identity metadata, got %#v", evt)
		}
		return
	}
	t.Fatalf("expected raw_event delta in %#v", events)
}

func TestCodexAppServerStreamChatWithOptions_EmitsStderrIdentityMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_stderr_meta"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_stderr_meta"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      printf '%s\n' 'stderr metadata line' >&2
      echo '{"id":"3","result":{"turn":{"id":"turn_stderr_meta","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_stderr_meta","turn":{"id":"turn_stderr_meta"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_stderr_meta","turnId":"turn_stderr_meta","item":{"type":"agentMessage","id":"item_agent","text":"hello"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_stderr_meta","turn":{"id":"turn_stderr_meta","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	var events []ChatEvent
	_, err := NewCodexAppServer(script).StreamChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
	}, func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithOptions: %v", err)
	}
	for _, evt := range events {
		if strings.TrimSpace(evt.Type) != "stderr" {
			continue
		}
		if !strings.Contains(evt.Text, "stderr metadata line") {
			continue
		}
		if evt.SourceEventType != "stderr" ||
			evt.SourceThreadID != "" ||
			evt.SourceTurnID != "" ||
			evt.SourceItemID != "" ||
			evt.SourceItemType != "" ||
			evt.EventSeq != 0 ||
			evt.CreatedAt != "" {
			t.Fatalf("expected stderr metadata defaults, got %#v", evt)
		}
		return
	}
	t.Fatalf("expected stderr event in %#v", events)
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

func TestCodexEventActivityText_CommandExecutionCamelCase(t *testing.T) {
	evt := map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":    "commandExecution",
			"command": "pwd",
		},
	}
	text, ok := codexEventActivityText(evt)
	if ok || strings.TrimSpace(text) != "" {
		t.Fatalf("expected commandExecution activity to be suppressed in favor of structured raw_event exec rows, got ok=%v text=%q", ok, text)
	}
}

func TestCodexEventActivityText_FunctionCallExecCommandUsesArguments(t *testing.T) {
	evt := map[string]any{
		"type": "rawResponseItem/completed",
		"item": map[string]any{
			"type":      "function_call",
			"name":      "exec_command",
			"arguments": `{"command":"ls -la"}`,
		},
	}
	text, ok := codexEventActivityText(evt)
	if ok || strings.TrimSpace(text) != "" {
		t.Fatalf("expected function_call exec_command activity to be suppressed in favor of structured raw_event exec rows, got ok=%v text=%q", ok, text)
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

func TestCodexEventActivityText_CollabWaitFromFunctionName(t *testing.T) {
	waitEvt := map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":                "collab_tool_call",
			"receiver_thread_ids": []any{"agent-thread-1", "agent-thread-2"},
			"function": map[string]any{
				"name": "wait",
			},
		},
	}
	waitText, waitOK := codexEventActivityText(waitEvt)
	if !waitOK {
		t.Fatalf("expected collab wait activity from function.name")
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

func TestCodexEventActivityText_FileChangeIncludesStats(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "file_change",
			"changes": []any{
				map[string]any{
					"kind":          "edited",
					"path":          "CHANGELOG.md",
					"added_lines":   28,
					"deleted_lines": 12,
				},
			},
		},
	}

	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected file change activity to be emitted")
	}
	if !strings.HasPrefix(text, "[Edited ") {
		t.Fatalf("expected bracketed edited activity, got %q", text)
	}
	if !strings.Contains(text, "CHANGELOG.md") {
		t.Fatalf("expected file path in activity text, got %q", text)
	}
	if !strings.Contains(text, "(+28 -12)") {
		t.Fatalf("expected line stats in activity text, got %q", text)
	}
}

func TestCodexEventActivityText_MCPStarted(t *testing.T) {
	evt := map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type": "tool_call",
			"name": "mcp__playwright__browser_click",
		},
	}

	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected MCP activity to be emitted")
	}
	if got, want := text, "Running MCP: playwright.browser_click"; got != want {
		t.Fatalf("unexpected MCP started text: got %q want %q", got, want)
	}
}

func TestCodexEventActivityText_MCPStartedFromFunctionArguments(t *testing.T) {
	evt := map[string]any{
		"type": "item.updated",
		"item": map[string]any{
			"type": "tool_call",
			"function": map[string]any{
				"name":      "mcp__filesystem__read_file",
				"arguments": `{"path":"README.md"}`,
			},
		},
	}
	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected MCP function activity to be emitted")
	}
	if got, want := text, "Running MCP: filesystem.read_file"; got != want {
		t.Fatalf("unexpected MCP started text: got %q want %q", got, want)
	}
}

func TestCodexEventActivityText_MCPCompletedWithSummary(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "tool_call",
			"name": "mcp__github__search_code",
			"output": map[string]any{
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Found 12 matches in 3 files",
					},
				},
			},
		},
	}

	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected MCP completed activity to be emitted")
	}
	if !strings.Contains(text, "MCP done: github.search_code") {
		t.Fatalf("unexpected MCP completed header: %q", text)
	}
	if !strings.Contains(text, "Found 12 matches in 3 files") {
		t.Fatalf("expected MCP completed summary in activity text: %q", text)
	}
}

func TestCodexEventActivityText_MCPFailed(t *testing.T) {
	evt := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":  "tool_call",
			"name":  "mcp__playwright__browser_click",
			"error": map[string]any{"message": "Element not found"},
		},
	}

	text, ok := codexEventActivityText(evt)
	if !ok {
		t.Fatalf("expected MCP failed activity to be emitted")
	}
	if !strings.Contains(text, "MCP failed: playwright.browser_click") {
		t.Fatalf("unexpected MCP failed header: %q", text)
	}
}

func TestMapAppServerEvent_UnknownMethodFallsBackToActivity(t *testing.T) {
	var events []ChatEvent
	out := ChatResult{}
	err := mapAppServerEvent(rpcEnvelope{
		Method: "item/unknown",
		Params: mustJSON(map[string]any{"foo": "bar"}),
	}, &out, "executor", func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("mapAppServerEvent error: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected fallback activity event")
	}
	found := false
	for _, evt := range events {
		if strings.TrimSpace(strings.ToLower(evt.Type)) != "activity" {
			continue
		}
		if strings.Contains(strings.ToLower(evt.Text), "item/unknown") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected activity text to mention method, got %#v", events)
	}
}

func TestMapAppServerEvent_PreservesAssistantItemIdentity(t *testing.T) {
	out := ChatResult{}
	var captured []ChatEvent

	evt := rpcEnvelope{
		Method: "item/agentMessage/delta",
		Params: mustJSON(map[string]any{
			"threadId":  "thread-chat-1",
			"turnId":    "turn-chat-1",
			"itemId":    "item-assistant-1",
			"sequence":  17,
			"createdAt": "2026-04-02T10:11:12Z",
			"item": map[string]any{
				"type": "agent_message",
			},
			"delta": "hello",
		}),
	}

	err := mapAppServerEvent(evt, &out, "chat", func(event ChatEvent) error {
		captured = append(captured, event)
		return nil
	})
	if err != nil {
		t.Fatalf("mapAppServerEvent: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected one event, got %d", len(captured))
	}
	if got := captured[0]; got.SourceEventType != "item/agentMessage/delta" ||
		got.SourceThreadID != "thread-chat-1" ||
		got.SourceTurnID != "turn-chat-1" ||
		got.SourceItemID != "item-assistant-1" ||
		got.SourceItemType != "agent_message" ||
		got.EventSeq != 17 ||
		got.CreatedAt != "2026-04-02T10:11:12Z" {
		t.Fatalf("expected source identity metadata to be preserved, got %#v", got)
	}
}

func TestMapAppServerEvent_PreservesCompletedToolIdentity(t *testing.T) {
	out := ChatResult{}
	var captured []ChatEvent

	evt := rpcEnvelope{
		Method: "item/completed",
		Params: mustJSON(map[string]any{
			"eventSeq": 23,
			"thread": map[string]any{
				"id": "thread-chat-2",
			},
			"turn": map[string]any{
				"id":        "turn-chat-2",
				"createdAt": "2026-04-02T11:12:13Z",
			},
			"item": map[string]any{
				"id":     "item-tool-2",
				"type":   "tool_call",
				"name":   "mcp__github__search_code",
				"output": map[string]any{"summary": "Found 2 matches"},
			},
		}),
	}

	err := mapAppServerEvent(evt, &out, "chat", func(event ChatEvent) error {
		captured = append(captured, event)
		return nil
	})
	if err != nil {
		t.Fatalf("mapAppServerEvent: %v", err)
	}
	if len(captured) == 0 {
		t.Fatalf("expected captured events")
	}
	found := false
	for _, event := range captured {
		if event.SourceEventType == "item/completed" &&
			event.SourceThreadID == "thread-chat-2" &&
			event.SourceTurnID == "turn-chat-2" &&
			event.SourceItemID == "item-tool-2" &&
			event.SourceItemType == "tool_call" &&
			event.EventSeq == 23 &&
			event.CreatedAt == "2026-04-02T11:12:13Z" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected source tool identity in %#v", captured)
	}
}

func TestMapAppServerEvent_ThreadStartedSummaryIsSuppressed(t *testing.T) {
	var events []ChatEvent
	out := ChatResult{}
	err := mapAppServerEvent(rpcEnvelope{
		Method: "thread/started",
		Params: mustJSON(map[string]any{
			"thread": map[string]any{
				"id": "thread-subagent",
			},
		}),
	}, &out, "legacy_owner", func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("mapAppServerEvent error: %v", err)
	}
	if got := strings.TrimSpace(out.ThreadID); got != "thread-subagent" {
		t.Fatalf("expected thread id to be captured, got %q", got)
	}
	for _, evt := range events {
		if strings.TrimSpace(strings.ToLower(evt.Type)) == "activity" && strings.Contains(strings.ToLower(evt.Text), "thread/started") {
			t.Fatalf("expected thread/started summary to be suppressed, got %#v", events)
		}
	}
}

func TestMapAppServerEvent_ReconnectingIsTransientActivity(t *testing.T) {
	var events []ChatEvent
	out := ChatResult{}
	err := mapAppServerEvent(rpcEnvelope{
		Method: "error",
		Params: mustJSON(map[string]any{
			"error": map[string]any{
				"message": "Reconnecting... 2/5",
			},
		}),
	}, &out, "executor", func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("mapAppServerEvent reconnect error should be transient, got %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one activity event, got %#v", events)
	}
	if got := strings.TrimSpace(strings.ToLower(events[0].Type)); got != "activity" {
		t.Fatalf("expected activity event type, got %q", events[0].Type)
	}
	if got := strings.TrimSpace(events[0].Text); got != "Reconnecting... 2/5" {
		t.Fatalf("unexpected reconnect activity text: %q", got)
	}
}

func TestPersistentAppServerRuntimeCache_ShouldResetOnReconnectError(t *testing.T) {
	cache := &appServerRuntimeCache{}
	if !cache.shouldResetOnError(errors.New("codex app-server error: Reconnecting... 2/5")) {
		t.Fatalf("expected reconnect errors to trigger runtime cache reset")
	}
	if cache.shouldResetOnError(errors.New("validation failed")) {
		t.Fatalf("expected non-transport errors to avoid runtime cache reset")
	}
}

func TestSummarizeAppServerEvent_MCPReadyIsSuppressed(t *testing.T) {
	if got := summarizeAppServerEvent("mcpServer/startupStatus/updated", map[string]any{
		"name":   "github",
		"status": "ready",
	}); got != "" {
		t.Fatalf("expected ready MCP status to be suppressed, got %q", got)
	}
	if got := summarizeAppServerEvent("mcpServer/startupStatus/updated", map[string]any{
		"name":   "github",
		"status": "starting",
	}); got != "" {
		t.Fatalf("expected starting MCP status to be suppressed, got %q", got)
	}
	if got := summarizeAppServerEvent("mcpServer/startupStatus/updated", map[string]any{
		"name":   "github",
		"status": "failed",
		"error":  "handshake failed",
	}); !strings.Contains(strings.ToLower(got), "mcp server status") {
		t.Fatalf("expected failed MCP status summary, got %q", got)
	}
}

func TestExtractAppServerAssistantText_MessageContentTextVariants(t *testing.T) {
	text := extractAppServerAssistantText(map[string]any{
		"item": map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "first chunk"},
				map[string]any{"type": "text", "text": "second chunk"},
			},
		},
	})
	if got, want := text, "first chunk\n\nsecond chunk"; got != want {
		t.Fatalf("unexpected assistant text: got %q want %q", got, want)
	}
}

func TestExtractAppServerAssistantText_IgnoresNonAssistantMessageRoles(t *testing.T) {
	for _, role := range []string{"developer", "user"} {
		text := extractAppServerAssistantText(map[string]any{
			"item": map[string]any{
				"type": "message",
				"role": role,
				"content": []any{
					map[string]any{"type": "input_text", "text": "internal content should stay hidden"},
				},
			},
		})
		if text != "" {
			t.Fatalf("expected %s message content to stay hidden, got %q", role, text)
		}
	}
}

func TestSummarizeAppServerEvent_SuppressesInternalLifecycleNoise(t *testing.T) {
	for _, tc := range []struct {
		method string
		item   map[string]any
	}{
		{
			method: "item/started",
			item:   map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "hello"}}},
		},
		{
			method: "item/completed",
			item:   map[string]any{"type": "reasoning", "summary": []any{}},
		},
		{
			method: "rawResponseItem/completed",
			item:   map[string]any{"type": "message", "role": "developer"},
		},
	} {
		if got := summarizeAppServerEvent(tc.method, map[string]any{"item": tc.item}); got != "" {
			t.Fatalf("expected %s %+v to be suppressed, got %q", tc.method, tc.item, got)
		}
	}
	for _, method := range []string{"turn/started", "turn/completed", "thread/status/changed"} {
		if got := summarizeAppServerEvent(method, map[string]any{
			"status": map[string]any{"type": "active"},
		}); got != "" {
			t.Fatalf("expected %s to be suppressed, got %q", method, got)
		}
	}
}

func TestSummarizeAppServerEvent_SuppressesFileChangeNoise(t *testing.T) {
	if got := summarizeAppServerEvent("item/fileChange/outputDelta", map[string]any{
		"delta": "Success. Updated the following files:\nA /tmp/demo.txt\n",
	}); strings.TrimSpace(got) != "" {
		t.Fatalf("expected fileChange output delta to be suppressed, got %q", got)
	}

	if got := summarizeAppServerEvent("turn/diff/updated", map[string]any{
		"diff": "diff --git a/demo.txt b/demo.txt",
	}); strings.TrimSpace(got) != "" {
		t.Fatalf("expected turn diff update to be suppressed, got %q", got)
	}
}

func TestSummarizeAppServerEvent_SuppressesEmptyCommandOutputDelta(t *testing.T) {
	for _, delta := range []any{"", " ", nil, "null", "{}", "[]"} {
		if got := summarizeAppServerEvent("item/commandExecution/outputDelta", map[string]any{"delta": delta}); got != "" {
			t.Fatalf("expected empty command output delta to be suppressed for %#v, got %q", delta, got)
		}
	}
	if got := summarizeAppServerEvent("item/commandExecution/outputDelta", map[string]any{"delta": "line 1"}); got != "Command output: line 1" {
		t.Fatalf("expected non-empty command output summary, got %q", got)
	}
}

func TestSummarizeAppServerEvent_TerminalInteractionIsHumanized(t *testing.T) {
	if got := summarizeAppServerEvent("item/commandExecution/terminalInteraction", map[string]any{
		"itemId":    "call_123",
		"processId": "6579",
		"stdin":     "",
	}); got != "Terminal interaction" {
		t.Fatalf("expected terminal interaction summary, got %q", got)
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

func TestChatWithOptions_UsesAppServerWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    rpc_id="$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"'"${rpc_id:-1}"'","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_chat_wrapper"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_chat_wrapper"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_chat_wrapper","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_chat_wrapper","turn":{"id":"turn_chat_wrapper"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_chat_wrapper","turnId":"turn_chat_wrapper","item":{"type":"agentMessage","id":"item_agent","text":"hello"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_chat_wrapper","turnId":"turn_chat_wrapper","tokenUsage":{"total":{"inputTokens":4,"outputTokens":2,"cachedInputTokens":0},"last":{"inputTokens":4,"outputTokens":2,"cachedInputTokens":0},"modelContextWindow":200000}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_chat_wrapper","turn":{"id":"turn_chat_wrapper","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	reply, err := NewCodexAppServer(script).ChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
	})
	if err != nil {
		t.Fatalf("ChatWithOptions: %v", err)
	}
	if got := strings.TrimSpace(reply.ThreadID); got != "thread_chat_wrapper" {
		t.Fatalf("expected thread_chat_wrapper, got %q", got)
	}
	if got := strings.TrimSpace(reply.Text); got != "hello" {
		t.Fatalf("expected assistant text hello, got %q", got)
	}
}

func TestStreamChatWithOptions_UsesAppServerWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_stream_wrapper"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_stream_wrapper","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_stream_wrapper","turn":{"id":"turn_stream_wrapper"}}}'
      echo '{"method":"item/agentMessage/delta","params":{"threadId":"thread_stream_wrapper","turnId":"turn_stream_wrapper","itemId":"item_agent","delta":"hi"}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_stream_wrapper","turnId":"turn_stream_wrapper","item":{"type":"agentMessage","id":"item_agent","text":"hi"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_stream_wrapper","turnId":"turn_stream_wrapper","tokenUsage":{"total":{"inputTokens":7,"outputTokens":5,"cachedInputTokens":0},"last":{"inputTokens":7,"outputTokens":5,"cachedInputTokens":0},"modelContextWindow":200000}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_stream_wrapper","turn":{"id":"turn_stream_wrapper","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	var events []ChatEvent
	reply, err := NewCodexAppServer(script).StreamChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
		ResumeID:  "thread_stream_wrapper",
	}, func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithOptions: %v", err)
	}
	if got := strings.TrimSpace(reply.ThreadID); got != "thread_stream_wrapper" {
		t.Fatalf("expected thread_stream_wrapper, got %q", got)
	}
	if got := strings.TrimSpace(reply.Text); got != "hi" {
		t.Fatalf("expected assistant text hi, got %q", got)
	}
	if len(events) == 0 {
		t.Fatalf("expected streamed events")
	}
}

func TestStreamChatWithOptions_FallsBackWhenTurnCompletedMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	prevTimeout := appServerTurnCompletionIdleTimeout
	appServerTurnCompletionIdleTimeout = 50 * time.Millisecond
	defer func() {
		appServerTurnCompletionIdleTimeout = prevTimeout
	}()

	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_stream_idle"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_stream_idle","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_stream_idle","turn":{"id":"turn_stream_idle"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_stream_idle","turnId":"turn_stream_idle","item":{"type":"agentMessage","id":"item_agent","text":"idle-complete"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_stream_idle","turnId":"turn_stream_idle","tokenUsage":{"total":{"inputTokens":7,"outputTokens":5,"cachedInputTokens":0},"last":{"inputTokens":7,"outputTokens":5,"cachedInputTokens":0},"modelContextWindow":200000}}}'
      sleep 1
      exit 0
    fi
  done
fi
exit 1
`)

	reply, err := NewCodexAppServer(script).StreamChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
		ResumeID:  "thread_stream_idle",
	}, nil)
	if err != nil {
		t.Fatalf("StreamChatWithOptions: %v", err)
	}
	if got := strings.TrimSpace(reply.ThreadID); got != "thread_stream_idle" {
		t.Fatalf("expected thread_stream_idle, got %q", got)
	}
	if got := strings.TrimSpace(reply.Text); got != "idle-complete" {
		t.Fatalf("expected assistant text idle-complete, got %q", got)
	}
}

func TestStreamChatWithOptions_FallsBackWhenAssistantDeltaIsLastSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	prevTimeout := appServerTurnCompletionIdleTimeout
	appServerTurnCompletionIdleTimeout = 50 * time.Millisecond
	defer func() {
		appServerTurnCompletionIdleTimeout = prevTimeout
	}()

	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_stream_delta_idle"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_stream_delta_idle","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_stream_delta_idle","turn":{"id":"turn_stream_delta_idle"}}}'
      echo '{"method":"item/agentMessage/delta","params":{"threadId":"thread_stream_delta_idle","turnId":"turn_stream_delta_idle","itemId":"item_agent","delta":"hello from delta-only fallback"}}'
      sleep 1
      exit 0
    fi
  done
fi
exit 1
`)

	var events []ChatEvent
	reply, err := NewCodexAppServer(script).StreamChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
		ResumeID:  "thread_stream_delta_idle",
	}, func(evt ChatEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithOptions: %v", err)
	}
	if got := strings.TrimSpace(reply.ThreadID); got != "thread_stream_delta_idle" {
		t.Fatalf("expected thread_stream_delta_idle, got %q", got)
	}
	if got := strings.TrimSpace(reply.Text); got != "hello from delta-only fallback" {
		t.Fatalf("expected delta fallback assistant text, got %q", got)
	}
	foundDelta := false
	for _, evt := range events {
		if strings.TrimSpace(strings.ToLower(evt.Type)) != "delta" {
			continue
		}
		if strings.TrimSpace(evt.Text) == "hello from delta-only fallback" {
			foundDelta = true
			break
		}
	}
	if !foundDelta {
		t.Fatalf("expected assistant delta events, got %#v", events)
	}
}

func TestStreamChatWithOptions_DoesNotIdleCompleteWhileThreadActivityContinues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	prevTimeout := appServerTurnCompletionIdleTimeout
	appServerTurnCompletionIdleTimeout = 50 * time.Millisecond
	defer func() {
		appServerTurnCompletionIdleTimeout = prevTimeout
	}()

	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_stream_busy"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_stream_busy","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_stream_busy","turn":{"id":"turn_stream_busy"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_stream_busy","turnId":"turn_stream_busy","item":{"type":"agentMessage","id":"item_agent","text":"keep waiting"}}}'
      sleep 0.04
      echo '{"method":"item/started","params":{"threadId":"thread_stream_busy","turnId":"turn_stream_busy","item":{"type":"commandExecution","id":"cmd_1"}}}'
      sleep 0.04
      echo '{"method":"turn/completed","params":{"threadId":"thread_stream_busy","turn":{"id":"turn_stream_busy","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)

	startedAt := time.Now()
	reply, err := NewCodexAppServer(script).StreamChatWithOptions(context.Background(), ExecOptions{
		CodexHome: t.TempDir(),
		WorkDir:   t.TempDir(),
		Model:     "gpt-5.2-codex",
		Prompt:    "say hello",
		ResumeID:  "thread_stream_busy",
	}, nil)
	if err != nil {
		t.Fatalf("StreamChatWithOptions: %v", err)
	}
	if got := strings.TrimSpace(reply.ThreadID); got != "thread_stream_busy" {
		t.Fatalf("expected thread_stream_busy, got %q", got)
	}
	if got := strings.TrimSpace(reply.Text); got != "keep waiting" {
		t.Fatalf("expected assistant text keep waiting, got %q", got)
	}
	if elapsed := time.Since(startedAt); elapsed < 70*time.Millisecond {
		t.Fatalf("expected turn to stay open while thread activity continues, completed too early after %s", elapsed)
	}
}

func TestAppServerClient_CallRemovesPendingOnContextCancellation(t *testing.T) {
	client := &appServerClient{
		stdin:   bufio.NewWriter(io.Discard),
		pending: map[string]chan rpcResponse{},
		subs:    map[int64]func(rpcEnvelope) error{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.call(ctx, "initialize", map[string]any{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if len(client.pending) != 0 {
		t.Fatalf("expected pending calls to be cleaned up after context cancellation, got %d", len(client.pending))
	}
}

func TestAppServerClient_ReadLoopLogsMalformedStdoutLine(t *testing.T) {
	client := &appServerClient{
		pending: map[string]chan rpcResponse{},
		subs:    map[int64]func(rpcEnvelope) error{},
	}
	var logBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prevLogWriter)

	client.readLoop(io.NopCloser(strings.NewReader("not-json\n")))

	if !strings.Contains(strings.ToLower(logBuf.String()), "malformed app-server stdout") {
		t.Fatalf("expected malformed stdout line to be logged, got %q", logBuf.String())
	}
}

func TestAppServerClient_ReadLoopPreservesStructuredErrorData(t *testing.T) {
	respCh := make(chan rpcResponse, 1)
	client := &appServerClient{
		pending: map[string]chan rpcResponse{
			"1": respCh,
		},
		subs: map[int64]func(rpcEnvelope) error{},
	}

	client.readLoop(io.NopCloser(strings.NewReader(`{"id":"1","error":{"message":"request failed","data":{"code":"model_capacity"}}}` + "\n")))

	resp := <-respCh
	if resp.Err == nil {
		t.Fatalf("expected structured rpc error")
	}
	if !strings.Contains(strings.ToLower(resp.Err.Error()), "model_capacity") {
		t.Fatalf("expected structured error data to be preserved, got %q", resp.Err.Error())
	}
}

func TestPersistentAppServerRuntimeCache_ReusesClientForSameHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	codexHome := t.TempDir()
	startCountPath := filepath.Join(t.TempDir(), "start-count")
	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  count=0
  if [ -f "`+startCountPath+`" ]; then
    count="$(cat "`+startCountPath+`")"
  fi
  count=$((count + 1))
  printf '%s' "$count" > "`+startCountPath+`"
  while IFS= read -r line; do
    rpc_id="$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"'"${rpc_id:-1}"'","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      if [ ! -f "`+startCountPath+`.thread" ]; then
        printf '%s' "thread_reuse_1" > "`+startCountPath+`.thread"
      fi
      thread_id="$(cat "`+startCountPath+`.thread")"
      echo '{"id":"'"${rpc_id:-2}"'","result":{"thread":{"id":"'"$thread_id"'"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"'"$thread_id"'"}}}'
      continue
    fi
  done
fi
exit 1
`)
	clientA, createdA, err := persistentAppServerRuntimeCache.acquire(context.Background(), script, codexHome, t.TempDir())
	if err != nil {
		t.Fatalf("acquire client A: %v", err)
	}
	if !createdA {
		t.Fatalf("expected first acquire to create client")
	}
	clientB, createdB, err := persistentAppServerRuntimeCache.acquire(context.Background(), script, codexHome, t.TempDir())
	if err != nil {
		t.Fatalf("acquire client B: %v", err)
	}
	if createdB {
		t.Fatalf("expected second acquire to reuse client")
	}
	if clientA != clientB {
		t.Fatalf("expected cached client reuse, got different pointers")
	}
	rawCount, err := os.ReadFile(startCountPath)
	if err != nil {
		t.Fatalf("read start count: %v", err)
	}
	if got := strings.TrimSpace(string(rawCount)); got != "1" {
		t.Fatalf("expected persistent app-server process to start once, got %q", got)
	}
	persistentAppServerRuntimeCache.discard(script, codexHome, clientA)
}

func TestPersistentAppServerRuntimeCache_ClientSurvivesAcquireContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	codexHome := t.TempDir()
	startCountPath := filepath.Join(t.TempDir(), "start-count")
	script := writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  count=0
  if [ -f "`+startCountPath+`" ]; then
    count="$(cat "`+startCountPath+`")"
  fi
  count=$((count + 1))
  printf '%s' "$count" > "`+startCountPath+`"
  while IFS= read -r line; do
    rpc_id="$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"'"${rpc_id:-1}"'","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"'"${rpc_id:-2}"'","result":{"thread":{"id":"thread_reuse_after_cancel"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_reuse_after_cancel"}}}'
      continue
    fi
  done
fi
exit 1
`)

	ctx, cancel := context.WithCancel(context.Background())
	clientA, createdA, err := persistentAppServerRuntimeCache.acquire(ctx, script, codexHome, t.TempDir())
	if err != nil {
		t.Fatalf("acquire client A: %v", err)
	}
	if !createdA {
		t.Fatalf("expected first acquire to create client")
	}
	cancel()
	time.Sleep(100 * time.Millisecond)

	clientB, createdB, err := persistentAppServerRuntimeCache.acquire(context.Background(), script, codexHome, t.TempDir())
	if err != nil {
		t.Fatalf("acquire client B: %v", err)
	}
	if createdB {
		t.Fatalf("expected canceled caller context not to kill cached client")
	}
	if clientA != clientB {
		t.Fatalf("expected same client after caller context cancellation")
	}
	rawCount, err := os.ReadFile(startCountPath)
	if err != nil {
		t.Fatalf("read start count: %v", err)
	}
	if got := strings.TrimSpace(string(rawCount)); got != "1" {
		t.Fatalf("expected persistent app-server process to stay alive after caller cancellation, got start count %q", got)
	}
	persistentAppServerRuntimeCache.discard(script, codexHome, clientA)
}

func writeFakeCodexAppServerScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-codex.sh")
	script := "#!/usr/bin/env bash\nset -euo pipefail\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex script: %v", err)
	}
	return path
}
