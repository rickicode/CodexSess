package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
)

func TestHandleWebCodingMessages_CompactFallbackBuildsCanonicalFromRawHistory_Endpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-fallback.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	sessionID := "sess-compact-fallback"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Compact Fallback",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	command := `/bin/bash -lc "rtk git status --short"`
	now := time.Date(2026, 3, 24, 2, 3, 4, 0, time.UTC)
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-activity",
		SessionID: sessionID,
		Role:      "activity",
		Actor:     "executor",
		Content:   "Running: " + command,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("append activity message: %v", err)
	}
	rawCompleted, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":              "command_execution",
			"command":           command,
			"exit_code":         0,
			"aggregated_output": "ok",
		},
	})
	if err != nil {
		t.Fatalf("marshal event payload: %v", err)
	}
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-event",
		SessionID: sessionID,
		Role:      "event",
		Actor:     "executor",
		Content:   string(rawCompleted),
		CreatedAt: now.Add(10 * time.Millisecond),
	}); err != nil {
		t.Fatalf("append event message: %v", err)
	}

	emptyCanonical, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list canonical before request: %v", err)
	}
	if len(emptyCanonical) != 0 {
		t.Fatalf("expected no canonical rows before fallback build, got %d", len(emptyCanonical))
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/messages?session_id="+sessionID+"&view=compact&limit=50", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := stringFromAny(body["source"]); got != "canonical" {
		t.Fatalf("expected source canonical, got %q", got)
	}
	items, _ := body["messages"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected compact messages from raw fallback build")
	}
	first, _ := items[0].(map[string]any)
	if got := stringFromAny(first["role"]); got != "exec" {
		t.Fatalf("expected compact exec row, got %q", got)
	}
	if got := stringFromAny(first["exec_status"]); got != "done" {
		t.Fatalf("expected exec_status done, got %q", got)
	}
	if got := stringFromAny(first["exec_output"]); got != "[redacted]" {
		t.Fatalf("expected merged exec output redacted, got %q", got)
	}
	if redacted, _ := first["redacted"].(bool); !redacted {
		t.Fatalf("expected redacted=true on compact exec row")
	}

	canonicalRows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list canonical after request: %v", err)
	}
	if len(canonicalRows) == 0 {
		t.Fatalf("expected canonical rows persisted after fallback build")
	}
	if got := stringFromAny(canonicalRows[0]["role"]); got != "exec" {
		t.Fatalf("expected persisted compact role exec, got %q", got)
	}
	if got := stringFromAny(canonicalRows[0]["exec_output"]); got != "[redacted]" {
		t.Fatalf("expected persisted compact exec_output redacted, got %q", got)
	}
	snapshotPayload, ok, err := st.GetCodingMessageSnapshot(ctx, sessionID, "compact")
	if err != nil {
		t.Fatalf("get persisted snapshot: %v", err)
	}
	if !ok {
		t.Fatalf("expected compact snapshot persisted")
	}
	if strings.Contains(snapshotPayload, `"ok"`) {
		t.Fatalf("expected persisted snapshot to avoid raw exec output, got %s", snapshotPayload)
	}
	if !strings.Contains(snapshotPayload, `"[redacted]"`) {
		t.Fatalf("expected persisted snapshot to contain redacted marker, got %s", snapshotPayload)
	}
}

func TestPersistCompactCodingView_SanitizesAndPersistsBothStores(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-persist-helper.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-persist-helper"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Persist Helper",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	rows := []map[string]any{
		{
			"id":           "exec-helper-1",
			"role":         "exec",
			"exec_command": "rtk go test ./...",
			"exec_status":  "done",
			"exec_output":  "SECRET-HELPER-123",
			"subagent_raw": map[string]any{"secret": "SECRET-SUBAGENT-123"},
		},
	}

	if err := s.persistCompactCodingView(ctx, sessionID, "compact", rows); err != nil {
		t.Fatalf("persistCompactCodingView: %v", err)
	}

	canonicalRows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list canonical rows: %v", err)
	}
	if len(canonicalRows) != 1 {
		t.Fatalf("expected 1 canonical row, got %d", len(canonicalRows))
	}
	if got := stringFromAny(canonicalRows[0]["exec_output"]); got != "[redacted]" {
		t.Fatalf("expected sanitized exec output, got %q", got)
	}
	if _, exists := canonicalRows[0]["subagent_raw"]; exists {
		t.Fatalf("expected sanitized row without subagent_raw: %#v", canonicalRows[0])
	}

	snapshotPayload, ok, err := st.GetCodingMessageSnapshot(ctx, sessionID, "compact")
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if !ok {
		t.Fatalf("expected snapshot persisted")
	}
	if strings.Contains(snapshotPayload, "SECRET-HELPER-123") || strings.Contains(snapshotPayload, "SECRET-SUBAGENT-123") {
		t.Fatalf("expected sanitized snapshot payload, got %s", snapshotPayload)
	}
}

