import test from "node:test";
import assert from "node:assert/strict";

import {
  buildExecAwareMessages,
  parseExecEventFromPayload,
  parseRawEventPayload,
  parseSubagentActivityText,
  parseSubagentEventFromPayload,
} from "./liveMessagePipeline.js";
import { parseFileOperationPayload } from "../../lib/coding/activityParsing.js";

test("parseExecEventFromPayload supports commandExecution camelCase", () => {
  const parsed = parseExecEventFromPayload({
    type: "item.started",
    item: {
      type: "commandExecution",
      command: "pwd",
    },
  });

  assert.ok(parsed);
  assert.equal(parsed.command, "pwd");
  assert.equal(parsed.status, "running");
});

test("parseRawEventPayload flattens JSON-RPC envelope payloads", () => {
  const parsed = parseRawEventPayload(
    JSON.stringify({
      method: "item/completed",
      params: {
        item: {
          type: "commandExecution",
          command: "pwd",
          aggregatedOutput: "/tmp",
          exitCode: 0,
        },
      },
    }),
  );

  assert.ok(parsed);
  assert.equal(parsed.type, "item/completed");
  assert.equal(parsed.item?.type, "commandExecution");
  assert.equal(parsed.item?.aggregatedOutput, "/tmp");
});

test("parseExecEventFromPayload supports function_call exec_command envelopes", () => {
  const parsed = parseExecEventFromPayload({
    type: "rawResponseItem/completed",
    item: {
      type: "function_call",
      name: "exec_command",
      arguments: '{"command":"ls -la"}',
    },
  });

  assert.ok(parsed);
  assert.equal(parsed.command, "ls -la");
  assert.equal(parsed.status, "done");
});

test("parseExecEventFromPayload supports flattened envelope camelCase commandExecution", () => {
  const payload = parseRawEventPayload(
    JSON.stringify({
      method: "item/completed",
      params: {
        item: {
          type: "commandExecution",
          command: "pwd",
          aggregatedOutput: "/home/ricki",
          exitCode: 0,
        },
      },
    }),
  );

  const parsed = parseExecEventFromPayload(payload);
  assert.ok(parsed);
  assert.equal(parsed.command, "pwd");
  assert.equal(parsed.status, "done");
  assert.equal(parsed.exitCode, 0);
  assert.match(parsed.output, /home\/ricki/);
});

test("parseSubagentEventFromPayload supports function_call spawn_agent", () => {
  const parsed = parseSubagentEventFromPayload({
    type: "item.completed",
    item: {
      type: "function_call",
      name: "spawn_agent",
      arguments:
        '{"nickname":"Planck","agent_type":"golang-pro","model":"gpt-5.3-codex","reasoning_effort":"high","message":"Trace the code path"}',
      output: {
        agent_id: "agent-123",
      },
    },
  });

  assert.ok(parsed);
  assert.equal(parsed.toolName, "spawn_agent");
  assert.equal(parsed.nickname, "Planck");
  assert.equal(parsed.agentType, "golang-pro");
  assert.equal(parsed.model, "gpt-5.3-codex");
  assert.equal(parsed.reasoning, "high");
});

test("parseSubagentEventFromPayload supports flattened raw envelope spawn_agent", () => {
  const payload = parseRawEventPayload(
    JSON.stringify({
      method: "item/completed",
      params: {
        item: {
          type: "function_call",
          name: "spawn_agent",
          arguments:
            '{"nickname":"Planck","agent_type":"golang-pro","message":"Trace the code path"}',
          output: { agent_id: "agent-123" },
        },
      },
    }),
  );

  const parsed = parseSubagentEventFromPayload(payload);
  assert.ok(parsed);
  assert.equal(parsed.toolName, "spawn_agent");
  assert.equal(parsed.nickname, "Planck");
  assert.equal(parsed.agentType, "golang-pro");
});

test("parseSubagentEventFromPayload marks completed wait_agent as done", () => {
  const parsed = parseSubagentEventFromPayload({
    type: "item.completed",
    item: {
      type: "tool_call",
      tool_name: "wait_agent",
      arguments: '{"id":"agent-123"}',
    },
  });

  assert.ok(parsed);
  assert.equal(parsed.toolName, "wait_agent");
  assert.equal(parsed.status, "done");
  assert.equal(parsed.title, "Subagent wait completed");
});

test("parseSubagentEventFromPayload maps subagent thread.started to a subagent lifecycle row", () => {
  const parsed = parseSubagentEventFromPayload({
    type: "thread.started",
    thread: {
      id: "agent-123",
      agentNickname: "Kepler",
      agentRole: "browser-debugger",
    },
  });

  assert.ok(parsed);
  assert.equal(parsed.toolName, "spawn_agent");
  assert.equal(parsed.status, "running");
  assert.equal(parsed.nickname, "Kepler");
  assert.equal(parsed.agentType, "browser-debugger");
});

