package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

func TestCodingCompactBuilder_AssistantDeltaFinalStaysSingleRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "delta", Text: "Hel"}, now) {
		t.Fatalf("expected delta to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "delta", Text: "lo"}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected second delta to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "Hello"}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected final assistant message to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 assistant rows (delta + final), got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["role"]); got != "assistant" {
		t.Fatalf("expected first row assistant role, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["content"]); got != "Hello" {
		t.Fatalf("expected delta assistant content, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["role"]); got != "assistant" {
		t.Fatalf("expected second row assistant role, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["content"]); got != "Hello" {
		t.Fatalf("expected final assistant content, got %q", got)
	}
}

func TestCodingCompactBuilder_AssistantIdentitySeparatesItems(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 4, 2, 1, 2, 3, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{
		Type:            "delta",
		Text:            "First ",
		SourceEventType: "item/agentMessage/delta",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-1",
		SourceItemID:    "item-assistant-1",
		SourceItemType:  "agentmessage",
	}, now) {
		t.Fatalf("expected first delta")
	}
	if !b.Apply(provider.ChatEvent{
		Type:            "delta",
		Text:            "Second ",
		SourceEventType: "item/agentMessage/delta",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-1",
		SourceItemID:    "item-assistant-2",
		SourceItemType:  "agentmessage",
	}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected second delta")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 assistant rows, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["role"]); got != "assistant" {
		t.Fatalf("expected first row assistant, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["content"]); got != "First" {
		t.Fatalf("expected first assistant row content, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["source_item_id"]); got != "item-assistant-1" {
		t.Fatalf("expected first assistant source_item_id, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["role"]); got != "assistant" {
		t.Fatalf("expected second row assistant, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["content"]); got != "Second" {
		t.Fatalf("expected second assistant row content, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["source_item_id"]); got != "item-assistant-2" {
		t.Fatalf("expected second assistant source_item_id, got %q", got)
	}
}

func TestCodingCompactBuilder_SnapshotKeepsSourceIdentity(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 4, 2, 1, 2, 3, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{
		Type:            "delta",
		Text:            "Assistant bubble",
		Actor:           "chat",
		SourceEventType: "item/agentMessage/delta",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-1",
		SourceItemID:    "item-assistant-1",
		SourceItemType:  "agentmessage",
		EventSeq:        11,
	}, now) {
		t.Fatalf("expected assistant delta")
	}

	rawExec, err := json.Marshal(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":    "command_execution",
			"command": "pwd",
		},
	})
	if err != nil {
		t.Fatalf("marshal exec event: %v", err)
	}
	if !b.Apply(provider.ChatEvent{
		Type:            "raw_event",
		Text:            string(rawExec),
		Actor:           "executor",
		SourceEventType: "item/started",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-1",
		SourceItemID:    "item-exec-1",
		SourceItemType:  "command_execution",
		EventSeq:        12,
	}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected exec event")
	}

	rawSubagent, err := json.Marshal(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": `{"message":"Investigate ordering"}`,
		},
	})
	if err != nil {
		t.Fatalf("marshal subagent event: %v", err)
	}
	if !b.Apply(provider.ChatEvent{
		Type:            "raw_event",
		Text:            string(rawSubagent),
		Actor:           "executor",
		SourceEventType: "item/started",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-2",
		SourceItemID:    "item-subagent-1",
		SourceItemType:  "tool_call",
		EventSeq:        13,
	}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected subagent event")
	}

	rawMCP, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "tool_call",
			"name": "mcp__github__search_code",
			"output": map[string]any{
				"summary": "Found 2 matches",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal mcp event: %v", err)
	}
	if !b.Apply(provider.ChatEvent{
		Type:            "raw_event",
		Text:            string(rawMCP),
		Actor:           "chat",
		SourceEventType: "item/completed",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-3",
		SourceItemID:    "item-mcp-1",
		SourceItemType:  "tool_call",
		EventSeq:        14,
	}, now.Add(30*time.Millisecond)) {
		t.Fatalf("expected mcp event")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 4 {
		t.Fatalf("expected 4 compact rows, got %d", len(snapshot))
	}

	assertSourceIdentity := func(row map[string]any, role, turnID, itemID, itemType, eventType string, eventSeq int64) {
		t.Helper()
		if got := stringFromAny(row["role"]); got != role {
			t.Fatalf("expected role %q, got %q", role, got)
		}
		if got := stringFromAny(row["source_event_type"]); got != eventType {
			t.Fatalf("expected source_event_type %q, got %q", eventType, got)
		}
		if got := stringFromAny(row["source_thread_id"]); got != "thread-chat-1" {
			t.Fatalf("expected source_thread_id thread-chat-1, got %q", got)
		}
		if got := stringFromAny(row["source_turn_id"]); got != turnID {
			t.Fatalf("expected source_turn_id %q, got %q", turnID, got)
		}
		if got := stringFromAny(row["source_item_id"]); got != itemID {
			t.Fatalf("expected source_item_id %q, got %q", itemID, got)
		}
		if got := stringFromAny(row["source_item_type"]); got != itemType {
			t.Fatalf("expected source_item_type %q, got %q", itemType, got)
		}
		if got := int64(intFromAny(row["event_seq"])); got != eventSeq {
			t.Fatalf("expected event_seq %d, got %d", eventSeq, got)
		}
	}

	assertSourceIdentity(snapshot[0], "assistant", "turn-chat-1", "item-assistant-1", "agentmessage", "item/agentMessage/delta", 11)
	assertSourceIdentity(snapshot[1], "exec", "turn-chat-1", "item-exec-1", "command_execution", "item/started", 12)
	assertSourceIdentity(snapshot[2], "subagent", "turn-chat-2", "item-subagent-1", "tool_call", "item/started", 13)
	assertSourceIdentity(snapshot[3], "activity", "turn-chat-3", "item-mcp-1", "tool_call", "item/completed", 14)
}

func TestCodingCompactBuilder_InternalRunnerIdentitySeparatesItems(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 4, 2, 2, 3, 4, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{
		Type:            "delta",
		Text:            "First runner delta",
		Actor:           "internal_runner",
		SourceEventType: "item/agentMessage/delta",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-1",
		SourceItemID:    "item-runner-1",
		SourceItemType:  "agentmessage",
	}, now) {
		t.Fatalf("expected first internal runner delta")
	}
	if !b.Apply(provider.ChatEvent{
		Type:            "delta",
		Text:            "Second runner delta",
		Actor:           "internal_runner",
		SourceEventType: "item/agentMessage/delta",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-1",
		SourceItemID:    "item-runner-2",
		SourceItemType:  "agentmessage",
	}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected second internal runner delta")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 internal runner rows, got %d", len(snapshot))
	}
	for idx, wantItemID := range []string{"item-runner-1", "item-runner-2"} {
		row := snapshot[idx]
		if got := stringFromAny(row["role"]); got != "activity" {
			t.Fatalf("expected activity role at index %d, got %q", idx, got)
		}
		if !boolFromAny(row["internal_runner"]) {
			t.Fatalf("expected internal_runner row at index %d", idx)
		}
		if got := stringFromAny(row["source_item_id"]); got != wantItemID {
			t.Fatalf("expected source_item_id %q at index %d, got %q", wantItemID, idx, got)
		}
	}
}

func TestCodingCompactBuilder_InternalRunnerLegacyFallbackSkipsSourceTaggedRows(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 4, 2, 2, 4, 5, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{
		Type:            "delta",
		Text:            "Tagged runner",
		Actor:           "internal_runner",
		SourceEventType: "item/agentMessage/delta",
		SourceThreadID:  "thread-chat-1",
		SourceTurnID:    "turn-chat-1",
		SourceItemID:    "item-runner-1",
		SourceItemType:  "agentmessage",
	}, now) {
		t.Fatalf("expected tagged internal runner delta")
	}
	if !b.Apply(provider.ChatEvent{
		Type:  "delta",
		Text:  "Legacy",
		Actor: "internal_runner",
	}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected first legacy delta")
	}
	if !b.Apply(provider.ChatEvent{
		Type:  "delta",
		Text:  " fallback",
		Actor: "internal_runner",
	}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected second legacy delta")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected tagged row plus legacy fallback row, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["content"]); got != "Tagged runner" {
		t.Fatalf("expected source-tagged row to stay intact, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["source_item_id"]); got != "item-runner-1" {
		t.Fatalf("expected source-tagged row to keep source_item_id, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["content"]); got != "Legacy fallback" {
		t.Fatalf("expected legacy deltas to merge on their own fallback row, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["source_item_id"]); got != "" {
		t.Fatalf("expected legacy fallback row to remain without source identity, got %q", got)
	}
}

func TestCodingCompactBuilder_InternalRunnerLegacyDeltaFinalStaysSingleRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 4, 2, 2, 5, 6, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "delta", Text: "Legacy runner partial", Actor: "internal_runner"}, now) {
		t.Fatalf("expected legacy internal runner delta")
	}
	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "Legacy runner final", Actor: "internal_runner"}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected legacy internal runner final message")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 legacy internal runner row, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["content"]); got != "Legacy runner final" {
		t.Fatalf("expected legacy internal runner row to finalize in place, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["source_item_id"]); got != "" {
		t.Fatalf("expected legacy final row to stay without source identity, got %q", got)
	}
}

