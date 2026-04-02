package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func openCodingWS(t *testing.T, srv *Server) *websocket.Conn {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/coding/ws", srv.handleWebCodingWS)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)
	wsURL := "ws" + httpSrv.URL[len("http"):] + "/api/coding/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readWSEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var payload map[string]any
	if err := conn.ReadJSON(&payload); err != nil {
		t.Fatalf("read ws event: %v", err)
	}
	return payload
}

func readWSEvents(t *testing.T, conn *websocket.Conn, max int) []map[string]any {
	t.Helper()
	events := make([]map[string]any, 0, max)
	for i := 0; i < max; i++ {
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var payload map[string]any
		if err := conn.ReadJSON(&payload); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				break
			}
			t.Fatalf("read ws event: %v", err)
		}
		events = append(events, payload)
	}
	return events
}

func writeFakeCodexAppServerScript(t *testing.T, body string) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "fake-codex.sh")
	content := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake codex script: %v", err)
	}
	return scriptPath
}

func TestWSOriginAllowed_AllowsLoopbackProxyPortMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3052/api/coding/ws", nil)
	req.Host = "127.0.0.1:3052"
	req.Header.Set("Origin", "http://127.0.0.1:3051")

	if !wsOriginAllowed(req) {
		t.Fatalf("expected loopback dev proxy origin to be allowed")
	}
}

func TestWSOriginAllowed_RejectsNonLoopbackPortMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://codexsess.example.com/api/coding/ws", nil)
	req.Host = "codexsess.example.com:3052"
	req.Header.Set("Origin", "https://codexsess.example.com:3051")

	if wsOriginAllowed(req) {
		t.Fatalf("expected non-loopback cross-port origin to be rejected")
	}
}

func TestCodingWS_StateChangingRequiresRequestID(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-request-id.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_reqid",
		Title:          "ReqID",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"session_id": "sess_reqid",
		"content":    "hello",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	errorObj, _ := evt["error"].(map[string]any)
	if code := stringFromAny(errorObj["code"]); code != "bad_request" {
		t.Fatalf("expected bad_request, got %q", code)
	}
	if code := stringFromAny(evt["error_code"]); code != "bad_request" {
		t.Fatalf("expected top-level bad_request error_code, got %q", code)
	}
	if preserved, ok := evt["draft_preserved"].(bool); !ok || !preserved {
		t.Fatalf("expected draft_preserved=true")
	}
}

func TestCodingWS_RejectsLegacyExecutorLaneForChatOnlySession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-lane.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_lane",
		Title:          "Lane",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_lane_1",
		"session_id": "sess_lane",
		"lane":       "executor",
		"content":    "hello",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	errorObj, _ := evt["error"].(map[string]any)
	if code := stringFromAny(errorObj["code"]); code != "invalid_lane_for_session" {
		t.Fatalf("expected invalid_lane_for_session, got %q", code)
	}
	if code := stringFromAny(evt["error_code"]); code != "invalid_lane_for_session" {
		t.Fatalf("expected top-level invalid_lane_for_session, got %q", code)
	}
}

func TestCodingWS_UnsupportedLegacyControlRequestReturnsBadRequest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-legacy-mode.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_legacy_mode",
		Title:          "LegacyMode",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.legacy_control",
		"request_id": "req_legacy_mode_1",
		"session_id": "sess_legacy_mode",
		"mode":       "review",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	if code := stringFromAny(evt["error_code"]); code != "bad_request" {
		t.Fatalf("expected bad_request, got %q", code)
	}
	updated, err := st.GetCodingSession(context.Background(), "sess_legacy_mode")
	if err != nil {
		t.Fatalf("get updated session: %v", err)
	}
	if strings.TrimSpace(updated.ID) != "sess_legacy_mode" {
		t.Fatalf("expected unsupported legacy request to leave session state untouched, got %#v", updated)
	}
}

func TestCodingWS_SessionSendIgnoresLegacyCommandField(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-command-reject.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_cmd_reject",
		Title:          "CmdReject",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_cmd_reject_1",
		"session_id": "sess_cmd_reject",
		"lane":       "chat",
		"command":    "review",
		"content":    "/status",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.started" {
		t.Fatalf("expected session.started, got %q", got)
	}
	events := readWSEvents(t, conn, 4)
	var done map[string]any
	for _, candidate := range events {
		if stringFromAny(candidate["event"]) == "session.done" {
			done = candidate
			break
		}
	}
	if done == nil {
		t.Fatalf("expected session.done in ws event stream, got %#v", events)
	}
	assistant, _ := done["assistant"].(map[string]any)
	if !strings.Contains(stringFromAny(assistant["content"]), "CodexSess Status") {
		t.Fatalf("expected local status response, got %#v", assistant)
	}
}

func TestCodingWS_UnsupportedRequestTypeReturnsBadRequest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-review-source.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_review_source",
		Title:          "ReviewSource",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.unsupported_feature",
		"request_id": "req_unsupported_1",
		"session_id": "sess_review_source",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	if code := stringFromAny(evt["error_code"]); code != "bad_request" {
		t.Fatalf("expected bad_request for unsupported request type, got %q", code)
	}
}

func TestCodingWS_SessionSendRejectedInReviewMode(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-review-readonly.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_review_readonly",
		Title:          "ReviewReadonly",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_invalid_lane_1",
		"session_id": "sess_review_readonly",
		"lane":       "invalid-lane",
		"content":    "attempt stream in invalid lane",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	if code := stringFromAny(evt["error_code"]); code != "invalid_lane_for_session" {
		t.Fatalf("expected invalid_lane_for_session, got %q", code)
	}
}

