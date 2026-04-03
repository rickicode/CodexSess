import test from "node:test";
import assert from "node:assert/strict";

import {
  collectLiveMessageIDs,
  completedViewStatus,
  currentRecoveryStatus,
  isInternalRunnerActivity,
  messageDisplayContent,
  parsePlanningFinalPlan,
  projectMessagesForView,
  subagentDisplayTitle,
  subagentPreview,
} from "./messageView.js";
import { buildExecAwareMessages } from "./liveMessagePipeline.js";
import { reconcileLiveMessagesWithPersisted } from "../../lib/coding/messageMerge.js";

test("completed view status treats generic legacy_owner stop wording as waiting", () => {
  const status = completedViewStatus(
    [
      {
        id: "legacy_owner-stop",
        role: "assistant",
        actor: "legacy_owner",
        content: "Stopped for follow-up: waiting for user input",
      },
    ],
    {
      messageActor: (message) => String(message?.actor || "").trim().toLowerCase(),
    },
  );

  assert.equal(status, "Response received.");
});

test("currentRecoveryStatus surfaces retry progress from the latest recovery milestone", () => {
  const status = currentRecoveryStatus([
    {
      id: "recovery-detected",
      role: "activity",
      content: "runtime.recovery_detected role=chat reason=usage_limit",
    },
    {
      id: "switch-started",
      role: "activity",
      content: "account.switch_started role=chat",
    },
  ]);

  assert.equal(status, "Retrying with another account...");
});

test("currentRecoveryStatus clears after retry flow continues successfully", () => {
  const status = currentRecoveryStatus([
    {
      id: "switch-started",
      role: "activity",
      content: "account.switch_started role=chat",
    },
    {
      id: "continue-started",
      role: "activity",
      content: "turn.continue_started role=chat thread_id=thread_retry",
    },
  ]);

  assert.equal(status, "");
});

test("collectLiveMessageIDs only keeps active pending ids in the live format", () => {
  const ids = collectLiveMessageIDs([
    {
      id: "pending-1712040000000-abc123",
      role: "assistant",
    },
    {
      id: "pending_1712040000000_legacy",
      role: "assistant",
    },
    {
      id: "pending-legacy-placeholder",
      role: "assistant",
    },
  ]);

  assert.deepEqual(ids, ["pending-1712040000000-abc123"]);
});

test("projectMessagesForView collapses adjacent exec running-to-done transition", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "exec-run-1",
        role: "exec",
        actor: "executor",
        lane: "executor",
        exec_command: "rtk go test ./internal/httpapi",
        exec_status: "running",
        exec_exit_code: 0,
        exec_output: "",
        created_at: "2026-03-29T10:00:00Z",
        updated_at: "2026-03-29T10:00:00Z",
      },
      {
        id: "exec-done-1",
        role: "exec",
        actor: "executor",
        lane: "executor",
        exec_command: "rtk go test ./internal/httpapi",
        exec_status: "done",
        exec_exit_code: 0,
        exec_output: "ok",
        created_at: "2026-03-29T10:00:01Z",
        updated_at: "2026-03-29T10:00:01Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].role, "exec");
  assert.equal(rendered[0].exec_status, "done");
  assert.equal(rendered[0].exec_output, "ok");
});

test("projectMessagesForView collapses non-adjacent exec running-to-done transition", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "exec-run-2",
        role: "exec",
        actor: "legacy_owner",
        lane: "legacy_owner",
        exec_command: "rtk git status --porcelain=v1",
        exec_status: "running",
        exec_exit_code: 0,
        exec_output: "",
        created_at: "2026-03-30T06:31:00Z",
        updated_at: "2026-03-30T06:31:00Z",
      },
      {
        id: "activity-between",
        role: "activity",
        actor: "legacy_owner",
        lane: "legacy_owner",
        content: "Legacy Owner reviewing executor turn",
        created_at: "2026-03-30T06:31:01Z",
        updated_at: "2026-03-30T06:31:01Z",
      },
      {
        id: "exec-done-2",
        role: "exec",
        actor: "legacy_owner",
        lane: "legacy_owner",
        exec_command: "rtk git status --porcelain=v1",
        exec_status: "failed",
        exec_exit_code: 128,
        exec_output: "fatal: not a git repository",
        created_at: "2026-03-30T06:31:02Z",
        updated_at: "2026-03-30T06:31:02Z",
      },
    ],
    { alreadyCanonical: true },
  );

  const execRows = rendered.filter((row) => String(row?.role || "").toLowerCase() === "exec");
  assert.equal(execRows.length, 1);
  assert.equal(execRows[0].exec_status, "failed");
  assert.equal(execRows[0].exec_exit_code, 128);
  assert.match(String(execRows[0].exec_output || ""), /not a git repository/i);
});