func TestCodingCompactBuilder_AssistantIdentitySeparatesThreads(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 4, 2, 2, 6, 7, 0, time.UTC)

	for idx, threadID := range []string{"thread-chat-1", "thread-chat-2"} {
		if !b.Apply(provider.ChatEvent{
			Type:            "delta",
			Text:            "Thread-specific",
			SourceEventType: "item/agentMessage/delta",
			SourceThreadID:  threadID,
			SourceTurnID:    "turn-chat-1",
			SourceItemID:    "item-assistant-1",
			SourceItemType:  "agentmessage",
		}, now.Add(time.Duration(idx)*10*time.Millisecond)) {
			t.Fatalf("expected assistant delta for thread %s", threadID)
		}
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 assistant rows across distinct source threads, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["source_thread_id"]); got != "thread-chat-1" {
		t.Fatalf("expected first source_thread_id thread-chat-1, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["source_thread_id"]); got != "thread-chat-2" {
		t.Fatalf("expected second source_thread_id thread-chat-2, got %q", got)
	}
}

func TestCodingCompactBuilder_ExecCompletesInPlace(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)
	command := `/bin/bash -lc "rtk git status --short"`

	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "Running: " + command, Actor: "executor"}, now) {
		t.Fatalf("expected running activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "stderr", Text: "warn: sample stderr", Actor: "executor"}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected stderr to be applied")
	}
	rawCompleted, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":              "command_execution",
			"command":           command,
			"exit_code":         0,
			"aggregated_output": "done output",
		},
	})
	if err != nil {
		t.Fatalf("marshal raw completed: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(rawCompleted), Actor: "executor"}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected completed raw_event to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "Command done: " + command, Actor: "executor"}, now.Add(30*time.Millisecond)) {
		t.Fatalf("expected activity completion to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 exec row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "exec" {
		t.Fatalf("expected exec role, got %q", got)
	}
	if got := stringFromAny(row["exec_status"]); got != "done" {
		t.Fatalf("expected exec_status done, got %q", got)
	}
	output := stringFromAny(row["exec_output"])
	if output != "[redacted]" {
		t.Fatalf("expected merged exec output redacted, got %q", output)
	}
}

func TestCodingCompactBuilder_StandaloneRunFailureStderrPersists(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "stderr", Text: "Run failed: usage limit reached", Actor: "legacy_owner"}, now) {
		t.Fatalf("expected standalone stderr to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 stderr row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "stderr" {
		t.Fatalf("expected stderr role, got %q", got)
	}
	if got := stringFromAny(row["actor"]); got != "legacy_owner" {
		t.Fatalf("expected legacy_owner actor on stderr row, got %q", got)
	}
	if got := stringFromAny(row["content"]); got != "Run failed: usage limit reached" {
		t.Fatalf("expected sanitized run failure text preserved, got %q", got)
	}
}

func TestCodingCompactBuilder_RemovedSupervisorOwnershipStaysGenericActivity(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)
	command := `/bin/bash -lc "rtk git status --short"`

	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "Running: " + command, Actor: "legacy_owner"}, now) {
		t.Fatalf("expected supervisor activity to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 activity row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "activity" {
		t.Fatalf("expected activity role, got %q", got)
	}
	if got := stringFromAny(row["content"]); !strings.Contains(got, command) {
		t.Fatalf("expected supervisor activity to mention %q, got %q", command, got)
	}
	if got := stringFromAny(row["actor"]); got != "legacy_owner" {
		t.Fatalf("expected original actor to stay on generic activity row, got %q", got)
	}
}

