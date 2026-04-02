import test from "node:test";
import assert from "node:assert/strict";

import {
  isExecutionReadyPlan,
  parsePlanningReviewPlan,
} from "./planningReview.js";

test("execution readiness stays hidden for question-style planning replies", () => {
  const rawPlan = `What should happen if schema ownership is unclear?\nPlease confirm before I finalize the plan.`;
  const sections = parsePlanningReviewPlan(rawPlan);
  assert.equal(
    isExecutionReadyPlan({
      rawPlan,
      summary: sections.summary,
      tasks: sections.tasks,
    }),
    false,
  );
});

test("execution readiness accepts superpowers implementation plan shape", () => {
  const rawPlan = `# Auth Refactor Implementation Plan

**Goal:** Ship the approved refactor safely. Confidence: 93%.

### Task 1: Backend
- [ ] Update the backend handler.
- [ ] Update the planning bubble UI.

## Stop Conditions
- Ask before destructive schema changes.`;
  const sections = parsePlanningReviewPlan(rawPlan);
  assert.equal(
    isExecutionReadyPlan({
      rawPlan,
      summary: sections.summary,
      tasks: sections.tasks,
    }),
    true,
  );
  assert.deepEqual(sections.stopConditions, [
    "Ask before destructive schema changes.",
  ]);
});

test("execution readiness accepts tasks-only plans when actionable tasks exist", () => {
  const rawPlan = `# Fast Fix Plan

TASKS
  - [ ] Update the backend handler.
  - [ ] Update the planning bubble UI.
`;
  const sections = parsePlanningReviewPlan(rawPlan);
  assert.equal(
    isExecutionReadyPlan({
      rawPlan,
      summary: sections.summary,
      tasks: sections.tasks,
    }),
    true,
  );
});

test("execution readiness does not depend on confidence when structure is valid", () => {
  const rawPlan = `# Auth Refactor Implementation Plan

**Goal:** Ship the approved refactor safely. Confidence: 85%.

### Task 1: Backend
- [ ] Update the backend handler.
- [ ] Update the planning bubble UI.
`;
  const sections = parsePlanningReviewPlan(rawPlan);
  assert.equal(sections.confidence, 85);
  assert.equal(
    isExecutionReadyPlan({
      rawPlan,
      summary: sections.summary,
      tasks: sections.tasks,
    }),
    true,
  );
});