test("parseSubagentActivityText supports spawned rich transcript header with model and reasoning", () => {
  const parsed = parseSubagentActivityText(
    "• Spawned Ptolemy [code-reviewer] (gpt-5.3-codex high)\n└ Re-review the current /chat architecture after recent fixes in the working tree.",
  );

  assert.ok(parsed);
  assert.equal(parsed.toolName, "spawn_agent");
  assert.equal(parsed.nickname, "Ptolemy");
  assert.equal(parsed.agentType, "code-reviewer");
  assert.equal(parsed.model, "gpt-5.3-codex");
  assert.equal(parsed.reasoning, "high");
  assert.match(parsed.summary, /Re-review the current \/chat architecture/i);
});

test("buildExecAwareMessages hides generic thread.started raw events", () => {
  const messages = buildExecAwareMessages([
    {
      id: "raw-thread",
      role: "event",
      content: JSON.stringify({
        method: "thread/started",
        params: {
          thread: {
            id: "thread-main",
            agentNickname: null,
            agentRole: null,
          },
        },
      }),
      created_at: "2026-03-28T00:00:00Z",
    },
  ]);

  assert.equal(messages.length, 0);
});

test("parseSubagentActivityText supports timeline waiting activity", () => {
  const parsed = parseSubagentActivityText(
    "Timeline event: `WAITING` (awaiting analysis result from subagent `Kepler`).",
  );

  assert.ok(parsed);
  assert.equal(parsed.toolName, "wait_agent");
  assert.equal(parsed.status, "running");
});

test("parseSubagentActivityText supports completion activity text", () => {
  const parsed = parseSubagentActivityText(
    "The subagent finished; I'm validating the cited regions directly before returning the final summary.",
  );

  assert.ok(parsed);
  assert.equal(parsed.toolName, "wait_agent");
  assert.equal(parsed.status, "done");
  assert.equal(parsed.title, "Subagent wait completed");
});

test("buildExecAwareMessages keeps completed subagent wait rows visible", () => {
  const messages = buildExecAwareMessages([
    {
      id: "sub-1",
      role: "subagent",
      content: "Subagent wait completed",
      subagent_title: "Subagent wait completed",
      subagent_tool: "wait_agent",
      subagent_status: "done",
      created_at: "2026-03-28T00:00:00Z",
      updated_at: "2026-03-28T00:00:00Z",
    },
  ]);

  assert.equal(messages.length, 1);
  assert.equal(messages[0].role, "subagent");
  assert.equal(messages[0].subagent_status, "done");
});

test("buildExecAwareMessages drops removed legacy_owner ownership on synthesized exec, subagent, MCP, and file-op rows", () => {
  const messages = buildExecAwareMessages([
    {
      id: "raw-exec",
      role: "event",
      actor: "legacy_owner",
      content: JSON.stringify({
        method: "item/completed",
        params: {
          item: {
            type: "commandExecution",
            command: "pwd",
            aggregatedOutput: "/tmp",
            exitCode: 0,
          },
        },
      }),
      created_at: "2026-03-30T00:00:00Z",
    },
    {
      id: "raw-subagent",
      role: "event",
      actor: "legacy_owner",
      content: JSON.stringify({
        type: "item.started",
        item: {
          type: "collab_tool_call",
          receiver_thread_ids: ["agent-thread-1"],
          function: { name: "wait" },
        },
      }),
      created_at: "2026-03-30T00:00:01Z",
    },
    {
      id: "raw-mcp",
      role: "event",
      actor: "legacy_owner",
      content: JSON.stringify({
        type: "item.completed",
        item: {
          type: "tool_call",
          name: "mcp__github__search_code",
          output: { summary: "Found 12 matches" },
        },
      }),
      created_at: "2026-03-30T00:00:02Z",
    },
    {
      id: "raw-file",
      role: "event",
      actor: "legacy_owner",
      content: JSON.stringify({
        method: "item/completed",
        params: {
          item: {
            type: "fileRead",
            path: "/tmp/readme.md",
          },
        },
      }),
      created_at: "2026-03-30T00:00:03Z",
    },
  ]);

  const execRow = messages.find((row) => row?.role === "exec");
  const subagentRow = messages.find((row) => row?.role === "subagent");
  const mcpRow = messages.find((row) => row?.mcp_activity);
  const fileRow = messages.find((row) => row?.file_op);

  assert.equal(execRow?.actor || "", "");
  assert.equal(execRow?.lane || "", "");
  assert.equal(subagentRow?.actor || "", "");
  assert.equal(subagentRow?.lane || "", "");
  assert.equal(mcpRow?.actor || "", "");
  assert.equal(mcpRow?.lane || "", "");
  assert.equal(fileRow?.actor || "", "");
  assert.equal(fileRow?.lane || "", "");
});