func TestCodingCompactBuilder_ExecutorExecRowsPreserveActor(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 30, 2, 3, 4, 0, time.UTC)
	command := `/bin/bash -lc "rtk timeout 120s go test ./internal/httpapi"`

	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "Running: " + command, Actor: "executor"}, now) {
		t.Fatalf("expected executor activity to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 exec row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "exec" {
		t.Fatalf("expected exec role, got %q", got)
	}
	if got := stringFromAny(row["actor"]); got != "executor" {
		t.Fatalf("expected executor actor on exec row, got %q", got)
	}
}

func TestCodingCompactBuilder_RuntimeRecoveryActivityIsNormalized(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 3, 4, 5, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  "thread.resume_started role=legacy_owner thread_id=thread_orch_123",
		Actor: "legacy_owner",
	}, now) {
		t.Fatalf("expected resume_started activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  "thread.resume_failed attempts=2 role=legacy_owner thread_id=thread_orch_123 reason=no_rollout_found",
		Actor: "legacy_owner",
	}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected resume_failed activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  "thread.rebootstrap_started role=executor previous_thread_id=thread_exec_old",
		Actor: "executor",
	}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected rebootstrap_started activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  "turn.interrupt_requested role=legacy_owner",
		Actor: "legacy_owner",
	}, now.Add(30*time.Millisecond)) {
		t.Fatalf("expected interrupt_requested activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  "turn.continue_started role=executor thread_id=thread_exec_new",
		Actor: "executor",
	}, now.Add(40*time.Millisecond)) {
		t.Fatalf("expected continue_started activity to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 5 {
		t.Fatalf("expected 5 activity rows, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["recovery_kind"]); got != "resume_started" {
		t.Fatalf("expected resume_started recovery_kind, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["content"]); !strings.Contains(got, "Resuming legacy_owner thread: thread_orch_123") {
		t.Fatalf("unexpected normalized resume_started content: %q", got)
	}
	if got := stringFromAny(snapshot[1]["recovery_kind"]); got != "resume_failed" {
		t.Fatalf("expected resume_failed recovery_kind, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["content"]); !strings.Contains(got, "Resume failed for legacy_owner thread: thread_orch_123 after 2 attempts") {
		t.Fatalf("unexpected normalized resume_failed content: %q", got)
	}
	if got := stringFromAny(snapshot[2]["recovery_kind"]); got != "rebootstrap_started" {
		t.Fatalf("expected rebootstrap_started recovery_kind, got %q", got)
	}
	if got := stringFromAny(snapshot[2]["actor"]); got != "executor" {
		t.Fatalf("expected executor actor on rebootstrap row, got %q", got)
	}
	if got := stringFromAny(snapshot[3]["recovery_kind"]); got != "interrupt_requested" {
		t.Fatalf("expected interrupt_requested recovery_kind, got %q", got)
	}
	if got := stringFromAny(snapshot[4]["recovery_kind"]); got != "continue_started" {
		t.Fatalf("expected continue_started recovery_kind, got %q", got)
	}
	if got := stringFromAny(snapshot[4]["content"]); !strings.Contains(got, "Continuing executor runtime after recovery: thread_exec_new") {
		t.Fatalf("unexpected normalized continue_started content: %q", got)
	}
}

func TestCodingCompactBuilder_SubagentTimelineActivityShowsWaitLifecycle(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 28, 2, 3, 4, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  "Timeline event: `WAITING` (awaiting analysis result from subagent `Kepler`).",
		Actor: "executor",
	}, now) {
		t.Fatalf("expected waiting activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  "The subagent finished; I am validating the cited regions directly before returning the final summary.",
		Actor: "executor",
	}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected completion activity to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 subagent lifecycle rows, got %d", len(snapshot))
	}
	waitRow := snapshot[0]
	if got := stringFromAny(waitRow["role"]); got != "subagent" {
		t.Fatalf("expected subagent role, got %q", got)
	}
	if got := stringFromAny(waitRow["subagent_tool"]); got != "wait_agent" {
		t.Fatalf("expected wait_agent tool, got %q", got)
	}
	if got := stringFromAny(waitRow["subagent_status"]); got != "running" {
		t.Fatalf("expected running wait row, got %q", got)
	}
	doneRow := snapshot[1]
	if got := stringFromAny(doneRow["subagent_tool"]); got != "wait_agent" {
		t.Fatalf("expected wait_agent tool on completion, got %q", got)
	}
	if got := stringFromAny(doneRow["subagent_status"]); got != "done" {
		t.Fatalf("expected completed wait row, got %q", got)
	}
	if got := stringFromAny(doneRow["content"]); got != "Subagent wait completed" {
		t.Fatalf("expected completed wait content, got %q", got)
	}
}

func TestCodingCompactBuilder_SubagentSpawnCompletionMergesAndWaitFinalizesRunningRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	startArgs, err := json.Marshal(map[string]any{
		"message":    "Investigate compact ordering",
		"agent_type": "golang-pro",
		"nickname":   "Planck",
	})
	if err != nil {
		t.Fatalf("marshal start args: %v", err)
	}
	startPayload, err := json.Marshal(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": string(startArgs),
		},
	})
	if err != nil {
		t.Fatalf("marshal start payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(startPayload)}, now) {
		t.Fatalf("expected spawn start to be applied")
	}

	doneArgs, err := json.Marshal(map[string]any{
		"message": "Investigate compact ordering",
		"id":      "agent-123",
	})
	if err != nil {
		t.Fatalf("marshal done args: %v", err)
	}
	donePayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": string(doneArgs),
			"output": map[string]any{
				"nickname": "Planck",
				"agent_id": "agent-123",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal done payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(donePayload)}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected spawn completion to be applied")
	}

	waitStartArgs, err := json.Marshal(map[string]any{"id": "agent-123"})
	if err != nil {
		t.Fatalf("marshal wait start args: %v", err)
	}
	waitStartPayload, err := json.Marshal(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "wait_agent",
			"arguments": string(waitStartArgs),
		},
	})
	if err != nil {
		t.Fatalf("marshal wait start payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(waitStartPayload)}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected wait start to be applied")
	}

	waitArgs, err := json.Marshal(map[string]any{"id": "agent-123"})
	if err != nil {
		t.Fatalf("marshal wait args: %v", err)
	}
	waitDonePayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "wait_agent",
			"arguments": string(waitArgs),
		},
	})
	if err != nil {
		t.Fatalf("marshal wait payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(waitDonePayload)}, now.Add(30*time.Millisecond)) {
		t.Fatalf("expected wait completion to finalize existing row")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 subagent rows, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "subagent" {
		t.Fatalf("expected subagent role, got %q", got)
	}
	if got := stringFromAny(row["subagent_status"]); got != "done" {
		t.Fatalf("expected subagent_status done, got %q", got)
	}
	if got := stringFromAny(row["subagent_target_id"]); got != "agent-123" {
		t.Fatalf("expected merged target id, got %q", got)
	}
	if got := stringFromAny(row["subagent_nickname"]); got != "Planck" {
		t.Fatalf("expected merged nickname, got %q", got)
	}
	if _, exists := row["subagent_raw"]; exists {
		t.Fatalf("expected compact row without subagent_raw field: %#v", row)
	}
	waitRow := snapshot[1]
	if got := stringFromAny(waitRow["subagent_tool"]); got != "wait_agent" {
		t.Fatalf("expected wait_agent tool, got %q", got)
	}
	if got := stringFromAny(waitRow["subagent_status"]); got != "done" {
		t.Fatalf("expected wait row done, got %q", got)
	}
	if got := stringFromAny(waitRow["content"]); !strings.Contains(got, "Planck") {
		t.Fatalf("expected wait row content to retain nickname, got %q", got)
	}
	if _, exists := waitRow["subagent_raw"]; exists {
		t.Fatalf("expected wait compact row without subagent_raw field: %#v", waitRow)
	}
}

func TestCodingCompactBuilder_SpawnPromptCarriesNicknameIntoWaitCompletion(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	startArgs, err := json.Marshal(map[string]any{
		"message": "Your nickname for this task is Planck. Read AGENTS.md only.",
	})
	if err != nil {
		t.Fatalf("marshal start args: %v", err)
	}
	startPayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": string(startArgs),
			"output": map[string]any{
				"agent_id": "agent-123",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal start payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(startPayload)}, now) {
		t.Fatalf("expected spawn completion to be applied")
	}

	waitArgs, err := json.Marshal(map[string]any{"id": "agent-123"})
	if err != nil {
		t.Fatalf("marshal wait args: %v", err)
	}
	waitDonePayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "wait_agent",
			"arguments": string(waitArgs),
		},
	})
	if err != nil {
		t.Fatalf("marshal wait payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(waitDonePayload)}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected wait completion to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(snapshot))
	}
	waitRow := snapshot[1]
	if got := stringFromAny(waitRow["content"]); !strings.Contains(got, "Planck") {
		t.Fatalf("expected wait completion to include inferred nickname, got %q", got)
	}
}