func TestCodingWS_ReplaySnapshotEmitsChatOnlyContractWhenBehindEventSeq(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-replay.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:                  "sess_replay",
		Title:               "Replay",
		Model:               "gpt-5.2-codex",
		ReasoningLevel:      "medium",
		WorkDir:             "~/",
		SandboxMode:         "workspace-write",
		LastAppliedEventSeq: 7,
		CreatedAt:           now,
		UpdatedAt:           now,
		LastMessageAt:       now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, err = st.AppendCodingMessage(context.Background(), store.CodingMessage{
		ID:        "msg_replay_1",
		SessionID: "sess_replay",
		Role:      "assistant",
		Content:   "hello replay",
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":                "session.send",
		"request_id":          "req_replay_1",
		"session_id":          "sess_replay",
		"lane":                "chat",
		"content":             "/status",
		"last_seen_event_seq": 1,
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	foundSnapshot := false
	for i := 0; i < 10; i++ {
		evt := readWSEvent(t, conn)
		if stringFromAny(evt["event"]) != "session.snapshot" {
			continue
		}
		foundSnapshot = true
		messagesRaw, _ := evt["messages"].([]any)
		if len(messagesRaw) == 0 {
			t.Fatalf("expected snapshot messages to be non-empty")
		}
		if got := intFromAny(evt["replay_from_seq"]); got != 1 {
			t.Fatalf("expected replay_from_seq=1, got %d", got)
		}
		if got := intFromAny(evt["last_event_seq"]); got != 7 {
			t.Fatalf("expected last_event_seq=7, got %d", got)
		}
		session, _ := evt["session"].(map[string]any)
		if got := stringFromAny(session["id"]); got != "sess_replay" {
			t.Fatalf("expected replay snapshot to include chat-only session metadata, got %#v", session)
		}
		expectedKeys := map[string]struct{}{
			"id":                     {},
			"thread_id":              {},
			"title":                  {},
			"model":                  {},
			"reasoning_level":        {},
			"work_dir":               {},
			"sandbox_mode":           {},
			"last_applied_event_seq": {},
			"created_at":             {},
			"updated_at":             {},
			"last_message_at":        {},
		}
		for key := range session {
			if _, ok := expectedKeys[key]; !ok {
				t.Fatalf("expected replay snapshot session to stay compact, found unexpected key %q", key)
			}
		}
		if got := stringFromAny(session["thread_id"]); got != "" {
			t.Fatalf("expected replay snapshot thread_id to stay empty before runtime attach, got %q", got)
		}
		if _, ok := evt["lane_projections"]; ok {
			t.Fatalf("expected chat-only snapshot payload to omit lane_projections, got %#v", evt["lane_projections"])
		}
		break
	}
	if !foundSnapshot {
		raw, _ := json.Marshal(map[string]any{"note": "no session.snapshot seen"})
		t.Fatalf("expected session.snapshot event, got none (%s)", string(raw))
	}
}

func TestCodingWS_SessionErrorRejectsLegacyExecutorLaneWithoutPersistingLaneProjection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-error-projection.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_error_projection",
		Title:          "ErrorProjection",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_error_projection_1",
		"session_id": "sess_error_projection",
		"lane":       "executor",
		"content":    "should fail lane validation",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	if code := stringFromAny(evt["error_code"]); code != "invalid_lane_for_session" {
		t.Fatalf("expected invalid_lane_for_session, got %q", code)
	}

	rows, err := st.ListCodingViewMessages(context.Background(), "sess_error_projection", "lane_executor")
	if err != nil {
		t.Fatalf("ListCodingViewMessages lane_executor: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected chat-only contract to avoid persisting lane_executor replay rows, got %#v", rows)
	}
}

func TestCodingWS_SessionErrorPersistsAssistantBubbleInCompactHistory(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-error-compact.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_error_compact",
		Title:          "ErrorCompact",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_error_compact_1",
		"session_id": "sess_error_compact",
		"lane":       "executor",
		"content":    "trigger invalid lane",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}

	history, err := st.ListCodingMessages(context.Background(), "sess_error_compact")
	if err != nil {
		t.Fatalf("ListCodingMessages: %v", err)
	}
	foundAssistant := false
	for _, item := range history {
		if !strings.EqualFold(item.Role, "assistant") || !strings.EqualFold(item.Actor, "executor") {
			continue
		}
		content := strings.ToLower(strings.TrimSpace(item.Content))
		if strings.Contains(content, "run failed:") &&
			(strings.Contains(content, "request failed.") ||
				strings.Contains(content, "selected lane is not writable for this session.")) {
			foundAssistant = true
			break
		}
	}
	if !foundAssistant {
		t.Fatalf("expected persisted assistant error bubble, got %#v", history)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/coding/messages?session_id=sess_error_compact&view=compact&limit=50", nil)
	rec := httptest.NewRecorder()
	srv.handleWebCodingMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleWebCodingMessages status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	foundCompactAssistant := false
	for _, row := range body.Messages {
		if !strings.EqualFold(stringFromAny(row["role"]), "assistant") || !strings.EqualFold(stringFromAny(row["actor"]), "executor") {
			continue
		}
		content := strings.ToLower(stringFromAny(row["content"]))
		if strings.Contains(content, "run failed:") &&
			(strings.Contains(content, "request failed.") ||
				strings.Contains(content, "selected lane is not writable for this session.")) {
			foundCompactAssistant = true
			break
		}
	}
	if !foundCompactAssistant {
		t.Fatalf("expected compact assistant error bubble, got %#v", body.Messages)
	}
}

func TestCodingWS_ReplaySnapshotUsesCompactSubagentRows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-subagent-replay.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 3, 28, 3, 30, 0, 0, time.UTC)
	session, err := st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:                  "sess_subagent_replay",
		Title:               "SubagentReplay",
		Model:               "gpt-5.3-codex",
		ReasoningLevel:      "medium",
		WorkDir:             "~/",
		SandboxMode:         "workspace-write",
		LastAppliedEventSeq: 4,
		LastMessageAt:       now,
		UpdatedAt:           now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	rawPayload, err := json.Marshal(map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"item": map[string]any{
				"type": "function_call",
				"function": map[string]any{
					"name":      "spawn_agent",
					"arguments": `{"nickname":"Ptolemy","agent_type":"code-reviewer","message":"Re-review the current /chat architecture after recent fixes in the working tree.","model":"gpt-5.3-codex","reasoning_effort":"high"}`,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal raw payload: %v", err)
	}
	if _, err := st.AppendCodingMessage(context.Background(), store.CodingMessage{
		ID:        "msg_subagent_event",
		SessionID: session.ID,
		Role:      "event",
		Actor:     "chat",
		Content:   string(rawPayload),
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("append coding event: %v", err)
	}
	if _, err := st.AppendCodingMessage(context.Background(), store.CodingMessage{
		ID:        "msg_assistant",
		SessionID: session.ID,
		Role:      "assistant",
		Actor:     "chat",
		Content:   "Spawned a code-reviewer subagent.",
		CreatedAt: now.Add(10 * time.Millisecond),
	}); err != nil {
		t.Fatalf("append coding assistant: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":                "session.send",
		"request_id":          "req_subagent_replay",
		"session_id":          session.ID,
		"lane":                "chat",
		"content":             "/status",
		"last_seen_event_seq": 1,
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	foundSnapshot := false
	for i := 0; i < 12; i++ {
		evt := readWSEvent(t, conn)
		if stringFromAny(evt["event"]) != "session.snapshot" {
			continue
		}
		foundSnapshot = true
		rows, _ := evt["messages"].([]any)
		if len(rows) == 0 {
			t.Fatalf("expected compact snapshot rows, got none")
		}
		foundSubagent := false
		for _, raw := range rows {
			row, _ := raw.(map[string]any)
			if strings.TrimSpace(strings.ToLower(stringFromAny(row["role"]))) != "subagent" {
				continue
			}
			foundSubagent = true
			if got := stringFromAny(row["subagent_nickname"]); got != "Ptolemy" {
				t.Fatalf("expected subagent_nickname Ptolemy, got %q", got)
			}
			if got := stringFromAny(row["subagent_model"]); got != "gpt-5.3-codex" {
				t.Fatalf("expected subagent_model gpt-5.3-codex, got %q", got)
			}
			if got := stringFromAny(row["subagent_reasoning"]); got != "high" {
				t.Fatalf("expected subagent_reasoning high, got %q", got)
			}
		}
		if !foundSubagent {
			t.Fatalf("expected compact snapshot to include subagent row, got %#v", rows)
		}
		break
	}
	if !foundSnapshot {
		t.Fatalf("expected session.snapshot for replay")
	}
}

func TestCodingWS_ReplaySnapshotRejectsLegacyOrchestratorLaneForChatOnlySession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-replay-recovery-lanes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:                  "sess_replay_recovery_lanes",
		Title:               "ReplayRecoveryLanes",
		Model:               "gpt-5.2-codex",
		ReasoningLevel:      "medium",
		WorkDir:             "~/",
		SandboxMode:         "workspace-write",
		LastAppliedEventSeq: 6,
		CreatedAt:           now,
		UpdatedAt:           now,
		LastMessageAt:       now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":                "session.send",
		"request_id":          "req_replay_recovery_lanes_1",
		"session_id":          "sess_replay_recovery_lanes",
		"lane":                "legacy_owner",
		"content":             "/status",
		"last_seen_event_seq": 1,
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	if code := stringFromAny(evt["error_code"]); code != "invalid_lane_for_session" {
		t.Fatalf("expected invalid_lane_for_session for legacy_owner replay request, got %q", code)
	}
}

func TestCodingWS_ReplaySnapshotRedactsEventAndStderrMessages(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-replay-redact.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:                  "sess_replay_redact",
		Title:               "ReplayRedact",
		Model:               "gpt-5.2-codex",
		ReasoningLevel:      "medium",
		WorkDir:             "~/",
		SandboxMode:         "workspace-write",
		LastAppliedEventSeq: 5,
		CreatedAt:           now,
		UpdatedAt:           now,
		LastMessageAt:       now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	secretEvent := "provider raw secret token=event-123"
	secretStderr := "provider raw secret token=stderr-456"
	seedMessages := []store.CodingMessage{
		{ID: "msg_replay_redact_evt", SessionID: "sess_replay_redact", Role: "event", Content: secretEvent, CreatedAt: now},
		{ID: "msg_replay_redact_err", SessionID: "sess_replay_redact", Role: "stderr", Content: secretStderr, CreatedAt: now},
		{ID: "msg_replay_redact_asst", SessionID: "sess_replay_redact", Role: "assistant", Content: "safe assistant", CreatedAt: now},
	}
	for _, msg := range seedMessages {
		if _, err := st.AppendCodingMessage(context.Background(), msg); err != nil {
			t.Fatalf("append seed message %s: %v", msg.ID, err)
		}
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":                "session.send",
		"request_id":          "req_replay_redact_1",
		"session_id":          "sess_replay_redact",
		"lane":                "chat",
		"content":             "/status",
		"last_seen_event_seq": 1,
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	foundSnapshot := false
	for i := 0; i < 12; i++ {
		evt := readWSEvent(t, conn)
		if stringFromAny(evt["event"]) != "session.snapshot" {
			continue
		}
		foundSnapshot = true
		messagesRaw, _ := evt["messages"].([]any)
		if len(messagesRaw) < 2 {
			t.Fatalf("expected at least 2 snapshot messages, got %d", len(messagesRaw))
		}
		var sawStderr bool
		var sawAssistantUnredacted bool
		for _, raw := range messagesRaw {
			msg, _ := raw.(map[string]any)
			role := strings.TrimSpace(strings.ToLower(stringFromAny(msg["role"])))
			content := stringFromAny(msg["content"])
			switch role {
			case "stderr":
				sawStderr = true
				lower := strings.ToLower(content)
				if strings.Contains(lower, strings.ToLower(secretStderr)) {
					t.Fatalf("expected stderr replay to avoid raw secret text, got content=%q", content)
				}
			case "assistant":
				sawAssistantUnredacted = true
				if content != "safe assistant" {
					t.Fatalf("expected assistant replay message to remain visible, got %q", content)
				}
			}
		}
		if !sawStderr || !sawAssistantUnredacted {
			t.Fatalf("expected stderr and assistant replay messages, got %#v", messagesRaw)
		}
		break
	}
	if !foundSnapshot {
		t.Fatalf("expected session.snapshot event for replay redaction check")
	}
}

func TestCodingWS_DuplicateRequestIDRejectedWithoutStateMutation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-dup-request.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_dup_request",
		Title:          "DupRequest",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)

	reqID := "req_dup_send_1"
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": reqID,
		"session_id": "sess_dup_request",
		"lane":       "executor",
		"content":    "should fail lane validation",
	}); err != nil {
		t.Fatalf("write first session.send: %v", err)
	}
	first := readWSEvent(t, conn)
	if got := stringFromAny(first["event"]); got != "session.error" {
		t.Fatalf("expected session.error on first request, got %q", got)
	}

	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": reqID,
		"session_id": "sess_dup_request",
		"lane":       "executor",
		"content":    "should fail lane validation",
	}); err != nil {
		t.Fatalf("write duplicate session.send: %v", err)
	}
	foundDuplicate := false
	for i := 0; i < 8; i++ {
		second := readWSEvent(t, conn)
		if stringFromAny(second["event"]) == "session.duplicate_request" {
			foundDuplicate = true
			break
		}
	}
	if !foundDuplicate {
		t.Fatalf("expected session.duplicate_request event for duplicate request_id")
	}

	updated, err := st.GetCodingSession(context.Background(), "sess_dup_request")
	if err != nil {
		t.Fatalf("get session after duplicate request: %v", err)
	}
	if strings.TrimSpace(updated.ID) != "sess_dup_request" {
		t.Fatalf("expected duplicate request to leave session state intact, got %#v", updated)
	}
}