func TestRebuildCompactCodingViewFromRawHistory_PersistsCanonicalRows(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-rebuild-helper.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-rebuild-helper"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Rebuild Helper",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	now := time.Date(2026, 4, 3, 10, 15, 0, 0, time.UTC)
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-user-helper",
		SessionID: sessionID,
		Role:      "user",
		Content:   "hello",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-assistant-helper",
		SessionID: sessionID,
		Role:      "assistant",
		Content:   "world",
		CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	rows, err := s.rebuildCompactCodingViewFromRawHistory(ctx, sessionID, "compact")
	if err != nil {
		t.Fatalf("rebuildCompactCodingViewFromRawHistory: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 compact rows, got %d", len(rows))
	}
	if got := stringFromAny(rows[0]["role"]); got != "user" {
		t.Fatalf("expected first row user, got %q", got)
	}
	if got := stringFromAny(rows[1]["role"]); got != "assistant" {
		t.Fatalf("expected second row assistant, got %q", got)
	}

	canonicalRows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list canonical rows: %v", err)
	}
	if len(canonicalRows) != 2 {
		t.Fatalf("expected canonical rows persisted, got %d", len(canonicalRows))
	}
}

func TestHandleWebCodingMessages_RedactsLegacyUnsanitizedExecOutput(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-legacy-redact.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-compact-legacy-redact"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Compact Legacy Redact",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	legacyRows := []map[string]any{
		{
			"id":           "exec-legacy-1",
			"role":         "exec",
			"content":      "rtk go test ./...",
			"exec_command": "rtk go test ./...",
			"exec_status":  "done",
			"exec_output":  "SECRET-TOKEN-LEGACY-123",
			"subagent_raw": map[string]any{"secret": "SECRET-SUBAGENT-LEGACY-123"},
			"created_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"updated_at":   time.Now().UTC().Format(time.RFC3339Nano),
		},
	}
	if err := st.ReplaceCodingViewMessages(ctx, sessionID, "compact", legacyRows); err != nil {
		t.Fatalf("seed legacy compact rows: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/messages?session_id="+sessionID+"&view=compact&limit=50", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := body["messages"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 compact row, got %d", len(items))
	}
	row, _ := items[0].(map[string]any)
	if got := stringFromAny(row["exec_output"]); got != "[redacted]" {
		t.Fatalf("expected response-layer redacted exec_output, got %q", got)
	}
	rawBody := rec.Body.String()
	if strings.Contains(rawBody, "SECRET-TOKEN-LEGACY-123") {
		t.Fatalf("expected no legacy secret in response body, got %s", rawBody)
	}
	canonicalRows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list rewritten canonical rows: %v", err)
	}
	if len(canonicalRows) != 1 {
		t.Fatalf("expected rewritten canonical row count=1, got %d", len(canonicalRows))
	}
	if got := stringFromAny(canonicalRows[0]["exec_output"]); got != "[redacted]" {
		t.Fatalf("expected rewritten canonical exec_output redacted, got %q", got)
	}
	if _, exists := canonicalRows[0]["subagent_raw"]; exists {
		t.Fatalf("expected rewritten canonical rows without subagent_raw field, got %#v", canonicalRows[0])
	}
	snapshotPayload, ok, err := st.GetCodingMessageSnapshot(ctx, sessionID, "compact")
	if err != nil {
		t.Fatalf("get rewritten compact snapshot: %v", err)
	}
	if !ok {
		t.Fatalf("expected rewritten compact snapshot persisted")
	}
	if strings.Contains(snapshotPayload, "SECRET-TOKEN-LEGACY-123") || strings.Contains(snapshotPayload, "SECRET-SUBAGENT-LEGACY-123") {
		t.Fatalf("expected rewritten snapshot without legacy raw secrets, got %s", snapshotPayload)
	}
	if strings.Contains(snapshotPayload, "subagent_raw") {
		t.Fatalf("expected rewritten snapshot without subagent_raw field, got %s", snapshotPayload)
	}
}

func TestHandleWebCodingMessages_RebuildsWhenCanonicalRowsAreStale(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-stale-rebuild.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-compact-stale"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Compact Stale",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	oldTime := time.Date(2026, 3, 26, 5, 18, 22, 0, time.UTC)
	newTime := oldTime.Add(36 * time.Minute)
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-old-stderr",
		SessionID: sessionID,
		Role:      "stderr",
		Content:   "Run failed: codex runtime rate limited or quota exhausted (429)",
		CreatedAt: oldTime,
	}); err != nil {
		t.Fatalf("append stale stderr: %v", err)
	}
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-new-assistant",
		SessionID: sessionID,
		Role:      "assistant",
		Content:   "autoswitched after multiple quota failures",
		CreatedAt: newTime,
	}); err != nil {
		t.Fatalf("append fresh assistant: %v", err)
	}

	staleRows := []map[string]any{
		{
			"id":         "stderr-000001",
			"role":       "stderr",
			"content":    "Run failed: codex runtime rate limited or quota exhausted (429)",
			"created_at": oldTime.Format(time.RFC3339Nano),
			"updated_at": oldTime.Format(time.RFC3339Nano),
		},
	}
	if err := st.ReplaceCodingViewMessages(ctx, sessionID, "compact", staleRows); err != nil {
		t.Fatalf("seed stale canonical rows: %v", err)
	}
	if encoded, err := json.Marshal(staleRows); err != nil {
		t.Fatalf("marshal stale snapshot: %v", err)
	} else if err := st.UpsertCodingMessageSnapshot(ctx, sessionID, "compact", string(encoded)); err != nil {
		t.Fatalf("seed stale snapshot: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/messages?session_id="+sessionID+"&view=compact&limit=50", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := stringFromAny(body["source"]); got != "canonical" {
		t.Fatalf("expected source canonical, got %q", got)
	}
	items, _ := body["messages"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected rebuilt compact timeline with 2 rows, got %d", len(items))
	}
	last, _ := items[len(items)-1].(map[string]any)
	if got := stringFromAny(last["role"]); got != "assistant" {
		t.Fatalf("expected rebuilt assistant row, got %q", got)
	}
	if got := stringFromAny(last["content"]); got != "autoswitched after multiple quota failures" {
		t.Fatalf("expected rebuilt assistant content, got %q", got)
	}

	canonicalRows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list rebuilt canonical rows: %v", err)
	}
	if len(canonicalRows) != 2 {
		t.Fatalf("expected rebuilt canonical rows count=2, got %d", len(canonicalRows))
	}
	if got := stringFromAny(canonicalRows[len(canonicalRows)-1]["content"]); got != "autoswitched after multiple quota failures" {
		t.Fatalf("expected rebuilt canonical tail content, got %q", got)
	}
}