func TestCodingCompactBuilder_IndonesianSpawnPromptCarriesNicknameIntoWaitCompletion(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	startArgs, err := json.Marshal(map[string]any{
		"message": "Nickname Anda: Planck. Baca AGENTS.md saja.",
	})
	if err != nil {
		t.Fatalf("marshal start args: %v", err)
	}
	startPayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": string(startArgs),
			"output": map[string]any{
				"agent_id": "agent-123",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal start payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(startPayload)}, now) {
		t.Fatalf("expected spawn completion to be applied")
	}

	waitArgs, err := json.Marshal(map[string]any{"id": "agent-123"})
	if err != nil {
		t.Fatalf("marshal wait args: %v", err)
	}
	waitDonePayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "wait_agent",
			"arguments": string(waitArgs),
		},
	})
	if err != nil {
		t.Fatalf("marshal wait payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(waitDonePayload)}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected wait completion to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(snapshot))
	}
	waitRow := snapshot[1]
	if got := stringFromAny(waitRow["content"]); !strings.Contains(got, "Planck") {
		t.Fatalf("expected wait completion to include inferred nickname from Indonesian prompt, got %q", got)
	}
}

func TestCodingCompactBuilder_NicknamedPromptCarriesNicknameIntoWaitCompletion(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	startArgs, err := json.Marshal(map[string]any{
		"message": "You are nicknamed Planck. Read AGENTS.md only.",
	})
	if err != nil {
		t.Fatalf("marshal start args: %v", err)
	}
	startPayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": string(startArgs),
			"output": map[string]any{
				"agent_id": "agent-123",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal start payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(startPayload)}, now) {
		t.Fatalf("expected spawn completion to be applied")
	}

	waitArgs, err := json.Marshal(map[string]any{"id": "agent-123"})
	if err != nil {
		t.Fatalf("marshal wait args: %v", err)
	}
	waitDonePayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "wait_agent",
			"arguments": string(waitArgs),
		},
	})
	if err != nil {
		t.Fatalf("marshal wait payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(waitDonePayload)}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected wait completion to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(snapshot))
	}
	waitRow := snapshot[1]
	if got := stringFromAny(waitRow["content"]); !strings.Contains(got, "Planck") {
		t.Fatalf("expected wait completion to include inferred nickname from nicknamed prompt, got %q", got)
	}
}

func TestCodingCompactBuilder_YouArePromptCarriesNicknameIntoWaitCompletion(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	startArgs, err := json.Marshal(map[string]any{
		"message": "You are Planck. Read AGENTS.md only.",
	})
	if err != nil {
		t.Fatalf("marshal start args: %v", err)
	}
	startPayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": string(startArgs),
			"output": map[string]any{
				"agent_id": "agent-123",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal start payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(startPayload)}, now) {
		t.Fatalf("expected spawn completion to be applied")
	}

	waitArgs, err := json.Marshal(map[string]any{"id": "agent-123"})
	if err != nil {
		t.Fatalf("marshal wait args: %v", err)
	}
	waitDonePayload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "wait_agent",
			"arguments": string(waitArgs),
		},
	})
	if err != nil {
		t.Fatalf("marshal wait payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(waitDonePayload)}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected wait completion to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(snapshot))
	}
	waitRow := snapshot[1]
	if got := stringFromAny(waitRow["content"]); got != "Finished waiting for Planck" {
		t.Fatalf("expected wait completion to include inferred nickname from 'You are' prompt, got %q", got)
	}
}