func TestCodingWS_ReplaySnapshotNotEmittedWhenLastSeenAhead(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-replay-ahead.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:                  "sess_replay_ahead",
		Title:               "ReplayAhead",
		Model:               "gpt-5.2-codex",
		ReasoningLevel:      "medium",
		WorkDir:             "~/",
		SandboxMode:         "workspace-write",
		LastAppliedEventSeq: 3,
		CreatedAt:           now,
		UpdatedAt:           now,
		LastMessageAt:       now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":                "session.send",
		"request_id":          "req_replay_ahead_1",
		"session_id":          "sess_replay_ahead",
		"lane":                "chat",
		"content":             "/status",
		"last_seen_event_seq": 99,
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	events := readWSEvents(t, conn, 10)
	for _, evt := range events {
		if stringFromAny(evt["event"]) == "session.snapshot" {
			t.Fatalf("did not expect session.snapshot when client is already ahead; events=%#v", events)
		}
	}
}

func TestCodingWS_EventDeliveryLatencyP95_Local(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-latency.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := &Server{svc: &service.Service{Store: st}}
	latencies := make([]float64, 0, 12)

	for i := 0; i < 12; i++ {
		sessionID := "sess_latency_" + strconv.Itoa(i+1)
		_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
			ID:             sessionID,
			Title:          "Latency",
			Model:          "gpt-5.2-codex",
			ReasoningLevel: "medium",
			WorkDir:        "~/",
			SandboxMode:    "workspace-write",
		})
		if err != nil {
			t.Fatalf("create session %s: %v", sessionID, err)
		}
		conn := openCodingWS(t, srv)
		reqID := "req_latency_" + strconv.Itoa(i+1)
		if err := conn.WriteJSON(map[string]any{
			"type":       "session.send",
			"request_id": reqID,
			"session_id": sessionID,
			"lane":       "chat",
			"content":    "/status",
		}); err != nil {
			t.Fatalf("write latency request %d: %v", i+1, err)
		}
		deadline := time.Now().Add(3 * time.Second)
		sawStarted := false
		sawDone := false
		for {
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting lifecycle events for req %s", reqID)
			}
			evt := readWSEvent(t, conn)
			eventType := stringFromAny(evt["event"])
			if eventType == "session.started" {
				evtReqID := stringFromAny(evt["request_id"])
				if evtReqID != reqID {
					continue
				}
				createdAt := stringFromAny(evt["created_at"])
				parsed, parseErr := time.Parse(time.RFC3339Nano, createdAt)
				if parseErr != nil {
					t.Fatalf("parse event created_at: %v", parseErr)
				}
				latencies = append(latencies, time.Since(parsed).Seconds()*1000)
				sawStarted = true
				if sawDone {
					break
				}
				continue
			}
			if eventType == "session.done" {
				evtReqID := stringFromAny(evt["request_id"])
				if evtReqID != reqID {
					continue
				}
				sawDone = true
				if sawStarted {
					break
				}
			}
		}
		_ = conn.Close()
	}

	if len(latencies) < 5 {
		t.Fatalf("insufficient latency samples: %d", len(latencies))
	}
	sort.Float64s(latencies)
	index := int(math.Ceil(0.95*float64(len(latencies)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(latencies) {
		index = len(latencies) - 1
	}
	p95 := latencies[index]
	if p95 > 1500 {
		t.Fatalf("expected local ws event delivery p95 <= 1500ms, got %.2fms (samples=%v)", p95, latencies)
	}
}

func TestCodingWS_ImmediateFollowupAfterDone_DoesNotReturnRuntimeBusy(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-done-followup.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := &Server{svc: &service.Service{Store: st}}
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_done_followup",
		Title:          "Done Followup",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	conn := openCodingWS(t, srv)
	defer conn.Close()

	sendReq := func(reqID string) {
		if err := conn.WriteJSON(map[string]any{
			"type":       "session.send",
			"request_id": reqID,
			"session_id": "sess_done_followup",
			"lane":       "chat",
			"content":    "/status",
		}); err != nil {
			t.Fatalf("write %s: %v", reqID, err)
		}
	}

	waitDone := func(reqID string, timeout time.Duration) {
		deadline := time.Now().Add(timeout)
		for {
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting session.done for %s", reqID)
			}
			evt := readWSEvent(t, conn)
			if stringFromAny(evt["request_id"]) != reqID {
				continue
			}
			eventType := stringFromAny(evt["event"])
			if eventType == "session.error" {
				t.Fatalf("unexpected session.error for %s: %#v", reqID, evt)
			}
			if eventType == "session.done" {
				return
			}
		}
	}

	sendReq("req_done_1")
	waitDone("req_done_1", 5*time.Second)

	sendReq("req_done_2")
	waitDone("req_done_2", 5*time.Second)
}

func TestCodingWS_SessionErrorSanitizesRawBackendText(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-sanitize.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := &Server{svc: &service.Service{Store: st}}
	var logBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevLogWriter) })
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_sanitize_1",
		"session_id": "sess_missing",
		"lane":       "chat",
		"content":    "hello",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
	evt := readWSEvent(t, conn)
	if got := stringFromAny(evt["event"]); got != "session.error" {
		t.Fatalf("expected session.error, got %q", got)
	}
	if code := stringFromAny(evt["error_code"]); code != "runtime_unavailable" {
		t.Fatalf("expected runtime_unavailable, got %q", code)
	}
	message := stringFromAny(evt["message"])
	if message == "" {
		t.Fatalf("expected non-empty sanitized message")
	}
	if strings.Contains(strings.ToLower(message), "coding session not found") {
		t.Fatalf("expected sanitized message without raw backend text, got %q", message)
	}
	errorObj, _ := evt["error"].(map[string]any)
	nested := stringFromAny(errorObj["message"])
	if strings.Contains(strings.ToLower(nested), "coding session not found") {
		t.Fatalf("expected sanitized nested message without raw backend text, got %q", nested)
	}
	if strings.Contains(strings.ToLower(logBuf.String()), "coding session not found") {
		t.Fatalf("expected log output without raw backend text, got %q", logBuf.String())
	}
}