func TestHandleWebCodingMessages_RebuildsWhenCanonicalRowsContainRawProtocolNoise(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-protocol-noise.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-compact-protocol-noise"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Compact Protocol Noise",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	now := time.Date(2026, 3, 27, 16, 17, 22, 0, time.UTC)
	rawStarted, err := json.Marshal(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":    "command_execution",
			"command": `/bin/bash -lc "pwd"`,
		},
	})
	if err != nil {
		t.Fatalf("marshal started event: %v", err)
	}
	rawCompleted, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":              "command_execution",
			"command":           `/bin/bash -lc "pwd"`,
			"exit_code":         0,
			"aggregated_output": "/home/ricki/workspaces/codexsess",
		},
	})
	if err != nil {
		t.Fatalf("marshal completed event: %v", err)
	}
	for idx, payload := range []string{string(rawStarted), string(rawCompleted)} {
		if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
			ID:        fmt.Sprintf("evt-%d", idx+1),
			SessionID: sessionID,
			Role:      "event",
			Actor:     "chat",
			Content:   payload,
			CreatedAt: now.Add(time.Duration(idx) * time.Second),
		}); err != nil {
			t.Fatalf("append event %d: %v", idx+1, err)
		}
	}
	staleRows := []map[string]any{
		{
			"id":         "activity-1",
			"role":       "activity",
			"actor":      "chat",
			"content":    "item/started: {\"item\":{\"command\":\"/bin/bash -lc \\\"pwd\\\"\"}}",
			"created_at": now.Format(time.RFC3339Nano),
			"updated_at": now.Format(time.RFC3339Nano),
		},
	}
	if err := st.ReplaceCodingViewMessages(ctx, sessionID, "compact", staleRows); err != nil {
		t.Fatalf("seed noisy canonical rows: %v", err)
	}
	if encoded, err := json.Marshal(staleRows); err != nil {
		t.Fatalf("marshal noisy snapshot: %v", err)
	} else if err := st.UpsertCodingMessageSnapshot(ctx, sessionID, "compact", string(encoded)); err != nil {
		t.Fatalf("seed noisy snapshot: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/messages?session_id="+sessionID+"&view=compact&limit=50", nil)
	rec := httptest.NewRecorder()
	s.handleWebCodingMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := body["messages"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected rebuilt compact exec row, got %d rows", len(items))
	}
	row, _ := items[0].(map[string]any)
	if got := stringFromAny(row["role"]); got != "exec" {
		t.Fatalf("expected rebuilt exec row, got %q", got)
	}
	if got := stringFromAny(row["exec_status"]); got != "done" {
		t.Fatalf("expected rebuilt exec status done, got %q", got)
	}
	if strings.Contains(strings.ToLower(stringFromAny(row["content"])), "item/started") {
		t.Fatalf("expected raw protocol noise to be removed, got %q", row["content"])
	}
}