func TestCodingCompactBuilder_SubagentSpawnCarriesRuntimeMetadata(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 28, 3, 24, 22, 0, time.UTC)

	args, err := json.Marshal(map[string]any{
		"message":          "Re-review the current /chat architecture after recent fixes in the working tree.",
		"agent_type":       "code-reviewer",
		"nickname":         "Ptolemy",
		"model":            "gpt-5.3-codex",
		"reasoning_effort": "high",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":      "tool_call",
			"tool_name": "spawn_agent",
			"arguments": string(args),
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(payload)}, now) {
		t.Fatalf("expected spawn completion payload to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected one subagent row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["subagent_model"]); got != "gpt-5.3-codex" {
		t.Fatalf("expected subagent_model gpt-5.3-codex, got %q", got)
	}
	if got := stringFromAny(row["subagent_reasoning"]); got != "high" {
		t.Fatalf("expected subagent_reasoning high, got %q", got)
	}
}

func TestCodingCompactBuilder_RawEventCommandExecutionCamelCaseBecomesExecRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC)

	payload, err := json.Marshal(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":    "commandExecution",
			"command": "pwd",
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Actor: "chat", Text: string(payload)}, now) {
		t.Fatalf("expected commandExecution payload to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "exec" {
		t.Fatalf("expected exec role, got %q", got)
	}
	if got := stringFromAny(row["exec_command"]); got != "pwd" {
		t.Fatalf("expected exec command pwd, got %q", got)
	}
	if got := stringFromAny(row["exec_status"]); got != "running" {
		t.Fatalf("expected running exec status, got %q", got)
	}
}

func TestCodingCompactBuilder_RawEventFunctionCallExecCommandBecomesExecRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 8, 0, 1, 0, time.UTC)

	payload, err := json.Marshal(map[string]any{
		"type": "rawResponseItem/completed",
		"item": map[string]any{
			"type":      "function_call",
			"name":      "exec_command",
			"arguments": `{"command":"ls -la"}`,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Actor: "chat", Text: string(payload)}, now) {
		t.Fatalf("expected function_call exec payload to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "exec" {
		t.Fatalf("expected exec role, got %q", got)
	}
	if got := stringFromAny(row["exec_status"]); got != "done" {
		t.Fatalf("expected done exec status, got %q", got)
	}
	if got := stringFromAny(row["exec_command"]); got != "ls -la" {
		t.Fatalf("expected exec command ls -la, got %q", got)
	}
}

func TestCodingCompactBuilder_RawEnvelopeCommandExecutionBecomesExecRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 8, 0, 2, 0, time.UTC)

	payload, err := json.Marshal(map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"item": map[string]any{
				"type":             "commandExecution",
				"command":          "/bin/bash -lc 'pwd && rtk git status --short'",
				"aggregatedOutput": "/home/ricki/workspaces/codexsess\n M web/src/views/CodingView.svelte",
				"exitCode":         0,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Actor: "chat", Text: string(payload)}, now) {
		t.Fatalf("expected raw envelope commandExecution payload to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "exec" {
		t.Fatalf("expected exec role, got %q", got)
	}
	if got := stringFromAny(row["exec_status"]); got != "done" {
		t.Fatalf("expected done exec status, got %q", got)
	}
	if got := stringFromAny(row["exec_command"]); got == "" {
		t.Fatalf("expected exec command to be present")
	}
	if got := stringFromAny(row["exec_output"]); got != codingCompactRedactedText {
		t.Fatalf("expected sanitized exec output, got %q", got)
	}
}

func TestCodingCompactBuilder_RawEnvelopeFileReadBecomesFileOpRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 8, 0, 3, 0, time.UTC)

	payload, err := json.Marshal(map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"item": map[string]any{
				"type": "fileRead",
				"path": "/tmp/readme.md",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Actor: "legacy_owner", Text: string(payload)}, now) {
		t.Fatalf("expected fileRead payload to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "activity" {
		t.Fatalf("expected activity role, got %q", got)
	}
	if got := stringFromAny(row["actor"]); got != "legacy_owner" {
		t.Fatalf("expected legacy_owner actor on file-op row, got %q", got)
	}
	if got := stringFromAny(row["file_op"]); got != "Read: /tmp/readme.md" {
		t.Fatalf("expected read file_op, got %q", got)
	}
}