func TestCodingWS_ModelCapacityErrorIsClassified(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-model-capacity.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_model_capacity",
		Title:          "ModelCapacity",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_capacity_existing",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	svc := &service.Service{Store: st}
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
echo 'Failed to send: codex runtime failed: Selected model is at capacity. Please try a different model.' 1>&2
exit 1
`))
	srv := &Server{svc: svc}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_model_capacity_1",
		"session_id": "sess_model_capacity",
		"lane":       "chat",
		"content":    "continue",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	events := readWSEvents(t, conn, 20)
	var errorEvent map[string]any
	for _, evt := range events {
		if stringFromAny(evt["event"]) == "session.error" {
			errorEvent = evt
			break
		}
	}
	if errorEvent == nil {
		t.Fatalf("expected session.error event, got %#v", events)
	}
	if got := stringFromAny(errorEvent["error_code"]); got != "model_capacity" && got != "account_switch_failed" && got != "unknown_runtime_error" {
		t.Fatalf("expected model_capacity/account_switch_failed/unknown_runtime_error error_code, got %q", got)
	}
	if got := stringFromAny(errorEvent["category"]); got != "model_capacity" && got != "unknown_runtime_error" {
		t.Fatalf("expected model_capacity/unknown_runtime_error category, got %q", got)
	}
}

func TestCodingWS_ModelCapacityAccountSwitchFailureIsClassified(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-model-capacity-switch-fail.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_model_capacity_switch_fail",
		Title:          "ModelCapacitySwitchFail",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_capacity_existing",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	svc := &service.Service{Store: st}
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
echo 'Failed to send: codex runtime failed: Selected model is at capacity. Please try a different model.' 1>&2
exit 1
`))
	srv := &Server{svc: svc}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_model_capacity_switch_fail_1",
		"session_id": "sess_model_capacity_switch_fail",
		"lane":       "chat",
		"content":    "continue",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	events := readWSEvents(t, conn, 20)
	var errorEvent map[string]any
	for _, evt := range events {
		if stringFromAny(evt["event"]) == "session.error" {
			errorEvent = evt
			break
		}
	}
	if errorEvent == nil {
		t.Fatalf("expected session.error event, got %#v", events)
	}
	if got := stringFromAny(errorEvent["error_code"]); got != "account_switch_failed" && got != "unknown_runtime_error" {
		t.Fatalf("expected account_switch_failed/unknown_runtime_error error_code, got %q", got)
	}
	if got := stringFromAny(errorEvent["category"]); got != "model_capacity" && got != "unknown_runtime_error" {
		t.Fatalf("expected model_capacity/unknown_runtime_error category, got %q", got)
	}
}

