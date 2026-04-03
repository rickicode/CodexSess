import test from "node:test";
import assert from "node:assert/strict";

import { findAssistantStreamTargetIndex } from "./assistantStreamState.js";

test("assistant stream with identity does not reuse generic pending placeholder", () => {
  const messages = [
    {
      id: "assistant-placeholder",
      role: "assistant",
      actor: "",
      content: "",
      pending: true,
    },
  ];

  const index = findAssistantStreamTargetIndex(messages, {
    actor: "",
    assistantKey: "assistant|turn-chat-1|item-assistant-2|agentmessage",
  });

  assert.equal(index, -1);
});

test("assistant stream with identity reuses matching stream identity row", () => {
  const messages = [
    {
      id: "assistant-1",
      role: "assistant",
      actor: "",
      content: "First bubble",
      pending: true,
      stream_identity_key: "assistant|turn-chat-1|item-assistant-1|agentmessage",
    },
    {
      id: "assistant-2",
      role: "assistant",
      actor: "",
      content: "Second bubble",
      pending: true,
      stream_identity_key: "assistant|turn-chat-1|item-assistant-2|agentmessage",
    },
  ];

  const index = findAssistantStreamTargetIndex(messages, {
    actor: "",
    assistantKey: "assistant|turn-chat-1|item-assistant-2|agentmessage",
  });

  assert.equal(index, 1);
});

test("assistant stream without identity still reuses matching pending actor row", () => {
  const messages = [
    {
      id: "assistant-chat",
      role: "assistant",
      actor: "chat",
      content: "Streaming...",
      pending: true,
    },
    {
      id: "assistant-executor",
      role: "assistant",
      actor: "executor",
      content: "Working...",
      pending: true,
    },
  ];

  const index = findAssistantStreamTargetIndex(messages, {
    actor: "executor",
    assistantKey: "",
  });

  assert.equal(index, 1);
});