func TestCodingCompactBuilder_RawEnvelopeFileChangesBecomeFileOpRows(t *testing.T) {
	tests := []struct {
		name     string
		kind     any
		path     string
		expected string
		added    int
		deleted  int
	}{
		{name: "created", kind: "create", path: "/tmp/new.txt", expected: "Created: /tmp/new.txt"},
		{name: "edited", kind: "edit", path: "/tmp/app.go", expected: "Edited: /tmp/app.go (+3 -1)", added: 3, deleted: 1},
		{name: "deleted", kind: "delete", path: "/tmp/old.txt", expected: "Deleted: /tmp/old.txt"},
		{name: "deleted nested kind object", kind: map[string]any{"type": "delete"}, path: "/tmp/nested-old.txt", expected: "Deleted: /tmp/nested-old.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newCodingCompactBuilder()
			payload, err := json.Marshal(map[string]any{
				"method": "item/completed",
				"params": map[string]any{
					"item": map[string]any{
						"type": "fileChange",
						"changes": []map[string]any{
							{
								"kind":          tt.kind,
								"path":          tt.path,
								"added_lines":   tt.added,
								"deleted_lines": tt.deleted,
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			if !b.Apply(provider.ChatEvent{Type: "raw_event", Actor: "chat", Text: string(payload)}, time.Date(2026, 3, 27, 8, 0, 4, 0, time.UTC)) {
				t.Fatalf("expected fileChange payload to be applied")
			}
			snapshot := b.Snapshot()
			if len(snapshot) != 1 {
				t.Fatalf("expected 1 row, got %d", len(snapshot))
			}
			if got := stringFromAny(snapshot[0]["file_op"]); got != tt.expected {
				t.Fatalf("expected file_op %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestCodingCompactBuilder_SuppressesLegacyFileChangeActivityNoise(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 28, 0, 45, 0, 0, time.UTC)

	if b.Apply(provider.ChatEvent{Type: "activity", Actor: "chat", Text: "Created: /home/ricki/workspaces/codexsess/docs/_bubble_fileop_test.md"}, now) {
		t.Fatalf("expected direct file-op activity row to be suppressed in compact rebuild")
	}
	if b.Apply(provider.ChatEvent{Type: "activity", Actor: "chat", Text: "File change: Success. Updated the following files:\nA /home/ricki/workspaces/codexsess/docs/_bubble_fileop_test.md"}, now) {
		t.Fatalf("expected file change output delta noise to be suppressed in compact rebuild")
	}
	if b.Apply(provider.ChatEvent{Type: "activity", Actor: "chat", Text: "turn/diff/updated: {\"diff\":\"diff --git a/demo.txt b/demo.txt\"}"}, now) {
		t.Fatalf("expected turn diff noise to be suppressed in compact rebuild")
	}
	if got := len(b.Snapshot()); got != 0 {
		t.Fatalf("expected no compact rows after suppressing legacy file-op noise, got %d", got)
	}
}

func TestCodingCompactBuilder_SuppressesRawReasoningActivityNoise(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 30, 2, 5, 0, 0, time.UTC)

	for _, text := range []string{
		`item/reasoning/summaryPartAdded: {"itemId":"rs_123","summaryIndex":0}`,
		`item/reasoning/textDelta: {"itemId":"rs_123","delta":"checking files"}`,
		`item/reasoning/contentPartAdded: {"itemId":"rs_123","partIndex":0}`,
	} {
		if b.Apply(provider.ChatEvent{Type: "activity", Actor: "legacy_owner", Text: text}, now) {
			t.Fatalf("expected reasoning activity %q to be suppressed", text)
		}
	}

	if got := len(b.Snapshot()); got != 0 {
		t.Fatalf("expected no compact rows after suppressing reasoning noise, got %d", got)
	}
}

func TestCodingCompactBuilder_SeedRebuildsRunningIndexes(t *testing.T) {
	b := newCodingCompactBuilder()
	command := `/bin/bash -lc "rtk rg -n compact internal/httpapi/server.go"`
	b.Seed([]map[string]any{
		{
			"id":           "exec-000001",
			"role":         "exec",
			"content":      command,
			"exec_command": command,
			"exec_status":  "running",
			"created_at":   "2026-03-24T01:02:03Z",
			"updated_at":   "2026-03-24T01:02:03Z",
		},
	})

	if !b.Apply(provider.ChatEvent{Type: "stderr", Text: "seeded stderr"}, time.Date(2026, 3, 24, 1, 2, 4, 0, time.UTC)) {
		t.Fatalf("expected stderr to update seeded exec row")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected seeded snapshot to stay single row, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["exec_output"]); got != "[redacted]" {
		t.Fatalf("expected seeded exec output redacted, got %q", got)
	}
}

func TestCodingCompactBuilder_SeedFromRawMessages_PreservesExecutorAssistantRole(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 29, 8, 30, 0, 0, time.UTC)

	history := []store.CodingMessage{
		{
			ID:        "msg-exec-assistant",
			Role:      "assistant",
			Actor:     "executor",
			Content:   "I still need your choice to proceed.",
			CreatedAt: now,
		},
		{
			ID:        "msg-orch-activity",
			Role:      "activity",
			Actor:     "legacy_owner",
			Content:   "Legacy Owner reviewing executor turn",
			CreatedAt: now.Add(10 * time.Millisecond),
		},
	}

	b.SeedFromRawMessages(history)
	snapshot := b.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 compact rows, got %d", len(snapshot))
	}
	first := snapshot[0]
	if got := stringFromAny(first["role"]); got != "assistant" {
		t.Fatalf("expected executor assistant row to stay assistant, got %q", got)
	}
	if got := stringFromAny(first["actor"]); got != "executor" {
		t.Fatalf("expected executor actor on assistant row, got %q", got)
	}
	if got := stringFromAny(first["content"]); got != "I still need your choice to proceed." {
		t.Fatalf("unexpected assistant content: %q", got)
	}
}

func TestCodingCompactBuilder_AssistantFinalAppendsAcrossGenericActivity(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "delta", Text: "Hel", Actor: "executor"}, now) {
		t.Fatalf("expected delta to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "Background sync still running"}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected generic activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "Hello", Actor: "executor"}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected final assistant message to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("expected delta + activity + final assistant rows, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["role"]); got != "assistant" {
		t.Fatalf("expected first row assistant, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["actor"]); got != "executor" {
		t.Fatalf("expected first row executor actor, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["content"]); got != "Hel" {
		t.Fatalf("expected delta assistant content Hel, got %q", got)
	}
	if got := stringFromAny(snapshot[1]["role"]); got != "activity" {
		t.Fatalf("expected middle generic activity row, got %q", got)
	}
	if got := stringFromAny(snapshot[2]["role"]); got != "assistant" {
		t.Fatalf("expected trailing assistant row, got %q", got)
	}
	if got := stringFromAny(snapshot[2]["actor"]); got != "executor" {
		t.Fatalf("expected trailing executor actor, got %q", got)
	}
	if got := stringFromAny(snapshot[2]["content"]); got != "Hello" {
		t.Fatalf("expected trailing final assistant content Hello, got %q", got)
	}
}

func TestCodingCompactBuilder_AssistantActorChangeCreatesNewRowAcrossGenericActivity(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "Executor finished batch one.", Actor: "executor"}, now) {
		t.Fatalf("expected executor assistant message to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "Legacy Owner reviewing executor turn"}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected generic activity to be applied")
	}
	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "DONE", Actor: "legacy_owner"}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected legacy_owner assistant message to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("expected executor + activity + legacy_owner rows, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["actor"]); got != "executor" {
		t.Fatalf("expected first assistant actor executor, got %q", got)
	}
	if got := stringFromAny(snapshot[0]["content"]); got != "Executor finished batch one." {
		t.Fatalf("expected executor content to remain intact, got %q", got)
	}
	if got := stringFromAny(snapshot[2]["actor"]); got != "legacy_owner" {
		t.Fatalf("expected trailing assistant actor legacy_owner, got %q", got)
	}
	if got := stringFromAny(snapshot[2]["content"]); got != "DONE" {
		t.Fatalf("expected legacy_owner content to stay separate, got %q", got)
	}
}

func TestCodingCompactBuilder_RemovedOrchestratorMessageEventIsIgnored(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	if b.Apply(provider.ChatEvent{Type: "legacy_owner.message", Text: "Legacy stopped for follow-up.", Actor: "legacy_owner"}, now) {
		t.Fatalf("expected removed legacy_owner.message event to be ignored")
	}

	if got := len(b.Snapshot()); got != 0 {
		t.Fatalf("expected no rows after removed legacy_owner.message event, got %d", got)
	}
}

func TestCodingCompactBuilder_TranscriptPreservesExecutorThenOrchestratorThenExecutor(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 7, 26, 32, 0, time.UTC)

	events := []provider.ChatEvent{
		{Type: "assistant_message", Text: "Executor finished the first implementation step.", Actor: "executor"},
		{Type: "activity", Text: "Legacy Owner reviewing executor turn"},
		{Type: "assistant_message", Text: "CONTINUE: Tighten the final validation and keep going.", Actor: "legacy_owner"},
		{Type: "activity", Text: "Legacy Owner sent continuation to executor"},
		{Type: "assistant_message", Text: "Executor finished the validation pass.", Actor: "executor"},
		{Type: "activity", Text: "Legacy Owner reviewing executor turn"},
		{Type: "assistant_message", Text: "Task is complete. No further executor action is needed.", Actor: "legacy_owner"},
	}
	for idx, evt := range events {
		if !b.Apply(evt, now.Add(time.Duration(idx)*10*time.Millisecond)) {
			t.Fatalf("expected event %d to be applied", idx)
		}
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 7 {
		t.Fatalf("expected full compact transcript with preserved ordering, got %d", len(snapshot))
	}
	wantActors := []string{"executor", "", "legacy_owner", "", "executor", "", "legacy_owner"}
	wantContent := []string{
		"Executor finished the first implementation step.",
		"Legacy Owner reviewing executor turn",
		"CONTINUE: Tighten the final validation and keep going.",
		"Legacy Owner sent continuation to executor",
		"Executor finished the validation pass.",
		"Legacy Owner reviewing executor turn",
		"Task is complete. No further executor action is needed.",
	}
	for idx := range wantContent {
		if got := stringFromAny(snapshot[idx]["content"]); got != wantContent[idx] {
			t.Fatalf("unexpected content at index %d: got %q want %q", idx, got, wantContent[idx])
		}
		if wantActors[idx] == "" {
			continue
		}
		if got := stringFromAny(snapshot[idx]["actor"]); got != wantActors[idx] {
			t.Fatalf("unexpected actor at index %d: got %q want %q", idx, got, wantActors[idx])
		}
	}
}

func TestCodingCompactBuilder_TranscriptStopsAfterOrchestratorDone(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 7, 30, 0, 0, time.UTC)

	events := []provider.ChatEvent{
		{Type: "assistant_message", Text: "Executor completed the requested changes.", Actor: "executor"},
		{Type: "activity", Text: "Legacy Owner reviewing executor turn"},
		{Type: "assistant_message", Text: "Task is complete. No further executor action is needed.", Actor: "legacy_owner"},
	}
	for idx, evt := range events {
		if !b.Apply(evt, now.Add(time.Duration(idx)*10*time.Millisecond)) {
			t.Fatalf("expected event %d to be applied", idx)
		}
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("expected executor -> activity -> legacy_owner rows, got %d", len(snapshot))
	}
	if got := stringFromAny(snapshot[0]["actor"]); got != "executor" {
		t.Fatalf("expected executor first, got %q", got)
	}
	if got := stringFromAny(snapshot[2]["actor"]); got != "legacy_owner" {
		t.Fatalf("expected legacy_owner final, got %q", got)
	}
}

func TestCodingCompactBuilder_SubagentActivityOnlyProducesCompactRow(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 1, 2, 3, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "• Waiting for subagent agent-123"}, now) {
		t.Fatalf("expected subagent activity to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 compact row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "subagent" {
		t.Fatalf("expected subagent role, got %q", got)
	}
	if got := stringFromAny(row["subagent_tool"]); got != "wait_agent" {
		t.Fatalf("expected wait_agent tool, got %q", got)
	}
	if got := stringFromAny(row["subagent_status"]); got != "running" {
		t.Fatalf("expected running status, got %q", got)
	}
	if got := stringFromAny(row["subagent_target_id"]); !strings.Contains(got, "agent-123") {
		t.Fatalf("expected target id to include agent-123, got %q", got)
	}
}

func TestCodingCompactBuilder_MCPActivityIsStructured(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 6, 49, 21, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  `MCP failed: git.status` + "\n  └ handshake failed",
		Actor: "chat",
	}, now) {
		t.Fatalf("expected MCP activity to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 MCP row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "activity" {
		t.Fatalf("expected activity role, got %q", got)
	}
	if !boolFromAny(row["mcp_activity"]) {
		t.Fatalf("expected mcp_activity=true, got %#v", row["mcp_activity"])
	}
	if got := stringFromAny(row["content"]); !strings.Contains(strings.ToLower(got), "mcp failed: git.status") {
		t.Fatalf("unexpected MCP content: %q", got)
	}
}

func TestCodingCompactBuilder_GenericMCPServerStatusActivityIsDropped(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 6, 49, 20, 0, time.UTC)

	if b.Apply(provider.ChatEvent{
		Type:  "activity",
		Text:  `MCP server status: {"name":"filesystem","status":"starting","error":null}`,
		Actor: "chat",
	}, now) {
		t.Fatalf("expected generic MCP startup activity to be suppressed")
	}

	if got := len(b.Snapshot()); got != 0 {
		t.Fatalf("expected no compact rows, got %d", got)
	}
}

func TestCodingCompactBuilder_RawEventMCPBecomesStructuredActivity(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 6, 49, 21, 0, time.UTC)
	raw, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "tool_call",
			"name": "mcp__github__search_code",
			"output": map[string]any{
				"summary": "Found 12 matches in 3 files",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal raw event: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(raw), Actor: "legacy_owner"}, now) {
		t.Fatalf("expected raw MCP event to be applied")
	}
	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 MCP row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if !boolFromAny(row["mcp_activity"]) {
		t.Fatalf("expected mcp_activity=true, got %#v", row["mcp_activity"])
	}
	if got := stringFromAny(row["actor"]); got != "legacy_owner" {
		t.Fatalf("expected legacy_owner actor on MCP row, got %q", got)
	}
	if got := stringFromAny(row["mcp_activity_target"]); got != "github.search_code" {
		t.Fatalf("expected target github.search_code, got %q", got)
	}
	if got := stringFromAny(row["content"]); !strings.Contains(got, "Found 12 matches in 3 files") {
		t.Fatalf("expected MCP summary text, got %q", got)
	}
}