test("buildExecAwareMessages collapses persisted exec plus persisted command activity after removed legacy_owner ownership is stripped", () => {
  const messages = buildExecAwareMessages([
    {
      id: "exec-persisted-1",
      role: "exec",
      actor: "legacy_owner",
      lane: "legacy_owner",
      exec_command:
        "sed -n '180,290p' internal/store/store_schema_migration_test.go",
      exec_status: "done",
      exec_exit_code: 0,
      exec_output: "ok",
      created_at: "2026-03-31T00:16:48Z",
      updated_at: "2026-03-31T00:16:48Z",
    },
    {
      id: "msg-activity-1",
      role: "activity",
      actor: "legacy_owner",
      lane: "legacy_owner",
      content:
        "Command done: sed -n '180,290p' internal/store/store_schema_migration_test.go",
      created_at: "2026-03-31T00:16:48Z",
      updated_at: "2026-03-31T00:16:48Z",
    },
  ]);

  const execRows = messages.filter((row) => row?.role === "exec");
  assert.equal(execRows.length, 1);
  assert.equal(execRows[0]?.actor || "", "");
  assert.equal(execRows[0]?.lane || "", "");
  assert.equal(execRows[0]?.exec_status, "done");
  assert.equal(
    execRows[0]?.exec_command,
    "sed -n '180,290p' internal/store/store_schema_migration_test.go",
  );
});

test("buildExecAwareMessages classifies mixed raw timeline events without leaving raw event rows visible", () => {
  const messages = buildExecAwareMessages([
    {
      id: "assistant-1",
      role: "assistant",
      content: "Checking the latest /chat activity.",
      created_at: "2026-04-01T10:00:00Z",
      updated_at: "2026-04-01T10:00:00Z",
    },
    {
      id: "raw-exec-mixed",
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
      created_at: "2026-04-01T10:00:01Z",
    },
    {
      id: "raw-subagent-mixed",
      role: "event",
      content: JSON.stringify({
        type: "thread.started",
        thread: {
          id: "agent-kepler",
          agentNickname: "Kepler",
          agentRole: "browser-debugger",
        },
      }),
      created_at: "2026-04-01T10:00:02Z",
    },
    {
      id: "raw-mcp-mixed",
      role: "event",
      content: JSON.stringify({
        type: "item.completed",
        item: {
          type: "tool_call",
          name: "mcp__github__search_code",
          output: { summary: "Found 4 matches" },
        },
      }),
      created_at: "2026-04-01T10:00:03Z",
    },
    {
      id: "raw-file-mixed",
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
      created_at: "2026-04-01T10:00:04Z",
    },
  ]);

  assert.deepEqual(
    messages.map((row) => row.id),
    [
      "assistant-1",
      "exec-raw-exec-mixed",
      "subagent-raw-subagent-mixed",
      "mcp-raw-mcp-mixed",
      "fileop-raw-file-mixed",
    ],
  );
  assert.equal(messages.some((row) => row?.role === "event"), false);
  assert.equal(messages[1]?.role, "exec");
  assert.equal(messages[2]?.role, "subagent");
  assert.equal(messages[3]?.mcp_activity, true);
  assert.equal(messages[4]?.file_op, "Read: /tmp/readme.md");
});

test("parseFileOperationPayload supports file read payloads", () => {
  const parsed = parseFileOperationPayload({
    type: "item/completed",
    item: {
      type: "fileRead",
      path: "/tmp/readme.md",
    },
  });

  assert.equal(parsed, "Read: /tmp/readme.md");
});

test("parseFileOperationPayload supports created file change payloads", () => {
  const parsed = parseFileOperationPayload({
    type: "item/completed",
    item: {
      type: "fileChange",
      changes: [{ kind: "create", path: "/tmp/new.txt" }],
    },
  });

  assert.equal(parsed, "Created: /tmp/new.txt");
});

test("parseFileOperationPayload supports edited file change payloads", () => {
  const parsed = parseFileOperationPayload({
    type: "item/completed",
    item: {
      type: "fileChange",
      changes: [
        {
          kind: "edit",
          path: "/tmp/app.go",
          added_lines: 10,
          deleted_lines: 2,
        },
      ],
    },
  });

  assert.equal(parsed, "Edited: /tmp/app.go (+10 -2)");
});

test("parseFileOperationPayload supports deleted file change payloads", () => {
  const parsed = parseFileOperationPayload({
    type: "item/completed",
    item: {
      type: "fileChange",
      changes: [{ kind: "delete", path: "/tmp/old.txt" }],
    },
  });

  assert.equal(parsed, "Deleted: /tmp/old.txt");
});

test("parseFileOperationPayload supports nested kind objects from file change payloads", () => {
  const parsed = parseFileOperationPayload({
    type: "item/completed",
    item: {
      type: "fileChange",
      changes: [{ kind: { type: "delete" }, path: "/tmp/nested-old.txt" }],
    },
  });

  assert.equal(parsed, "Deleted: /tmp/nested-old.txt");
});