func TestCodingWS_UsageLimitErrorIsClassified(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-usage-limit.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_usage_limit",
		Title:          "UsageLimit",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_usage_existing",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	svc := &service.Service{Store: st}
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
echo "You've hit your usage limit. Upgrade to Plus to continue using Codex." 1>&2
exit 1
`))
	srv := &Server{svc: svc}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_usage_limit_1",
		"session_id": "sess_usage_limit",
		"lane":       "chat",
		"content":    "continue",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	events := readWSEvents(t, conn, 20)
	var errorEvent map[string]any
	for _, evt := range events {
		if stringFromAny(evt["event"]) == "session.error" {
			errorEvent = evt
			break
		}
	}
	if errorEvent == nil {
		t.Fatalf("expected session.error event, got %#v", events)
	}
	if got := stringFromAny(errorEvent["error_code"]); got != "usage_limit" && got != "unknown_runtime_error" {
		t.Fatalf("expected usage_limit/unknown_runtime_error error_code, got %q", got)
	}
	if got := stringFromAny(errorEvent["category"]); got != "usage_limit" && got != "unknown_runtime_error" {
		t.Fatalf("expected usage_limit/unknown_runtime_error category, got %q", got)
	}
}

func TestCodingWS_AuthFailureErrorIsClassified(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-auth-failed.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_auth_failed",
		Title:          "AuthFailed",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_auth_existing",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	svc := &service.Service{Store: st}
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
echo 'unexpected status 401 Unauthorized: account_deactivated' 1>&2
exit 1
`))
	srv := &Server{svc: svc}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_auth_failed_1",
		"session_id": "sess_auth_failed",
		"lane":       "chat",
		"content":    "continue",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	events := readWSEvents(t, conn, 20)
	var errorEvent map[string]any
	for _, evt := range events {
		if stringFromAny(evt["event"]) == "session.error" {
			errorEvent = evt
			break
		}
	}
	if errorEvent == nil {
		t.Fatalf("expected session.error event, got %#v", events)
	}
	if got := stringFromAny(errorEvent["error_code"]); got != "auth_failed" && got != "unknown_runtime_error" {
		t.Fatalf("expected auth_failed/unknown_runtime_error error_code, got %q", got)
	}
	if got := stringFromAny(errorEvent["category"]); got != "auth_failed" && got != "unknown_runtime_error" {
		t.Fatalf("expected auth_failed/unknown_runtime_error category, got %q", got)
	}
}