func TestCodingCompactBuilder_RawEventMCPFunctionArgumentsVariant(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 6, 49, 22, 0, time.UTC)
	raw, err := json.Marshal(map[string]any{
		"type": "item.updated",
		"item": map[string]any{
			"type": "tool_call",
			"function": map[string]any{
				"name":      "mcp__exa__web_search_exa",
				"arguments": `{"query":"latest codex app-server docs"}`,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal raw event: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(raw), Actor: "chat"}, now) {
		t.Fatalf("expected raw MCP function event to be applied")
	}
	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 MCP row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if !boolFromAny(row["mcp_activity"]) {
		t.Fatalf("expected mcp_activity=true, got %#v", row["mcp_activity"])
	}
	if got := stringFromAny(row["mcp_activity_status"]); got != "running" {
		t.Fatalf("expected running MCP status, got %q", got)
	}
	if got := stringFromAny(row["mcp_activity_target"]); got != "exa.web_search_exa" {
		t.Fatalf("expected target exa.web_search_exa, got %q", got)
	}
	if got := stringFromAny(row["content"]); !strings.Contains(strings.ToLower(got), "running mcp: exa.web_search_exa") {
		t.Fatalf("unexpected MCP content: %q", got)
	}
}

func TestCodingCompactBuilder_RawEventSubagentCollabWaitVariant(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 27, 6, 49, 23, 0, time.UTC)
	raw, err := json.Marshal(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":                "collab_tool_call",
			"receiver_thread_ids": []any{"agent-thread-1", "agent-thread-2"},
			"function": map[string]any{
				"name": "wait",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal raw event: %v", err)
	}
	if !b.Apply(provider.ChatEvent{Type: "raw_event", Text: string(raw), Actor: "legacy_owner"}, now) {
		t.Fatalf("expected raw subagent collab wait event to be applied")
	}
	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 subagent row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "subagent" {
		t.Fatalf("expected subagent role, got %q", got)
	}
	if got := stringFromAny(row["actor"]); got != "legacy_owner" {
		t.Fatalf("expected legacy_owner actor on subagent row, got %q", got)
	}
	if got := stringFromAny(row["subagent_tool"]); got != "wait" {
		t.Fatalf("expected wait tool, got %q", got)
	}
	if got := stringFromAny(row["subagent_status"]); got != "running" {
		t.Fatalf("expected running subagent status, got %q", got)
	}
	ids, _ := row["subagent_ids"].([]string)
	if len(ids) != 2 {
		t.Fatalf("expected 2 subagent ids, got %#v", row["subagent_ids"])
	}
}

func TestCodingCompactBuilder_SubagentActivityOnlyPreserved(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 2, 3, 4, 0, time.UTC)

	text := "• Waiting subagent agent-123\n└ collecting results"
	if !b.Apply(provider.ChatEvent{Type: "activity", Text: text, Actor: "legacy_owner"}, now) {
		t.Fatalf("expected subagent activity-only event to be applied")
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 row, got %d", len(snapshot))
	}
	row := snapshot[0]
	if got := stringFromAny(row["role"]); got != "subagent" {
		t.Fatalf("expected subagent role, got %q", got)
	}
	if got := stringFromAny(row["actor"]); got != "legacy_owner" {
		t.Fatalf("expected legacy_owner actor on subagent row, got %q", got)
	}
	if got := stringFromAny(row["subagent_tool"]); got != "wait_agent" {
		t.Fatalf("expected wait_agent tool, got %q", got)
	}
	if got := stringFromAny(row["subagent_status"]); got != "running" {
		t.Fatalf("expected running status, got %q", got)
	}
	if got := stringFromAny(row["subagent_summary"]); !strings.Contains(got, "collecting results") {
		t.Fatalf("expected summary from activity-only payload, got %q", got)
	}
}