test("projectMessagesForView collapses exec transition when lane metadata is missing on terminal update", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "exec-run-3",
        role: "exec",
        actor: "legacy_owner",
        lane: "legacy_owner",
        exec_command: "rtk read docs/superpowers/plans/2026-03-30-sample.md",
        exec_status: "running",
        exec_exit_code: 0,
        exec_output: "",
        created_at: "2026-03-30T06:35:10Z",
        updated_at: "2026-03-30T06:35:10Z",
      },
      {
        id: "exec-done-3",
        role: "exec",
        actor: "legacy_owner",
        lane: "",
        exec_command: "rtk read docs/superpowers/plans/2026-03-30-sample.md",
        exec_status: "done",
        exec_exit_code: 0,
        exec_output: "# sample",
        created_at: "2026-03-30T06:35:12Z",
        updated_at: "2026-03-30T06:35:12Z",
      },
    ],
    { alreadyCanonical: true },
  );

  const execRows = rendered.filter((row) => String(row?.role || "").toLowerCase() === "exec");
  assert.equal(execRows.length, 1);
  assert.equal(execRows[0].exec_status, "done");
});

test("projectMessagesForView collapses duplicate terminal rows with equivalent display command", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "exec-done-dup-1",
        role: "exec",
        actor: "legacy_owner",
        lane: "legacy_owner",
        exec_command: "ls",
        exec_status: "done",
        exec_exit_code: 0,
        exec_output: "",
        created_at: "2026-03-30T11:51:05Z",
        updated_at: "2026-03-30T11:51:05Z",
      },
      {
        id: "exec-done-dup-2",
        role: "exec",
        actor: "legacy_owner",
        lane: "legacy_owner",
        exec_command: "/bin/bash -lc ls",
        exec_status: "done",
        exec_exit_code: 0,
        exec_output: "",
        created_at: "2026-03-30T11:51:05Z",
        updated_at: "2026-03-30T11:51:05Z",
      },
    ],
    { alreadyCanonical: true },
  );

  const execRows = rendered.filter((row) => String(row?.role || "").toLowerCase() === "exec");
  assert.equal(execRows.length, 1);
});

test("projectMessagesForView keeps terminal status when out-of-order running row arrives after done", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "exec-outoforder-1",
        role: "exec",
        actor: "legacy_owner",
        lane: "legacy_owner",
        exec_command: "cat /home/ricki/.codex/superpowers/skills/brainstorming/SKILL.md",
        exec_status: "done",
        exec_exit_code: 0,
        exec_output: "ok",
        created_at: "2026-03-30T11:51:18Z",
        updated_at: "2026-03-30T11:51:18Z",
      },
      {
        id: "exec-outoforder-2",
        role: "exec",
        actor: "legacy_owner",
        lane: "legacy_owner",
        exec_command: "cat /home/ricki/.codex/superpowers/skills/brainstorming/SKILL.md",
        exec_status: "running",
        exec_exit_code: 0,
        exec_output: "",
        created_at: "2026-03-30T11:51:18Z",
        updated_at: "2026-03-30T11:51:18Z",
      },
    ],
    { alreadyCanonical: true },
  );

  const execRows = rendered.filter((row) => String(row?.role || "").toLowerCase() === "exec");
  assert.equal(execRows.length, 1);
  assert.equal(execRows[0].exec_status, "done");
});

test("canonical subagent rows collapse to the richer latest entry", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "subagent-1",
        role: "subagent",
        actor: "executor",
        lane: "executor",
        created_at: "2026-03-25T00:00:00Z",
        updated_at: "2026-03-25T00:00:00Z",
        subagent_tool: "spawn_agent",
        subagent_status: "running",
        subagent_name: "Lagrange",
        subagent_prompt: "Map the code path",
      },
      {
        id: "subagent-2",
        role: "subagent",
        actor: "executor",
        lane: "executor",
        created_at: "2026-03-25T00:00:01Z",
        updated_at: "2026-03-25T00:00:02Z",
        subagent_tool: "spawn_agent",
        subagent_status: "done",
        subagent_name: "Lagrange",
        subagent_prompt: "Map the code path",
        subagent_summary: "Mapped the relevant files.",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].id, "subagent-2");
  assert.equal(rendered[0].subagent_status, "done");
});

test("subagent rich title and preview include model tier and task summary", () => {
  const message = {
    id: "subagent-rich",
    role: "subagent",
    subagent_tool: "spawn_agent",
    subagent_status: "running",
    subagent_name: "Ptolemy",
    subagent_role: "code-review",
    subagent_model: "gpt-5.3-codex",
    subagent_reasoning: "high",
    subagent_prompt: "Re-review the current /chat architecture after recent fixes in the working tree.",
  };

  assert.equal(
    subagentDisplayTitle(message),
    "Spawned Ptolemy [code-review] (gpt-5.3-codex high)",
  );
  assert.equal(
    subagentPreview(message),
    "└ Re-review the current /chat architecture after recent fixes in the working tree.",
  );
});