func TestCodingWS_StreamIncludesSourceIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}

	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "coding-ws-stream-identity.db"))
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

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_stream_identity",
		Title:          "StreamIdentity",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	tokenID, err := cry.Encrypt([]byte("id-token-stream-identity"))
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	tokenAccess, err := cry.Encrypt([]byte("access-token-stream-identity"))
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	tokenRefresh, err := cry.Encrypt([]byte("refresh-token-stream-identity"))
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	account := store.Account{
		ID:           "acc_stream_identity",
		Email:        "stream-identity@example.com",
		AccountID:    "acct-stream-identity",
		TokenID:      tokenID,
		TokenAccess:  tokenAccess,
		TokenRefresh: tokenRefresh,
		CodexHome:    cfg.CodexHome,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastUsedAt:   now,
	}
	if err := st.UpsertAccount(context.Background(), account); err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, account.ID), "id-token-stream-identity", "access-token-stream-identity", "refresh-token-stream-identity", account.AccountID); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	svc := service.New(cfg, st, cry)
	if _, err := svc.UseAccountCLI(context.Background(), account.ID); err != nil {
		t.Fatalf("use account cli: %v", err)
	}
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"jsonrpc":"2.0","id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"jsonrpc":"2.0","id":"2","result":{"thread":{"id":"thread-chat-1"}}}'
      echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-chat-1"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      req_id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
      [ -n "$req_id" ] || req_id=3
      printf '{"jsonrpc":"2.0","id":"%s","result":{"turn":{"id":"turn-chat-1","status":"completed"}}}\n' "$req_id"
      echo '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"thread-chat-1","turn":{"id":"turn-chat-1"}}}'
      echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-chat-1","turnId":"turn-chat-1","itemId":"item-assistant-1","sequence":17,"createdAt":"2026-04-02T10:11:12Z","item":{"type":"agent_message"},"delta":"hello"}}'
      echo '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thread-chat-1","turnId":"turn-chat-1","item":{"id":"item-assistant-1","type":"agent_message","text":"hello"}}}'
      echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-chat-1","turn":{"id":"turn-chat-1","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`))
	srv := &Server{svc: svc}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_stream_identity_1",
		"session_id": "sess_stream_identity",
		"lane":       "chat",
		"content":    "hello",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	events := readWSEvents(t, conn, 20)
	for _, evt := range events {
		if stringFromAny(evt["event"]) != "session.stream" {
			continue
		}
		if stringFromAny(evt["stream_type"]) != "delta" {
			continue
		}
		if got := stringFromAny(evt["source_event_type"]); got != "item/agentMessage/delta" {
			t.Fatalf("expected source_event_type, got %q in %#v", got, evt)
		}
		if got := stringFromAny(evt["source_thread_id"]); got != "thread-chat-1" {
			t.Fatalf("expected source_thread_id, got %q in %#v", got, evt)
		}
		if got := stringFromAny(evt["source_turn_id"]); got != "turn-chat-1" {
			t.Fatalf("expected source_turn_id, got %q in %#v", got, evt)
		}
		if got := stringFromAny(evt["source_item_id"]); got != "item-assistant-1" {
			t.Fatalf("expected source_item_id, got %q in %#v", got, evt)
		}
		if got := stringFromAny(evt["source_item_type"]); got != "agent_message" {
			t.Fatalf("expected source_item_type, got %q in %#v", got, evt)
		}
		if got := intFromAny(evt["event_seq"]); got != 17 {
			t.Fatalf("expected provider event_seq, got %d in %#v", got, evt)
		}
		if got := stringFromAny(evt["created_at"]); got != "2026-04-02T10:11:12Z" {
			t.Fatalf("expected provider created_at, got %q in %#v", got, evt)
		}
		return
	}
	t.Fatalf("expected session.stream delta with source identity, got %#v", events)
}

func TestCodingWS_StreamRedactsRawEventAndStderrText(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-stream-redact.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_stream_redact",
		Title:          "StreamRedact",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	svc := &service.Service{Store: st}
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"jsonrpc":"2.0","id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"jsonrpc":"2.0","id":"2","result":{"thread":{"id":"thread_stream_redact"}}}'
      echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread_stream_redact"}}}'
        ;;
      *"\"method\":\"turn/start\""*)
      printf '%s\n' 'provider runtime raw secret token=abc123' >&2
      req_id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
      [ -n "$req_id" ] || req_id=4
      printf '{"jsonrpc":"2.0","id":"%s","result":{"turn":{"id":"turn_stream_redact","status":"completed"}}}\n' "$req_id"
      echo '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"thread_stream_redact","turn":{"id":"turn_stream_redact"}}}'
      echo '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thread_stream_redact","turnId":"turn_stream_redact","item":{"id":"item_0","type":"agentMessage","text":"stream done"}}}'
      echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread_stream_redact","turn":{"id":"turn_stream_redact","status":"completed"}}}'
      exit 0
    esac
  done
fi
exit 1
`))
	srv := &Server{svc: svc}
	var logBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevLogWriter) })
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_stream_redact_1",
		"session_id": "sess_stream_redact",
		"lane":       "chat",
		"content":    "hello",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	events := readWSEvents(t, conn, 20)
	secretNeedle := "token=abc123"
	foundRedacted := false
	seenRawEvent := false
	sawSessionError := false
	streamEvents := make([]map[string]any, 0, len(events))
	foundDoneRedaction := false
	for _, evt := range events {
		if stringFromAny(evt["event"]) != "session.stream" {
			if stringFromAny(evt["event"]) == "session.error" && stringFromAny(evt["error_code"]) == "unknown_runtime_error" {
				sawSessionError = true
			}
			if stringFromAny(evt["event"]) == "session.done" {
				eventMessages, _ := evt["event_messages"].([]any)
				for _, raw := range eventMessages {
					item, _ := raw.(map[string]any)
					role := strings.TrimSpace(strings.ToLower(stringFromAny(item["role"])))
					if role != "event" && role != "stderr" {
						continue
					}
					foundDoneRedaction = true
					if got := stringFromAny(item["content"]); got != "[redacted]" {
						t.Fatalf("expected session.done event_messages redaction, got %q", got)
					}
				}
			}
			continue
		}
		streamEvents = append(streamEvents, evt)
		streamType := strings.TrimSpace(strings.ToLower(stringFromAny(evt["stream_type"])))
		if streamType == "raw_event" {
			seenRawEvent = true
		}
		if streamType != "stderr" && streamType != "raw_event" {
			continue
		}
		if streamType == "stderr" {
			foundRedacted = true
			if got := stringFromAny(evt["text"]); got != "[redacted]" {
				t.Fatalf("expected redacted stream text, got %q", got)
			}
		}
	}
	if !seenRawEvent && !sawSessionError {
		t.Fatalf("expected raw_event stream events to reach the websocket for frontend specialization, got %#v", events)
	}
	if !foundRedacted && !sawSessionError {
		t.Fatalf("expected at least one redacted stderr stream event, got %#v", events)
	}
	if !foundDoneRedaction && !sawSessionError {
		t.Fatalf("expected session.done to include redacted event_messages for event/stderr roles, got %#v", events)
	}
	blob, _ := json.Marshal(streamEvents)
	if strings.Contains(strings.ToLower(string(blob)), strings.ToLower(secretNeedle)) {
		t.Fatalf("expected no raw secret leakage in session.stream events, got %s", string(blob))
	}
	if strings.Contains(strings.ToLower(logBuf.String()), strings.ToLower(secretNeedle)) {
		t.Fatalf("expected no raw secret leakage in logs, got %s", logBuf.String())
	}
}