func TestCodingCompactBuilder_AssistantFinalDoesNotSplitAcrossMetaActivity(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 2, 3, 4, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "delta", Text: "Hel"}, now) {
		t.Fatalf("expected first delta")
	}
	if !b.Apply(provider.ChatEvent{Type: "activity", Text: "Background heartbeat"}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected generic meta activity")
	}
	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "Hello"}, now.Add(20*time.Millisecond)) {
		t.Fatalf("expected final assistant")
	}

	snapshot := b.Snapshot()
	assistantCount := 0
	finalAssistant := ""
	for _, row := range snapshot {
		if stringFromAny(row["role"]) == "assistant" {
			assistantCount++
			finalAssistant = stringFromAny(row["content"])
		}
	}
	if assistantCount != 2 {
		t.Fatalf("expected assistant message and final assistant row, got %d", assistantCount)
	}
	if finalAssistant != "Hello" {
		t.Fatalf("expected final assistant content, got %q", finalAssistant)
	}
}

func TestCodingCompactBuilder_AssistantMessagesAppendAsSeparateRows(t *testing.T) {
	b := newCodingCompactBuilder()
	now := time.Date(2026, 3, 24, 2, 4, 5, 0, time.UTC)

	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "First assistant bubble"}, now) {
		t.Fatalf("expected first assistant message")
	}
	if !b.Apply(provider.ChatEvent{Type: "assistant_message", Text: "Second assistant bubble"}, now.Add(10*time.Millisecond)) {
		t.Fatalf("expected second assistant message")
	}

	snapshot := b.Snapshot()
	var assistantContents []string
	for _, row := range snapshot {
		if strings.EqualFold(stringFromAny(row["role"]), "assistant") {
			assistantContents = append(assistantContents, stringFromAny(row["content"]))
		}
	}
	if len(assistantContents) != 2 {
		t.Fatalf("expected 2 assistant rows, got %#v", assistantContents)
	}
	if assistantContents[0] != "First assistant bubble" || assistantContents[1] != "Second assistant bubble" {
		t.Fatalf("expected assistant messages to preserve order, got %#v", assistantContents)
	}
}