test("subagent preview prefers the completed summary over the original prompt", () => {
  const message = {
    id: "subagent-summary",
    role: "subagent",
    subagent_tool: "spawn_agent",
    subagent_status: "done",
    subagent_name: "Kepler",
    subagent_role: "browser-debugger",
    subagent_prompt: "Trace the live /chat rendering path.",
    subagent_summary: "Mapped the timeline helpers and isolated the merge boundary.",
  };

  assert.equal(
    subagentPreview(message),
    "└ Mapped the timeline helpers and isolated the merge boundary.",
  );
});

test("canonical view keeps non-spam events, hides protocol spam, and dedupes identical stderr", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "activity-thread-started",
        role: "activity",
        content: "thread/started: {\"thread\":{\"id\":\"thread-old\"}}",
        created_at: "2026-03-26T05:18:21Z",
        updated_at: "2026-03-26T05:18:21Z",
      },
      {
        id: "stderr-a",
        role: "stderr",
        content: "[redacted]",
        created_at: "2026-03-26T05:18:22Z",
        updated_at: "2026-03-26T05:18:22Z",
      },
      {
        id: "stderr-b",
        role: "stderr",
        actor: "legacy_owner",
        lane: "legacy_owner",
	content: "Run failed: codex runtime rate limited or quota exhausted (429)",
        created_at: "2026-03-26T05:18:23Z",
        updated_at: "2026-03-26T05:18:23Z",
      },
      {
        id: "stderr-c",
        role: "stderr",
        actor: "legacy_owner",
        lane: "legacy_owner",
	content: "Run failed: codex runtime rate limited or quota exhausted (429)",
        created_at: "2026-03-26T05:18:24Z",
        updated_at: "2026-03-26T05:18:24Z",
      },
      {
        id: "event-a",
        role: "event",
        content: "App-server event",
        created_at: "2026-03-26T05:18:25Z",
        updated_at: "2026-03-26T05:18:25Z",
      },
      {
        id: "event-spam",
        role: "event",
        content: "item/started: {\"type\":\"item.started\"}",
        created_at: "2026-03-26T05:18:25Z",
        updated_at: "2026-03-26T05:18:25Z",
      },
      {
        id: "assistant-a",
        role: "assistant",
        content: "autoswitched after multiple quota failures",
        created_at: "2026-03-26T05:18:26Z",
        updated_at: "2026-03-26T05:18:26Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 3);
  assert.equal(rendered[0].id, "stderr-c");
  assert.equal(rendered[0].role, "stderr");
  assert.equal(
    rendered[0].content,
    "Run failed: codex runtime rate limited or quota exhausted (429)",
  );
  assert.equal(rendered[1].id, "event-a");
  assert.equal(rendered[1].role, "event");
  assert.equal(rendered[2].id, "assistant-a");
  assert.equal(rendered[2].role, "assistant");
});

test("canonical view keeps a single chronological chat timeline even when runtime rows have no actor or lane owner", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "assistant-chat",
        role: "assistant",
        content: "Here is the current status.",
        created_at: "2026-03-30T01:00:00Z",
        updated_at: "2026-03-30T01:00:00Z",
      },
      {
        id: "exec-ownerless",
        role: "exec",
        exec_command: "rtk go test ./internal/httpapi",
        exec_status: "done",
        created_at: "2026-03-30T01:00:01Z",
        updated_at: "2026-03-30T01:00:01Z",
      },
      {
        id: "subagent-ownerless",
        role: "subagent",
        subagent_tool: "wait_agent",
        subagent_status: "running",
        subagent_name: "Kepler",
        subagent_title: "Waiting for Kepler",
        created_at: "2026-03-30T01:00:02Z",
        updated_at: "2026-03-30T01:00:02Z",
      },
      {
        id: "mcp-ownerless",
        role: "activity",
        mcp_activity: true,
        content: "MCP done: github.search_code",
        created_at: "2026-03-30T01:00:03Z",
        updated_at: "2026-03-30T01:00:03Z",
      },
      {
        id: "file-ownerless",
        role: "activity",
        file_op: "Read: /tmp/readme.md",
        content: "Read: /tmp/readme.md",
        created_at: "2026-03-30T01:00:04Z",
        updated_at: "2026-03-30T01:00:04Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.deepEqual(
    rendered.map((row) => row.id),
    [
      "assistant-chat",
      "exec-ownerless",
      "subagent-ownerless",
      "mcp-ownerless",
      "file-ownerless",
    ],
  );
  assert.equal(rendered[1].role, "exec");
  assert.equal(rendered[2].role, "subagent");
  assert.equal(rendered[3].mcp_activity, true);
  assert.equal(rendered[4].file_op, "Read: /tmp/readme.md");
});