func TestHandleWebCodingMessages_RebuildsFromRawHistoryWhenSnapshotIsEmpty(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-empty-snapshot.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-compact-empty-snapshot"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Compact Empty Snapshot",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	now := time.Now().UTC()
	rawHistory := []store.CodingMessage{
		{ID: "evt-1", SessionID: sessionID, Role: "event", Content: `{"type":"turn.failed","error":{"message":"usage limit"}}`, CreatedAt: now},
		{ID: "err-1", SessionID: sessionID, Role: "stderr", Content: "Run failed: usage limit", CreatedAt: now.Add(time.Millisecond)},
	}
	for _, msg := range rawHistory {
		if _, err := st.AppendCodingMessage(ctx, msg); err != nil {
			t.Fatalf("append coding message %s: %v", msg.ID, err)
		}
	}
	if err := st.ReplaceCodingViewMessages(ctx, sessionID, "compact", []map[string]any{}); err != nil {
		t.Fatalf("seed empty canonical rows: %v", err)
	}
	if err := st.UpsertCodingMessageSnapshot(ctx, sessionID, "compact", "[]"); err != nil {
		t.Fatalf("seed empty compact snapshot: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/messages?session_id="+sessionID+"&view=compact&limit=50", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := body["messages"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected compact rows rebuilt from raw history, got 0")
	}
	row, _ := items[len(items)-1].(map[string]any)
	if got := stringFromAny(row["role"]); got != "stderr" {
		t.Fatalf("expected rebuilt compact row to include stderr, got %q", got)
	}

	canonicalRows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list rebuilt canonical rows: %v", err)
	}
	if len(canonicalRows) == 0 {
		t.Fatalf("expected rebuilt canonical rows persisted")
	}
}

func TestHandleWebCodingMessages_RePersistsSanitizedSnapshotRows(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-compact-snapshot-repersist.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-compact-snapshot-repersist"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Compact Snapshot Repersist",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	legacySnapshot := `[{"id":"exec-snap-1","role":"exec","exec_command":"rtk go test ./...","exec_status":"done","exec_output":"SECRET-SNAPSHOT-EXEC-999","subagent_raw":{"secret":"SECRET-SUBAGENT-RAW-999"}}]`
	if err := st.UpsertCodingMessageSnapshot(ctx, sessionID, "compact", legacySnapshot); err != nil {
		t.Fatalf("seed legacy snapshot: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/messages?session_id="+sessionID+"&view=compact&limit=50", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	canonicalRows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list canonical rows: %v", err)
	}
	if len(canonicalRows) != 1 {
		t.Fatalf("expected 1 canonical row from snapshot repersist, got %d", len(canonicalRows))
	}
	if got := stringFromAny(canonicalRows[0]["exec_output"]); got != "[redacted]" {
		t.Fatalf("expected canonical repersisted exec_output redacted, got %q", got)
	}
	if _, exists := canonicalRows[0]["subagent_raw"]; exists {
		t.Fatalf("expected canonical repersisted row without subagent_raw, got %#v", canonicalRows[0])
	}

	snapshotPayload, ok, err := st.GetCodingMessageSnapshot(ctx, sessionID, "compact")
	if err != nil {
		t.Fatalf("get compact snapshot: %v", err)
	}
	if !ok {
		t.Fatalf("expected sanitized compact snapshot to be persisted")
	}
	if strings.Contains(snapshotPayload, "SECRET-SNAPSHOT-EXEC-999") || strings.Contains(snapshotPayload, "SECRET-SUBAGENT-RAW-999") {
		t.Fatalf("expected sanitized snapshot without legacy secrets, got %s", snapshotPayload)
	}
	if strings.Contains(snapshotPayload, "subagent_raw") {
		t.Fatalf("expected sanitized snapshot without subagent_raw field, got %s", snapshotPayload)
	}
}

func TestHandleWebCodingMessageSnapshot_SanitizesBeforePersistence(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-message-snapshot-sanitize.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-message-snapshot-sanitize"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Message Snapshot Sanitize",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	body := []byte(`{
		"session_id":"sess-message-snapshot-sanitize",
		"view_mode":"compact",
		"messages":[
			{
				"id":"exec-1",
				"role":"exec",
				"exec_command":"rtk go test ./...",
				"exec_status":"done",
				"exec_output":"SECRET-EXEC-OUTPUT-789"
			},
			{
				"id":"subagent-1",
				"role":"subagent",
				"content":"Spawned Planck",
				"subagent_raw":{"secret":"SECRET-SUBAGENT-RAW-456"}
			}
		]
	}`)
	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodPost, "/api/coding/message_snapshot", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleWebCodingMessageSnapshot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list canonical rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 canonical rows, got %d", len(rows))
	}
	for _, row := range rows {
		role := strings.TrimSpace(strings.ToLower(stringFromAny(row["role"])))
		if role == "exec" {
			if got := stringFromAny(row["exec_output"]); got != "[redacted]" {
				t.Fatalf("expected canonical exec_output redacted, got %q", got)
			}
		}
		if _, exists := row["subagent_raw"]; exists {
			t.Fatalf("expected canonical row without subagent_raw field: %#v", row)
		}
	}

	snapshotPayload, ok, err := st.GetCodingMessageSnapshot(ctx, sessionID, "compact")
	if err != nil {
		t.Fatalf("get compact snapshot: %v", err)
	}
	if !ok {
		t.Fatalf("expected compact snapshot persisted")
	}
	if strings.Contains(snapshotPayload, "SECRET-EXEC-OUTPUT-789") || strings.Contains(snapshotPayload, "SECRET-SUBAGENT-RAW-456") {
		t.Fatalf("expected sanitized snapshot payload without raw secrets, got %s", snapshotPayload)
	}
	if strings.Contains(snapshotPayload, "subagent_raw") {
		t.Fatalf("expected sanitized snapshot payload without subagent_raw field, got %s", snapshotPayload)
	}
}

func TestHandleWebCodingMessageSnapshot_RebuildsWhenClientSnapshotIsIncomplete(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-snapshot-incomplete.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := "sess-snapshot-incomplete"
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             sessionID,
		Title:          "Snapshot Incomplete",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	}); err != nil {
		t.Fatalf("create coding session: %v", err)
	}
	now := time.Date(2026, 3, 27, 6, 10, 0, 0, time.UTC)
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-user",
		SessionID: sessionID,
		Role:      "user",
		Content:   "hello",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-assistant",
		SessionID: sessionID,
		Role:      "assistant",
		Content:   "assistant reply",
		CreatedAt: now.Add(1 * time.Minute),
	}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if _, err := st.AppendCodingMessage(ctx, store.CodingMessage{
		ID:        "msg-activity",
		SessionID: sessionID,
		Role:      "activity",
		Actor:     "executor",
		Content:   "Executor resumed against legacy_owner continuation.",
		CreatedAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("append activity: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	reqBody := []byte(`{"session_id":"sess-snapshot-incomplete","view_mode":"compact","messages":[{"id":"message-msg-assistant","role":"assistant","content":"assistant reply","created_at":"2026-03-27T06:11:00Z","updated_at":"2026-03-27T06:11:00Z"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/coding/message_snapshot", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.handleWebCodingMessageSnapshot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rows, _, err := st.ListCodingViewMessagesPage(ctx, sessionID, "compact", 50, "")
	if err != nil {
		t.Fatalf("list canonical rows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected canonical rebuild to restore all rows, got %d", len(rows))
	}
	if got := stringFromAny(rows[0]["role"]); got != "user" {
		t.Fatalf("expected first canonical row to remain user, got %q", got)
	}
	snapshotPayload, ok, err := st.GetCodingMessageSnapshot(ctx, sessionID, "compact")
	if err != nil {
		t.Fatalf("get compact snapshot: %v", err)
	}
	if !ok {
		t.Fatalf("expected compact snapshot persisted")
	}
	if strings.Count(snapshotPayload, `"role":"assistant"`) != 1 || !strings.Contains(snapshotPayload, `"role":"activity"`) {
		t.Fatalf("expected canonical snapshot to replace incomplete client snapshot, got %s", snapshotPayload)
	}
}

func TestHandleWebCodingChat_IgnoresLegacyCommandField(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-chat-command-reject.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_chat_cmd_reject",
		Title:          "ChatCmdReject",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	body := []byte(`{"session_id":"sess_chat_cmd_reject","content":"hello","command":"review"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/coding/chat", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleWebCodingChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errorObj, _ := payload["error"].(map[string]any)
	if got := stringFromAny(errorObj["code"]); got != "unknown_runtime_error" {
		t.Fatalf("expected legacy command to be ignored and normal runtime error to surface, got %q", got)
	}
}

func TestCreateCodingSession_AllowsDefaultSessionWithoutLegacyReviewValidation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-chat-review-mode-reject.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	session, err := st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_chat_review_reject",
		Title:          "ChatReviewReject",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("expected default session creation to succeed, got %v", err)
	}
	if session.ID == "" {
		t.Fatalf("expected persisted session id")
	}
}

func TestHandleWebCodingChat_SanitizesRawBackendText(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-chat-sanitize.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s := &Server{svc: &service.Service{Store: st}}
	var logBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevLogWriter) })
	body := []byte(`{"session_id":"sess_missing","content":"hello","command":"chat"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/coding/chat", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleWebCodingChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	if strings.Contains(strings.ToLower(raw), "coding session not found") {
		t.Fatalf("expected sanitized response without raw backend text, got %q", raw)
	}
	if strings.Contains(strings.ToLower(logBuf.String()), "coding session not found") {
		t.Fatalf("expected sanitized logs without raw backend text, got %q", logBuf.String())
	}
}

func TestHandleWebCodingStatus_SanitizesRawBackendText(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-status-sanitize.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/status?session_id=sess_missing", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingStatus(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	if strings.Contains(strings.ToLower(raw), "coding session not found") {
		t.Fatalf("expected sanitized response without raw backend text, got %q", raw)
	}
}

func TestHandleWebCodingStatus_ReturnsCompactRuntimeContract(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-status-contract.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_status_contract",
		Title:          "StatusContract",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/status?session_id=sess_status_contract", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	expectedKeys := map[string]struct{}{
		"ok":         {},
		"session_id": {},
		"in_flight":  {},
		"started_at": {},
	}
	for key := range body {
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("expected compact status response, found unexpected key %q", key)
		}
	}
}