func TestCodingWS_SessionSendContinuesAfterClientDisconnect(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-ws-disconnect-background.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_disconnect_background",
		Title:          "DisconnectBackground",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	svc := &service.Service{Store: st}
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
echo '{"type":"thread.started","thread_id":"thread_disconnect_background"}'
sleep 1
echo '{"type":"item.completed","item":{"id":"item_0","type":"agentMessage","text":"background run finished"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":5}}'
`))
	srv := &Server{svc: svc}
	conn := openCodingWS(t, srv)
	if err := conn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_disconnect_background_1",
		"session_id": "sess_disconnect_background",
		"lane":       "chat",
		"content":    "continue in background",
	}); err != nil {
		t.Fatalf("write ws request: %v", err)
	}

	sawStarted := false
	for i := 0; i < 8; i++ {
		evt := readWSEvent(t, conn)
		if stringFromAny(evt["event"]) == "session.started" {
			sawStarted = true
			break
		}
	}
	if !sawStarted {
		t.Fatalf("expected session.started before disconnect")
	}
	time.Sleep(200 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatalf("close ws conn: %v", err)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		inFlight, _, _ := svc.CodingRunStatus("sess_disconnect_background")
		messages, listErr := st.ListCodingMessages(context.Background(), "sess_disconnect_background")
		if listErr != nil {
			t.Fatalf("list messages: %v", listErr)
		}
		if inFlight {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		foundAssistant := false
		foundKnownPreflightFailure := false
		for _, msg := range messages {
			if strings.TrimSpace(strings.ToLower(msg.Role)) == "stderr" && strings.Contains(strings.ToLower(msg.Content), "close sent") {
				t.Fatalf("expected disconnect not to persist websocket failure, got stderr %q", msg.Content)
			}
			if strings.TrimSpace(strings.ToLower(msg.Role)) == "stderr" && strings.Contains(strings.ToLower(msg.Content), "no healthy codex account available for runtime") {
				foundKnownPreflightFailure = true
			}
			if strings.TrimSpace(strings.ToLower(msg.Role)) == "assistant" && strings.Contains(msg.Content, "background run finished") {
				foundAssistant = true
			}
		}
		if !foundAssistant && !foundKnownPreflightFailure {
			t.Fatalf("expected assistant result or known preflight runtime failure after disconnect, got %#v", messages)
		}
		if foundAssistant {
			session, getErr := st.GetCodingSession(context.Background(), "sess_disconnect_background")
			if getErr != nil {
				t.Fatalf("get session: %v", getErr)
			}
			if session.CodexThreadID != "thread_disconnect_background" {
				t.Fatalf("expected canonical thread id to persist after disconnect, got %q", session.CodexThreadID)
			}
		}
		return
	}
	t.Fatalf("expected background coding run to finish after websocket disconnect")
}

func TestCodingMessageProjectionLane_RemovedOrchestratorOwnershipNoLongerProjectsLane(t *testing.T) {
	if got := codingMessageProjectionLane(store.CodingMessage{
		Role:    "exec",
		Content: "rtk go test ./...",
	}); got != "" {
		t.Fatalf("expected no lane for actorless exec rows, got %q", got)
	}
	if got := codingMessageProjectionLane(store.CodingMessage{
		Role:    "exec",
		Actor:   "legacy_owner",
		Content: "rtk go test ./...",
	}); got != "" {
		t.Fatalf("expected removed legacy_owner lane to stay empty for exec rows, got %q", got)
	}
	if got := codingMessageProjectionLane(store.CodingMessage{
		Role:    "subagent",
		Content: "Spawned code-reviewer helper",
	}); got != "" {
		t.Fatalf("expected no lane for actorless subagent rows, got %q", got)
	}
	if got := codingMessageProjectionLane(store.CodingMessage{
		Role:    "subagent",
		Actor:   "legacy_owner",
		Content: "Spawned legacy_owner helper",
	}); got != "" {
		t.Fatalf("expected removed legacy_owner lane to stay empty for subagent rows, got %q", got)
	}
	if got := codingMessageProjectionLane(store.CodingMessage{
		Role:    "stderr",
		Content: "Run failed: usage limit reached",
	}); got != "" {
		t.Fatalf("expected no lane for actorless stderr rows, got %q", got)
	}
	if got := codingMessageProjectionLane(store.CodingMessage{
		Role:    "activity",
		Content: "Resuming legacy_owner thread: thread_123",
	}); got != "" {
		t.Fatalf("expected no lane for actorless activity rows, got %q", got)
	}
	if got := codingMessageProjectionLane(store.CodingMessage{
		Role:    "activity",
		Actor:   "legacy_owner",
		Content: "Resuming legacy_owner thread: thread_123",
	}); got != "" {
		t.Fatalf("expected removed legacy_owner lane to stay empty for activity rows, got %q", got)
	}
}

func TestMapCodingSession_UsesCompactPublicContract(t *testing.T) {
	now := time.Date(2026, 3, 30, 8, 0, 0, 0, time.UTC)
	relayUpdatedAt := now.Add(2 * time.Minute)
	mapped := mapCodingSession(store.CodingSession{
		ID:            "sess_map",
		WorkDir:       t.TempDir(),
		CreatedAt:     now,
		UpdatedAt:     relayUpdatedAt,
		LastMessageAt: relayUpdatedAt,
	})

	expectedKeys := map[string]struct{}{
		"id":                     {},
		"thread_id":              {},
		"title":                  {},
		"model":                  {},
		"reasoning_level":        {},
		"work_dir":               {},
		"sandbox_mode":           {},
		"last_applied_event_seq": {},
		"created_at":             {},
		"updated_at":             {},
		"last_message_at":        {},
	}
	for key := range mapped {
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("expected compact session payload, found unexpected key %q", key)
		}
	}
}

func TestCodingRuntimeRestartResponse_OmitsLegacyRuntimeFields(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-runtime-restart-contract.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	_, err = st.CreateCodingSession(context.Background(), store.CodingSession{
		ID:             "sess_runtime_restart_contract",
		Title:          "RuntimeRestartContract",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv := &Server{svc: &service.Service{Store: st}}
	reqBody := bytes.NewBufferString(`{"session_id":"sess_runtime_restart_contract","force":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/coding/runtime/restart", reqBody)
	rec := httptest.NewRecorder()
	srv.handleWebCodingRuntimeRestart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, key := range []string{"ok", "accepted", "deferred", "session_id", "in_flight"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected %q in runtime restart response", key)
		}
	}
	expectedKeys := map[string]struct{}{
		"ok":         {},
		"accepted":   {},
		"deferred":   {},
		"session_id": {},
		"in_flight":  {},
	}
	for key := range payload {
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("expected runtime restart response to stay compact, found unexpected key %q", key)
		}
	}
}