test("live projection keeps ownerless runtime rows visible on the chat timeline", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "assistant-chat-live",
        role: "assistant",
        content: "Checking the workspace.",
        created_at: "2026-03-30T02:00:00Z",
        updated_at: "2026-03-30T02:00:00Z",
      },
      {
        id: "raw-exec-ownerless",
        role: "event",
        content: JSON.stringify({
          method: "item/completed",
          params: {
            item: {
              type: "commandExecution",
              command: "pwd",
              aggregatedOutput: "/tmp/workspace",
              exitCode: 0,
            },
          },
        }),
        created_at: "2026-03-30T02:00:01Z",
        updated_at: "2026-03-30T02:00:01Z",
      },
      {
        id: "raw-subagent-ownerless",
        role: "event",
        content: JSON.stringify({
          type: "item.started",
          item: {
            type: "collab_tool_call",
            receiver_thread_ids: ["agent-thread-1"],
            function: { name: "wait" },
          },
        }),
        created_at: "2026-03-30T02:00:02Z",
        updated_at: "2026-03-30T02:00:02Z",
      },
      {
        id: "raw-mcp-ownerless",
        role: "event",
        content: JSON.stringify({
          type: "item.completed",
          item: {
            type: "tool_call",
            name: "mcp__github__search_code",
            output: { summary: "Found 12 matches" },
          },
        }),
        created_at: "2026-03-30T02:00:03Z",
        updated_at: "2026-03-30T02:00:03Z",
      },
      {
        id: "raw-file-ownerless",
        role: "event",
        content: JSON.stringify({
          method: "item/completed",
          params: {
            item: {
              type: "fileRead",
              path: "/tmp/readme.md",
            },
          },
        }),
        created_at: "2026-03-30T02:00:04Z",
        updated_at: "2026-03-30T02:00:04Z",
      },
    ],
    { buildExecAwareMessages },
  );

  assert.deepEqual(
    rendered.map((row) => row.id),
    [
      "assistant-chat-live",
      "exec-raw-exec-ownerless",
      "subagent-raw-subagent-ownerless",
      "mcp-raw-mcp-ownerless",
      "fileop-raw-file-ownerless",
    ],
  );
  assert.equal(rendered[1].role, "exec");
  assert.equal(rendered[2].role, "subagent");
  assert.equal(rendered[3].mcp_activity, true);
  assert.equal(rendered[4].file_op, "Read: /tmp/readme.md");
});

test("runtime recovery activity stays visible in canonical view and is humanized", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "activity-recovery",
        role: "activity",
        actor: "legacy_owner",
        lane: "legacy_owner",
        content: "thread.resume_started role=legacy_owner thread_id=thread_orch_123",
        created_at: "2026-03-27T03:04:05Z",
        updated_at: "2026-03-27T03:04:05Z",
      },
      {
        id: "assistant-after",
        role: "assistant",
        actor: "legacy_owner",
        content: "CONTINUE: proceed with the next validation pass.",
        created_at: "2026-03-27T03:04:06Z",
        updated_at: "2026-03-27T03:04:06Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 2);
  assert.equal(rendered[0].id, "activity-recovery");
  assert.equal(
    messageDisplayContent(rendered[0]),
    "Resuming runtime thread: thread_orch_123",
  );
});

test("runtime recovery failure is summarized once and humanized", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "activity-recovery-failed",
        role: "activity",
        actor: "legacy_owner",
        lane: "legacy_owner",
        content: "thread.resume_failed attempts=5 role=chat thread_id=thread_chat_123 reason=no_rollout_found",
        created_at: "2026-03-27T03:04:05Z",
        updated_at: "2026-03-27T03:04:05Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(
    messageDisplayContent(rendered[0]),
    "Resume failed for chat thread: thread_chat_123 after 5 attempts (no_rollout_found)",
  );
});

