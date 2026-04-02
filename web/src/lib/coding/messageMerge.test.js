import test from "node:test";
import assert from "node:assert/strict";

import {
  eventSequenceFromMessage,
  reconcileLiveMessagesWithPersisted,
  sequenceFromMessage,
  sortMessagesChronologically,
  timestampFromMessage,
} from "./messageMerge.js";
import { collectLiveMessageIDs } from "../../views/coding/messageView.js";

test("timestampFromMessage falls back to updated_at when created_at missing", () => {
  const ts = timestampFromMessage({
    updated_at: "2026-03-29T05:00:01.000Z",
  });
  assert.equal(Number.isFinite(ts) && ts > 0, true);
});

test("eventSequenceFromMessage extracts positive event_seq", () => {
  assert.equal(eventSequenceFromMessage({ event_seq: 12 }), 12);
  assert.equal(eventSequenceFromMessage({ event_seq: "7" }), 7);
  assert.equal(eventSequenceFromMessage({ eventSeq: "9" }), 9);
  assert.equal(eventSequenceFromMessage({ event_seq: 0 }), 0);
});

test("sequenceFromMessage supports compact aliases and id suffix fallback", () => {
  assert.equal(sequenceFromMessage({ sequence: "5" }), 5);
  assert.equal(sequenceFromMessage({ seq: 6 }), 6);
  assert.equal(sequenceFromMessage({ view_seq: 7 }), 7);
  assert.equal(sequenceFromMessage({ id: "cmsg-000123" }), 123);
  assert.equal(sequenceFromMessage({ id: "plain-id" }), 0);
});

test("sortMessagesChronologically keeps deterministic ordering within sequence/event/time groups", () => {
  const sorted = sortMessagesChronologically([
    { id: "t2", created_at: "2026-03-29T05:00:02.000Z" },
    { id: "ev2", event_seq: 2, created_at: "2026-03-29T05:00:01.000Z" },
    { id: "seq2", sequence: 2, created_at: "2026-03-29T05:00:03.000Z" },
    { id: "seq1", sequence: 1, created_at: "2026-03-29T05:00:04.000Z" },
    { id: "ev1", event_seq: 1, created_at: "2026-03-29T05:00:01.000Z" },
    { id: "t1", created_at: "2026-03-29T05:00:01.000Z" },
  ]);
  const ids = sorted.map((item) => item.id);
  assert.equal(ids.indexOf("seq1") < ids.indexOf("seq2"), true);
  assert.equal(ids.indexOf("ev1") < ids.indexOf("ev2"), true);
  assert.equal(ids.indexOf("t1") < ids.indexOf("t2"), true);
});

test("sortMessagesChronologically keeps sequence order stable across mixed rows", () => {
  const sorted = sortMessagesChronologically([
    { id: "m3", seq: 3, created_at: "" },
    { id: "m1", seq: 1, created_at: "2026-03-29T05:00:04.000Z" },
    { id: "m2", created_at: "2026-03-29T05:00:01.000Z" },
  ]);
  const ids = sorted.map((item) => item.id);
  assert.equal(ids.indexOf("m1") < ids.indexOf("m3"), true);
});

test("reconcileLiveMessagesWithPersisted replaces running exec bubble with persisted done bubble", () => {
  const current = [
    {
      id: "exec-live-1",
      role: "exec",
      content: "npm test",
      exec_command: "npm test",
      exec_status: "running",
      created_at: "2026-03-29T05:00:01.000Z",
      updated_at: "2026-03-29T05:00:01.000Z",
    },
  ];
  const persisted = [
    {
      id: "exec-db-1",
      role: "exec",
      content: "npm test",
      exec_command: "npm test",
      exec_status: "done",
      exec_exit_code: 0,
      created_at: "2026-03-29T05:00:02.000Z",
      updated_at: "2026-03-29T05:00:02.000Z",
    },
  ];

  const merged = reconcileLiveMessagesWithPersisted(
    current,
    persisted,
    collectLiveMessageIDs(current),
  );

  assert.equal(merged.length, 1);
  assert.equal(merged[0].id, "exec-db-1");
  assert.equal(merged[0].exec_status, "done");
});

test("reconcileLiveMessagesWithPersisted replaces terminal exec bubble with persisted row when command and owner match", () => {
  const current = [
    {
      id: "exec-live-done-1",
      role: "exec",
      actor: "legacy_owner",
      lane: "legacy_owner",
      content: "git status --porcelain=v1",
      exec_command: "git status --porcelain=v1",
      exec_status: "done",
      exec_exit_code: 0,
      created_at: "2026-03-30T06:29:23.000Z",
      updated_at: "2026-03-30T06:29:23.000Z",
    },
  ];
  const persisted = [
    {
      id: "exec-db-done-1",
      role: "exec",
      actor: "legacy_owner",
      lane: "legacy_owner",
      content: "git status --porcelain=v1",
      exec_command: "git status --porcelain=v1",
      exec_status: "done",
      exec_exit_code: 0,
      created_at: "2026-03-30T06:29:24.000Z",
      updated_at: "2026-03-30T06:29:24.000Z",
    },
  ];

  const merged = reconcileLiveMessagesWithPersisted(
    current,
    persisted,
    collectLiveMessageIDs(current),
  );

  assert.equal(merged.length, 1);
  assert.equal(merged[0].id, "exec-db-done-1");
  assert.equal(merged[0].exec_status, "done");
});

test("reconcileLiveMessagesWithPersisted replaces the matching live subagent bubble instead of duplicating a blank-content row", () => {
  const current = [
    {
      id: "sub-live-1",
      role: "subagent",
      subagent_status: "running",
      subagent_tool: "wait_agent",
      subagent_name: "Kepler",
      subagent_title: "Waiting for Kepler",
      subagent_summary: "Waiting for Kepler",
      created_at: "2026-03-30T01:00:01Z",
      updated_at: "2026-03-30T01:00:01Z",
    },
    {
      id: "sub-live-2",
      role: "subagent",
      subagent_status: "running",
      subagent_tool: "wait_agent",
      subagent_name: "Planck",
      subagent_title: "Waiting for Planck",
      subagent_summary: "Waiting for Planck",
      created_at: "2026-03-30T01:00:02Z",
      updated_at: "2026-03-30T01:00:02Z",
    },
  ];
  const persisted = [
    {
      id: "sub-db-2",
      role: "subagent",
      subagent_status: "done",
      subagent_tool: "wait_agent",
      subagent_name: "Planck",
      subagent_title: "Waiting for Planck",
      subagent_summary: "Mapped the code path",
      created_at: "2026-03-30T01:00:03Z",
      updated_at: "2026-03-30T01:00:03Z",
    },
  ];

  const merged = reconcileLiveMessagesWithPersisted(
    current,
    persisted,
    collectLiveMessageIDs(current),
  );

  assert.deepEqual(
    merged.map((item) => item.id),
    ["sub-live-1", "sub-db-2"],
  );
  assert.equal(merged[1].subagent_status, "done");
  assert.equal(merged[1].subagent_summary, "Mapped the code path");
});
