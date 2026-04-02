---
type: reference
title: Chat Bubble Parity
created: 2026-04-01
tags:
  - chat
  - frontend
  - ui
  - parity
related:
  - '[[Chat-Only-Contract]]'
  - '[[Chat-Legacy-Cleanup-Matrix]]'
---

# Chat Bubble Parity

## Purpose

Phase 03 should clean up `/chat` by reusing the existing single-timeline chat rendering path instead of inventing a new presentation model. This note freezes the frontend parity target before the next UI and test changes land.

## Evidence Sources

The parity target below is grounded in the current frontend implementation:

- `web/src/views/CodingView.svelte`
- `web/src/views/coding/CodingMessagesPane.svelte`
- `web/src/views/coding/liveMessagePipeline.js`
- `web/src/views/coding/messageView.js`
- `web/src/lib/coding/activityParsing.js`
- `web/src/lib/coding/messageMerge.js`
- `web/src/views/coding/chatViewport.js`
- `web/src/views/coding/CodingTopbar.svelte`
- `web/src/views/coding/CodingComposer.svelte`
- `web/src/views/coding/CodingSkillModal.svelte`
- `web/src/views/coding/CodingSessionDrawer.svelte`
- `web/src/views/coding/CodingExecOutputModal.svelte`
- `web/src/views/coding/CodingSubagentDetailModal.svelte`

## Existing Frontend Path To Reuse

`CodingView.svelte` already renders one visible `CodingMessagesPane` in the main `/chat` surface. That pane is fed by the current parsing and projection pipeline:

1. `activityParsing.js` normalizes exec, MCP, file-op, recovery, and tool payload details.
2. `liveMessagePipeline.js` converts live websocket/raw-event activity into structured exec, MCP, and subagent rows.
3. `messageMerge.js` keeps persisted and live rows in chronological order while tolerating legacy actor metadata.
4. `messageView.js` projects the canonical timeline, deduplicates adjacent runtime/file rows, and preserves specialized bubble data for rendering.
5. `CodingMessagesPane.svelte` renders the specialized bubble shapes and hooks the detail affordances back into `CodingView.svelte`.

Phase 03 should keep this pipeline and simplify it. It should not replace it with a second message dialect or a fresh component tree.

## Required Visible Bubble Categories

The cleaned `/chat` timeline must keep these visibly distinct row types:

- Assistant text bubbles, including collapsed/expanded content and assistant usage footer text when present.
- Exec bubbles with command summary, running/done/failed state, and modal drill-in.
- MCP activity bubbles, including tool activity and generic MCP status rows.
- Subagent lifecycle bubbles for spawn, wait, send-input, resume, close, and completion summaries.
- File-operation bubbles for edited, created, deleted, moved, renamed, and read activity.
- Inline runner-status activity rows for streaming, recovery, restart, stop, and other internal runtime state that remains intentionally visible in the timeline.
- Load-more history state at the top of the same timeline, including `Load earlier messages`, loading copy, and preserved chronology after older rows are appended.

## Required Interactive Surface

The single-timeline cleanup must leave these controls interactive:

- Workspace picker / workdir selection flow, including the displayed active workdir in the top bar and the new-session path workflow.
- Model selector.
- Reasoning selector.
- Sandbox mode toggle in the composer.
- Skill picker modal and `$skill` token insertion.
- Session drawer with session selection and deletion.
- Jump-to-latest button for the message viewport.
- Show more / show less expansion for long message bodies.
- Stop and force-stop behavior using the current send/cancel control path.
- Exec detail modal.
- Subagent detail modal.

## Parity Rules

- `/chat` should read as one chronological Codex-style conversation, not as a dashboard with lane framing.
- Legacy `supervisor` and `executor` actor or lane markers may still appear in stored data, but they are compatibility inputs only.
- Existing summary builders, modal openers, and row-deduplication helpers should be reused before any new abstraction is added.
- File-operation rows must remain explicit and readable; they must not degrade into raw log dumps.
- Load-more history, websocket reconnect behavior, and live streaming state should remain part of the same viewport contract.

## Non-Goals For Phase 03 Parity

- Reintroducing dual-lane UI, supervisor/executor panes, or legacy multi-runner copy.
- Replacing the current bubble system with decorative cards or a new dashboard shell.
- Splitting message rendering into separate visible timelines for different runtime actors.

## Immediate Implication For The Next Checkbox

The failing frontend tests for Phase 03 should assert parity against the existing single-timeline path above:

- correct chronological ordering
- specialized bubble classification
- deduplication behavior
- file-op / MCP / subagent visibility
- absence of visible legacy multi-runner lane splits