test("runtime recovery step noise is collapsed to the essential milestones", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "recovery-detected",
        role: "activity",
        recovery_kind: "recovery_detected",
        content: "Recovery detected for legacy_owner runtime (usage_limit)",
        created_at: "2026-03-28T02:01:17Z",
        updated_at: "2026-03-28T02:01:17Z",
      },
      {
        id: "interrupt",
        role: "activity",
        recovery_kind: "interrupt_requested",
        content: "Interrupt requested for legacy_owner runtime",
        created_at: "2026-03-28T02:01:17Z",
        updated_at: "2026-03-28T02:01:17Z",
      },
      {
        id: "stop-started",
        role: "activity",
        recovery_kind: "stop_started",
        content: "Stopping legacy_owner runtime",
        created_at: "2026-03-28T02:01:17Z",
        updated_at: "2026-03-28T02:01:17Z",
      },
      {
        id: "stop-completed",
        role: "activity",
        recovery_kind: "stop_completed",
        content: "Stopped legacy_owner runtime",
        created_at: "2026-03-28T02:01:17Z",
        updated_at: "2026-03-28T02:01:17Z",
      },
      {
        id: "switch-started",
        role: "activity",
        recovery_kind: "account_switch_started",
        content: "Switching account for legacy_owner runtime",
        created_at: "2026-03-28T02:01:17Z",
        updated_at: "2026-03-28T02:01:17Z",
      },
      {
        id: "switch-completed",
        role: "activity",
        recovery_kind: "account_switch_completed",
        content: "Switched account for legacy_owner runtime: codex_123",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "auth-started",
        role: "activity",
        recovery_kind: "auth_sync_started",
        content: "Auth sync started for legacy_owner runtime",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "auth-completed",
        role: "activity",
        recovery_kind: "auth_sync_completed",
        content: "Auth sync completed for legacy_owner runtime",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "restart-started",
        role: "activity",
        recovery_kind: "restart_started",
        content: "Restarting legacy_owner runtime",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "restart-completed",
        role: "activity",
        recovery_kind: "restart_completed",
        content: "Restarted legacy_owner runtime",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "continue-started",
        role: "activity",
        recovery_kind: "continue_started",
        content: "Continuing legacy_owner runtime after recovery",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].id, "continue-started");
  assert.match(rendered[0].content, /Recovery detected for legacy_owner runtime/);
  assert.match(rendered[0].content, /Switched account for legacy_owner runtime: codex_123/);
  assert.match(rendered[0].content, /Continuing legacy_owner runtime after recovery/);
});

test("runtime recovery summaries stay split by cycle", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "cycle-1-detected",
        role: "activity",
        actor: "legacy_owner",
        content: "runtime.recovery_detected role=legacy_owner reason=usage_limit",
        created_at: "2026-03-28T02:01:17Z",
        updated_at: "2026-03-28T02:01:17Z",
      },
      {
        id: "cycle-1-switch",
        role: "activity",
        actor: "legacy_owner",
        content: "account.switch_completed role=legacy_owner account_id=codex_111",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "cycle-1-continue",
        role: "activity",
        actor: "legacy_owner",
        content: "turn.continue_started role=legacy_owner thread_id=thread_a",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "cycle-2-detected",
        role: "activity",
        actor: "legacy_owner",
        content: "runtime.recovery_detected role=legacy_owner reason=usage_limit",
        created_at: "2026-03-28T02:01:25Z",
        updated_at: "2026-03-28T02:01:25Z",
      },
      {
        id: "cycle-2-switch",
        role: "activity",
        actor: "legacy_owner",
        content: "account.switch_completed role=legacy_owner account_id=codex_222",
        created_at: "2026-03-28T02:01:26Z",
        updated_at: "2026-03-28T02:01:26Z",
      },
      {
        id: "cycle-2-continue",
        role: "activity",
        actor: "legacy_owner",
        content: "turn.continue_started role=legacy_owner thread_id=thread_b",
        created_at: "2026-03-28T02:01:26Z",
        updated_at: "2026-03-28T02:01:26Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 2);
  assert.match(rendered[0].content, /codex_111/);
  assert.match(rendered[1].content, /codex_222/);
});

test("runtime recovery summary prefers account email over account id", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "cycle-email-detected",
        role: "activity",
        actor: "legacy_owner",
        content: "runtime.recovery_detected role=legacy_owner reason=usage_limit",
        created_at: "2026-03-28T02:01:17Z",
        updated_at: "2026-03-28T02:01:17Z",
      },
      {
        id: "cycle-email-switch",
        role: "activity",
        actor: "legacy_owner",
        content: "account.switch_completed role=legacy_owner account_email=orch@example.com account_id=codex_hidden",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
      {
        id: "cycle-email-continue",
        role: "activity",
        actor: "legacy_owner",
        content: "turn.continue_started role=legacy_owner thread_id=thread_a",
        created_at: "2026-03-28T02:01:18Z",
        updated_at: "2026-03-28T02:01:18Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.match(rendered[0].content, /orch@example.com/);
  assert.doesNotMatch(rendered[0].content, /codex_hidden/);
});

test("canonical MCP activity stays visible as a specialized row", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "mcp-a",
        role: "activity",
        actor: "legacy_owner",
        lane: "legacy_owner",
        content: "MCP failed: git.status\n  └ handshake failed",
        mcp_activity: true,
        created_at: "2026-03-27T06:49:21Z",
        updated_at: "2026-03-27T06:49:21Z",
      },
      {
        id: "assistant-a",
        role: "assistant",
        content: "Saya lanjutkan cek repo.",
        created_at: "2026-03-27T06:49:22Z",
        updated_at: "2026-03-27T06:49:22Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 2);
  assert.equal(rendered[0].id, "mcp-a");
  assert.equal(rendered[0].mcp_activity, true);
  assert.equal(
    messageDisplayContent(rendered[0]),
    "MCP failed: git.status\n  └ handshake failed",
  );
});