func TestHandleWebCodingStop_ReturnsCompactRuntimeContract(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-stop-contract.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_stop_contract",
		Title:          "StopContract",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodPost, "/api/coding/stop", bytes.NewBufferString(`{"session_id":"sess_stop_contract","force":false}`))
	rec := httptest.NewRecorder()

	s.handleWebCodingStop(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	expectedKeys := map[string]struct{}{
		"ok":         {},
		"session_id": {},
		"stopped":    {},
		"force":      {},
	}
	for key := range body {
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("expected compact stop response, found unexpected key %q", key)
		}
	}
}

func TestHandleWebCodingRuntimeDebug_ReturnsChatRuntimeMapping(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "coding-runtime-debug.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}
	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.AuthStoreDir = filepath.Join(root, "auth")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	svc := service.New(cfg, st, cry)

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_runtime_debug",
		Title:          "RuntimeDebug",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_chat_bound",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create coding session: %v", err)
	}

	for _, role := range []string{"chat"} {
		runtimeHome := svc.CodingRuntimeHomeForTests("sess_runtime_debug", role)
		if err := os.MkdirAll(runtimeHome, 0o755); err != nil {
			t.Fatalf("mkdir runtime home %s: %v", role, err)
		}
		if err := os.WriteFile(filepath.Join(runtimeHome, "auth.json"), []byte(`{"token":"ok"}`), 0o600); err != nil {
			t.Fatalf("write auth.json %s: %v", role, err)
		}
		stateDir := filepath.Join(filepath.Dir(runtimeHome), "state")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatalf("mkdir state dir %s: %v", role, err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "active-account-id"), []byte(role+"-account"), 0o600); err != nil {
			t.Fatalf("write account marker %s: %v", role, err)
		}
	}

	s := &Server{svc: svc}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/runtime/debug?session_id=sess_runtime_debug", nil)
	rec := httptest.NewRecorder()
	s.handleWebCodingRuntimeDebug(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		OK      bool `json:"ok"`
		Session struct {
			SessionID string `json:"session_id"`
			ThreadID  string `json:"thread_id"`
			Roles     map[string]struct {
				RuntimeHome       string `json:"runtime_home"`
				RuntimeHomeExists bool   `json:"runtime_home_exists"`
				StoredThreadID    string `json:"stored_thread_id"`
				ActiveAccountID   string `json:"active_account_id"`
				AuthJSONExists    bool   `json:"auth_json_exists"`
			} `json:"roles"`
		} `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.OK {
		t.Fatalf("expected ok response")
	}
	if body.Session.SessionID != "sess_runtime_debug" {
		t.Fatalf("expected session id, got %#v", body.Session)
	}
	if body.Session.ThreadID != "thread_chat_bound" {
		t.Fatalf("expected thread id, got %#v", body.Session)
	}
	if len(body.Session.Roles) != 1 {
		t.Fatalf("expected exactly one chat runtime role, got %#v", body.Session.Roles)
	}
	got, ok := body.Session.Roles["chat"]
	if !ok {
		t.Fatalf("expected chat role in payload")
	}
	if !got.RuntimeHomeExists || !got.AuthJSONExists {
		t.Fatalf("expected runtime home and auth.json for chat, got %#v", got)
	}
	if got.StoredThreadID != "thread_chat_bound" {
		t.Fatalf("expected stored thread %q for chat, got %#v", "thread_chat_bound", got)
	}
	if got.ActiveAccountID != "chat-account" {
		t.Fatalf("expected active account marker for chat, got %#v", got)
	}
	if !strings.Contains(got.RuntimeHome, filepath.Join("sess_runtime_debug", "chat", "codex-home")) {
		t.Fatalf("unexpected runtime home for chat: %q", got.RuntimeHome)
	}
}

func TestMapCodingEventMessages_PreservesEventAndStderrContent(t *testing.T) {
	input := []store.CodingMessage{
		{ID: "m1", Role: "event", Content: `{"type":"raw_event","secret":"abc123"}`},
		{ID: "m2", Role: "stderr", Content: "provider secret token=abc123"},
		{ID: "m3", Role: "activity", Content: "safe"},
	}
	out := mapCodingEventMessages(input)
	if len(out) != 3 {
		t.Fatalf("expected 3 mapped messages, got %d", len(out))
	}
	if got := stringFromAny(out[0]["content"]); got != `{"type":"raw_event","secret":"abc123"}` {
		t.Fatalf("expected event content preserved, got %q", got)
	}
	if got := stringFromAny(out[1]["content"]); got != "provider secret token=abc123" {
		t.Fatalf("expected stderr content preserved, got %q", got)
	}
	if got := stringFromAny(out[2]["content"]); got != "safe" {
		t.Fatalf("expected non-event role content unchanged, got %q", got)
	}
}