func TestMapCodingSession_OmitsRuntimeStateFields(t *testing.T) {
	now := time.Now().UTC()
	mapped := mapCodingSession(store.CodingSession{
		ID:             "sess_runtime_state",
		RestartPending: true,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})

	if _, ok := mapped["restart_pending"]; ok {
		t.Fatalf("expected restart_pending to be omitted from public session payload")
	}
}

func TestMapCodingSession_UsesCanonicalChatThreadContract(t *testing.T) {
	now := time.Now().UTC()
	mapped := mapCodingSession(store.CodingSession{
		ID:            "sess_shared_thread_contract",
		CodexThreadID: "thread_exec_shared",
		CreatedAt:     now,
		UpdatedAt:     now,
		LastMessageAt: now,
	})

	if got := stringFromAny(mapped["thread_id"]); got != "thread_exec_shared" {
		t.Fatalf("expected thread_id to project shared chat thread, got %q", got)
	}
}

func TestMapCodingSession_UsesSingleThreadIDForPlainChatSession(t *testing.T) {
	now := time.Now().UTC()
	mapped := mapCodingSession(store.CodingSession{
		ID:            "sess_plain_chat_contract",
		CodexThreadID: "thread_chat_only",
		CreatedAt:     now,
		UpdatedAt:     now,
		LastMessageAt: now,
	})

	if got := stringFromAny(mapped["thread_id"]); got != "thread_chat_only" {
		t.Fatalf("expected thread_id to preserve chat thread, got %q", got)
	}
}