test("messageDisplayContent humanizes running web-search MCP activity", () => {
  const text = messageDisplayContent({
    role: "activity",
    content: "Running MCP: exa.web_search_exa",
    mcp_activity: true,
    mcp_activity_target: "exa.web_search_exa",
  });

  assert.equal(text, "Searching the web");
});

test("messageDisplayContent humanizes completed code-search MCP activity", () => {
  const text = messageDisplayContent({
    role: "activity",
    content: "MCP done: github.search_code\n  └ Found 12 matches",
    mcp_activity: true,
    mcp_activity_target: "github.search_code",
  });

  assert.equal(text, "Searched code");
});

test("canonical terminal and file operation rows stay visible", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "exec-1",
        role: "exec",
        actor: "executor",
        lane: "executor",
        exec_command: "pwd",
        exec_status: "done",
        exec_output: "[redacted]",
        created_at: "2026-03-28T00:10:00Z",
        updated_at: "2026-03-28T00:10:02Z",
      },
      {
        id: "file-1",
        role: "activity",
        actor: "executor",
        lane: "executor",
        content: "Edited: /tmp/app.go (+5 -2)",
        file_op: "Edited: /tmp/app.go (+5 -2)",
        created_at: "2026-03-28T00:10:03Z",
        updated_at: "2026-03-28T00:10:03Z",
      },
      {
        id: "file-2",
        role: "activity",
        actor: "executor",
        lane: "executor",
        content: "Deleted: /tmp/old.go",
        file_op: "Deleted: /tmp/old.go",
        created_at: "2026-03-28T00:10:04Z",
        updated_at: "2026-03-28T00:10:04Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 3);
  assert.equal(rendered[0].role, "exec");
  assert.equal(rendered[1].file_op, "Edited: /tmp/app.go (+5 -2)");
  assert.equal(rendered[2].file_op, "Deleted: /tmp/old.go");
});

test("canonical view keeps terminal interaction activity visible", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "terminal-interaction",
        role: "activity",
        actor: "chat",
        content: "Terminal interaction",
        created_at: "2026-04-02T12:00:00Z",
        updated_at: "2026-04-02T12:00:00Z",
      },
      {
        id: "assistant-after",
        role: "assistant",
        content: "Done.",
        created_at: "2026-04-02T12:00:01Z",
        updated_at: "2026-04-02T12:00:01Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 2);
  assert.equal(rendered[0].id, "terminal-interaction");
});

test("generic activity rows with legacy legacy_owner metadata do not render as internal runner activity", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "activity-generic-1",
        role: "activity",
        actor: "legacy_owner",
        lane: "legacy_owner",
        content: "Summarizing the current timeline state.",
        created_at: "2026-04-02T03:10:00Z",
        updated_at: "2026-04-02T03:10:00Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].role, "activity");
  assert.equal(
    isInternalRunnerActivity(rendered[0]),
    false,
  );
  assert.equal(
    messageDisplayContent(rendered[0]),
    "Summarizing the current timeline state.",
  );
});

test("canonical view hides executor empty command-output noise", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "activity-empty-output",
        role: "activity",
        actor: "executor",
        content: "Command output: .",
        created_at: "2026-03-28T00:10:00Z",
        updated_at: "2026-03-28T00:10:00Z",
      },
      {
        id: "activity-real-output",
        role: "activity",
        actor: "executor",
        content: "Command output: 28 passed in 0.08s",
        created_at: "2026-03-28T00:10:01Z",
        updated_at: "2026-03-28T00:10:01Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].id, "activity-real-output");
});

test("canonical view hides empty command-output noise even without legacy owner metadata", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "activity-empty-ownerless",
        role: "activity",
        content: "Command output: .",
        created_at: "2026-04-02T03:12:00Z",
        updated_at: "2026-04-02T03:12:00Z",
      },
      {
        id: "activity-real-ownerless",
        role: "activity",
        content: "Command output: 28 passed in 0.08s",
        created_at: "2026-04-02T03:12:01Z",
        updated_at: "2026-04-02T03:12:01Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].id, "activity-real-ownerless");
});

test("canonical file operation rows dedupe redacted and concrete home paths", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "file-1",
        role: "activity",
        actor: "executor",
        lane: "executor",
        content: "Edited: /home/ricki/workspaces/codexsess/app.go (+5 -2)",
        file_op: "Edited: /home/ricki/workspaces/codexsess/app.go (+5 -2)",
        created_at: "2026-03-28T00:10:03Z",
        updated_at: "2026-03-28T00:10:03Z",
      },
      {
        id: "file-2",
        role: "activity",
        actor: "executor",
        lane: "executor",
        content: "Edited: /home/[user]/workspaces/codexsess/app.go (+5 -2)",
        file_op: "Edited: /home/[user]/workspaces/codexsess/app.go (+5 -2)",
        created_at: "2026-03-28T00:10:04Z",
        updated_at: "2026-03-28T00:10:04Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.match(rendered[0].file_op, /^Edited: \/home\/(?:ricki|\[user\])\/workspaces\/codexsess\/app\.go/);
});

