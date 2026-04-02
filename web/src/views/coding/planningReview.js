function parsePlanningReviewConfidence(rawPlan = "", summary = "") {
  const text = `${String(rawPlan || "").trim()}\n${String(summary || "").trim()}`;
  const matches = [...text.matchAll(/\bConfidence:\s*(\d{1,3})%/gi)];
  if (matches.length === 0) return null;
  const raw = Number.parseInt(String(matches[matches.length - 1][1] || ""), 10);
  if (!Number.isFinite(raw)) return null;
  return Math.min(100, Math.max(0, raw));
}

function parsePlanningReviewPlan(rawPlan) {
  const raw = String(rawPlan || "").trim();
  if (!raw) {
    return { summary: "", tasks: [], stopConditions: [], confidence: null };
  }

  const lines = raw.split("\n").map((line) => String(line || "").trim());
  const paragraphs = raw
    .split(/\n\s*\n/)
    .map((chunk) => String(chunk || "").trim())
    .filter(Boolean);
  const firstContent = paragraphs[0] || lines.find((line) => line) || "";
  const tasks = [];
  const stopConditions = [];
  let summary = "";
  let currentSection = "";
  let sawSummaryHeader = false;
  let sawTasksHeader = false;

  for (const line of lines) {
    if (!line) continue;

    const upper = line
      .replace(/^#+\s*/, "")
      .replace(/:$/, "")
      .trim()
      .toUpperCase();
    if (
      upper === "TASKS" ||
      upper === "EXECUTION ORDER" ||
      upper === "PLAN" ||
      upper === "STEPS"
    ) {
      currentSection = "tasks";
      sawTasksHeader = true;
      continue;
    }
    if (
      upper === "STOP CONDITIONS" ||
      upper === "STOP_CONDITIONS" ||
      upper === "REPLAN TRIGGERS" ||
      upper === "REPLAN_TRIGGERS" ||
      upper === "STOP" ||
      upper === "ESCALATION"
    ) {
      currentSection = "stop";
      continue;
    }
    if (/^(SUMMARY|OVERVIEW|GOAL|GOALS)\b/i.test(line)) {
      currentSection = "summary";
      sawSummaryHeader = true;
      continue;
    }

    const isChecklistTask = /^- \[[ xX]\]\s+/.test(line);
    const isGoalLine = /^\*{0,2}\s*goal\s*:\s*\*{0,2}\s+/i.test(line);
    const normalized = line
      .replace(/^- \[[ xX]\]\s+/, "")
      .replace(/^\*{0,2}\s*goal\s*:\s*\*{0,2}\s+/i, "")
      .replace(/^[-*]\s+/, "")
      .replace(/^\d+[.)]\s+/, "")
      .trim();
    if (!normalized) continue;
    if (/^confidence:\s*\d{1,3}%/i.test(normalized)) continue;

    if (currentSection === "summary") {
      summary = summary ? `${summary} ${normalized}` : normalized;
      continue;
    }
    if (isGoalLine) {
      summary = summary ? `${summary} ${normalized}` : normalized;
      continue;
    }
    if (currentSection === "tasks" && isChecklistTask) {
      tasks.push(normalized);
      continue;
    }
    if (
      currentSection === "stop" ||
      /stop|replan|sign[\s-]?off|confirm|review|security|destructive|irreversible/i.test(normalized)
    ) {
      stopConditions.push(normalized);
    }
  }

  const normalizedSummary = String(summary || "").trim();
  const fallbackChecklistTasks = lines
    .filter((line) => /^- \[[ xX]\]\s+/.test(String(line || "").trim()))
    .map((line) =>
      String(line || "")
        .trim()
        .replace(/^- \[[ xX]\]\s+/, "")
        .trim(),
    )
    .filter(Boolean);
  return {
    summary:
      normalizedSummary ||
      (sawSummaryHeader || sawTasksHeader ? normalizedSummary : firstContent || raw),
    tasks:
      sawTasksHeader
        ? [...new Set(tasks)].slice(0, 24)
        : [...new Set(fallbackChecklistTasks)].slice(0, 24),
    stopConditions: [...new Set(stopConditions)].slice(0, 5),
    confidence: parsePlanningReviewConfidence(raw, normalizedSummary),
  };
}

function isExecutionReadyPlan({ rawPlan = "", summary = "", tasks = [] } = {}) {
  void rawPlan;
  void summary;
  const normalizedTasks = Array.isArray(tasks)
    ? tasks.map((item) => String(item || "").trim()).filter(Boolean)
    : [];
  return normalizedTasks.length > 0;
}

export {
  isExecutionReadyPlan,
  parsePlanningReviewConfidence,
  parsePlanningReviewPlan,
};