test("canonical view hides generic MCP startup status rows", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "mcp-generic",
        role: "activity",
        actor: "legacy_owner",
        lane: "legacy_owner",
        content: 'MCP server status: {"name":"filesystem","status":"starting","error":null}',
        mcp_activity: true,
        mcp_activity_generic: true,
        created_at: "2026-03-27T06:49:20Z",
        updated_at: "2026-03-27T06:49:20Z",
      },
      {
        id: "mcp-failed",
        role: "activity",
        actor: "legacy_owner",
        lane: "legacy_owner",
        content: "MCP failed: filesystem.list\n  └ handshake failed",
        mcp_activity: true,
        created_at: "2026-03-27T06:49:21Z",
        updated_at: "2026-03-27T06:49:21Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].id, "mcp-failed");
});

test("canonical view hides event log truncated noise rows", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "truncated-noise",
        role: "activity",
        actor: "chat",
        content: "Event log truncated: 57 additional entries omitted.",
        created_at: "2026-04-02T12:00:00Z",
        updated_at: "2026-04-02T12:00:00Z",
      },
      {
        id: "assistant-after",
        role: "assistant",
        content: "Final answer.",
        created_at: "2026-04-02T12:00:02Z",
        updated_at: "2026-04-02T12:00:02Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].id, "assistant-after");
});

test("canonical view hides empty pending assistant placeholders so streaming renders outside bubbles", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "assistant-pending-empty",
        role: "assistant",
        actor: "chat",
        pending: true,
        content: "",
        created_at: "2026-04-02T08:33:58Z",
        updated_at: "2026-04-02T08:33:59Z",
      },
      {
        id: "assistant-done",
        role: "assistant",
        actor: "chat",
        pending: false,
        content: "Final answer.",
        created_at: "2026-04-02T08:34:10Z",
        updated_at: "2026-04-02T08:34:10Z",
      },
    ],
    { alreadyCanonical: true },
  );

  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].id, "assistant-done");
});


test("parsePlanningFinalPlan extracts structured final plan sections", () => {
  const parsed = parsePlanningFinalPlan({
    role: "assistant",
    actor: "legacy_owner",
    content: `# Login Reliability Implementation Plan

**Goal:** Perbaiki login agar POST /api/auth/login tidak lagi 500.

### Task 1: Reproduce and trace
- [ ] Reproduce 500.
- [ ] Trace auth handler.

## Stop Conditions
- Stop if repro berubah.
- Ask before schema changes.

Confidence: 91%`,
  });

  assert.ok(parsed);
  assert.equal(parsed.summary, "Perbaiki login agar POST /api/auth/login tidak lagi 500.");
  assert.deepEqual(parsed.tasks, ["Reproduce 500.", "Trace auth handler."]);
  assert.deepEqual(parsed.stopConditions, ["Stop if repro berubah.", "Ask before schema changes."]);
  assert.equal(parsed.ready, true);
  assert.equal(parsed.confidence, 91);
});

test("parsePlanningFinalPlan accepts canonical assistant role rows", () => {
  const parsed = parsePlanningFinalPlan({
    role: "assistant",
    content: `# Login Reliability Implementation Plan

**Goal:** Perbaiki login agar POST /api/auth/login tidak lagi 500.

### Task 1: Reproduce and trace
- [ ] Reproduce 500.
- [ ] Trace auth handler.

Confidence: 91%`,
  });

  assert.ok(parsed);
  assert.equal(parsed.ready, true);
  assert.equal(parsed.confidence, 91);
});

test("assistant delta only updates the matching source item bubble", () => {
  const current = [
    {
      id: "stream-assistant-1",
      role: "assistant",
      actor: "",
      lane: "",
      content: "First bubble",
      pending: true,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-1",
      source_item_type: "agentmessage",
      created_at: "2026-04-02T12:20:00.000Z",
      updated_at: "2026-04-02T12:20:00.000Z",
    },
    {
      id: "stream-assistant-2",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Second bubble",
      pending: true,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-2",
      source_item_type: "agentmessage",
      created_at: "2026-04-02T12:20:00.010Z",
      updated_at: "2026-04-02T12:20:00.010Z",
    },
  ];
  const persisted = [
    {
      id: "db-assistant-2",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Second bubble finalized",
      pending: false,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-2",
      source_item_type: "agentmessage",
      created_at: "2026-04-02T12:20:00.020Z",
      updated_at: "2026-04-02T12:20:00.020Z",
    },
  ];

  const merged = reconcileLiveMessagesWithPersisted(
    current,
    persisted,
    collectLiveMessageIDs(current),
  );

  assert.deepEqual(
    merged.map((row) => row.id),
    ["stream-assistant-1", "db-assistant-2"],
  );
  assert.equal(merged[0]?.content, "First bubble");
  assert.equal(merged[1]?.content, "Second bubble finalized");
  assert.equal(merged[0]?.source_item_id, "item-assistant-1");
  assert.equal(merged[1]?.source_item_id, "item-assistant-2");
});

test("assistant source identity matches across agent_message and agentMessage variants", () => {
  const current = [
    {
      id: "stream-assistant-1",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Saya akan cek instruksi sesi dulu.",
      pending: true,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-1",
      source_item_type: "agent_message",
      created_at: "2026-04-02T12:20:00.000Z",
      updated_at: "2026-04-02T12:20:00.000Z",
    },
  ];
  const persisted = [
    {
      id: "db-assistant-1",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Saya akan cek instruksi sesi dulu.",
      pending: false,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-1",
      source_item_type: "agentMessage",
      created_at: "2026-04-02T12:20:03.000Z",
      updated_at: "2026-04-02T12:20:03.000Z",
    },
  ];

  const merged = reconcileLiveMessagesWithPersisted(
    current,
    persisted,
    collectLiveMessageIDs(current),
  );

  assert.deepEqual(merged.map((row) => row.id), ["db-assistant-1"]);
});

test("projected timeline keeps assistant bubbles split around terminal activity after persisted merge", () => {
  const current = [
    {
      id: "stream-assistant-1",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Saya audit alur /chat.",
      pending: true,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-1",
      source_item_type: "agentmessage",
      created_at: "2026-04-02T12:20:00.000Z",
      updated_at: "2026-04-02T12:20:00.000Z",
    },
    {
      id: "stream-exec-1",
      role: "exec",
      actor: "",
      lane: "",
      content: "sed -n '1,220p' backend/internal/bootstrap/migrate.go",
      exec_command: "sed -n '1,220p' backend/internal/bootstrap/migrate.go",
      exec_status: "done",
      exec_output: "",
      created_at: "2026-04-02T12:20:00.050Z",
      updated_at: "2026-04-02T12:20:00.050Z",
    },
    {
      id: "stream-assistant-2",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Ada gap struktural jelas di bootstrap.",
      pending: true,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-2",
      source_item_type: "agentmessage",
      created_at: "2026-04-02T12:20:00.100Z",
      updated_at: "2026-04-02T12:20:00.100Z",
    },
  ];

  const persisted = [
    {
      id: "db-assistant-1",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Saya audit alur /chat.",
      pending: false,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-1",
      source_item_type: "agentmessage",
      created_at: "2026-04-02T12:20:00.000Z",
      updated_at: "2026-04-02T12:20:00.000Z",
    },
    {
      id: "db-assistant-2",
      role: "assistant",
      actor: "",
      lane: "",
      content: "Ada gap struktural jelas di bootstrap.",
      pending: false,
      source_turn_id: "turn-chat-1",
      source_item_id: "item-assistant-2",
      source_item_type: "agentmessage",
      created_at: "2026-04-02T12:20:00.100Z",
      updated_at: "2026-04-02T12:20:00.100Z",
    },
  ];

  const merged = reconcileLiveMessagesWithPersisted(
    current,
    persisted,
    collectLiveMessageIDs(current),
  );
  const rendered = projectMessagesForView(merged, {
    buildExecAwareMessages,
    rawMode: false,
    alreadyCanonical: true,
  });

  assert.deepEqual(
    rendered.map((row) => row.id),
    ["db-assistant-1", "stream-exec-1", "db-assistant-2"],
  );
  assert.equal(rendered[0]?.content, "Saya audit alur /chat.");
  assert.equal(rendered[1]?.role, "exec");
  assert.equal(rendered[2]?.content, "Ada gap struktural jelas di bootstrap.");
});

test("canonical view dedupes adjacent identical assistant bubbles from the same actor", () => {
  const rendered = projectMessagesForView(
    [
      {
        id: "assistant-1",
        role: "assistant",
        actor: "",
        content: "Saya akan cek instruksi sesi dulu.",
        created_at: "2026-04-02T12:20:00.000Z",
        updated_at: "2026-04-02T12:20:00.000Z",
      },
      {
        id: "assistant-2",
        role: "assistant",
        actor: "",
        content: "Saya akan cek instruksi sesi dulu.",
        created_at: "2026-04-02T12:20:03.000Z",
        updated_at: "2026-04-02T12:20:03.000Z",
      },
      {
        id: "exec-1",
        role: "exec",
        actor: "",
        content: "rtk read AGENTS.md",
        exec_command: "rtk read AGENTS.md",
        exec_status: "done",
        exec_output: "",
        created_at: "2026-04-02T12:20:04.000Z",
        updated_at: "2026-04-02T12:20:04.000Z",
      },
    ],
    {
      buildExecAwareMessages,
      rawMode: false,
      alreadyCanonical: true,
    },
  );

  assert.deepEqual(
    rendered.map((row) => row.id),
    ["assistant-2", "exec-1"],
  );
});
