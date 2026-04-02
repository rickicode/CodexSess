package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/trafficlog"
)

func TestWithTrafficLog_CapturesRequestAndResponse(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("claude", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/messages",
		strings.NewReader(`{"model":"gpt-5.2-codex","messages":[{"role":"user","content":"hi"}],"stream":false}`),
	)
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got, _ := entry["path"].(string); got != "/v1/messages" {
		t.Fatalf("expected path /v1/messages, got %q", got)
	}
	if got, _ := entry["protocol"].(string); got != "claude" {
		t.Fatalf("expected protocol claude, got %q", got)
	}
	if got, _ := entry["model"].(string); got != "gpt-5.2-codex" {
		t.Fatalf("expected model gpt-5.2-codex, got %q", got)
	}
	if got, _ := entry["status"].(float64); int(got) != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %v", entry["status"])
	}
	if got, _ := entry["response_body"].(string); !strings.Contains(got, "bad request") {
		t.Fatalf("expected response body to be captured, got %q", got)
	}
}

func TestWithTrafficLog_CapturesTokenUsageOpenAI(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("openai", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.2-codex","stream":false}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got := int(entry["request_tokens"].(float64)); got != 12 {
		t.Fatalf("expected request_tokens=12, got %d", got)
	}
	if got := int(entry["response_tokens"].(float64)); got != 8 {
		t.Fatalf("expected response_tokens=8, got %d", got)
	}
	if got := int(entry["total_tokens"].(float64)); got != 20 {
		t.Fatalf("expected total_tokens=20, got %d", got)
	}
}

func TestWithTrafficLog_CapturesTokenUsageClaude(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("claude", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","usage":{"input_tokens":5,"output_tokens":7}}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.2-codex","stream":false}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got := int(entry["request_tokens"].(float64)); got != 5 {
		t.Fatalf("expected request_tokens=5, got %d", got)
	}
	if got := int(entry["response_tokens"].(float64)); got != 7 {
		t.Fatalf("expected response_tokens=7, got %d", got)
	}
	if got := int(entry["total_tokens"].(float64)); got != 12 {
		t.Fatalf("expected total_tokens=12, got %d", got)
	}
}

func TestWithTrafficLog_CapturesTokenUsageFromStream(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("openai", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7}}}\n\n"))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.2-codex","stream":true}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got := int(entry["request_tokens"].(float64)); got != 11 {
		t.Fatalf("expected request_tokens=11, got %d", got)
	}
	if got := int(entry["response_tokens"].(float64)); got != 7 {
		t.Fatalf("expected response_tokens=7, got %d", got)
	}
	if got := int(entry["total_tokens"].(float64)); got != 18 {
		t.Fatalf("expected total_tokens=18, got %d", got)
	}
}

func TestWithTrafficLog_CapturesResolvedAccountWithoutLeakingHeaders(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("openai", func(w http.ResponseWriter, _ *http.Request) {
		setResolvedAccountHeaders(w, store.Account{
			ID:    "acc_test_1",
			Email: "tester@example.com",
		})
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.2-codex","stream":false}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	if got := rec.Header().Get("X-Codex-Resolved-Account-ID"); got != "" {
		t.Fatalf("expected no leaked account id header, got %q", got)
	}
	if got := rec.Header().Get("X-Codex-Resolved-Account-Email"); got != "" {
		t.Fatalf("expected no leaked account email header, got %q", got)
	}

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got, _ := entry["account_id"].(string); got != "acc_test_1" {
		t.Fatalf("expected account_id acc_test_1, got %q", got)
	}
	if got, _ := entry["account_email"].(string); got != "tester@example.com" {
		t.Fatalf("expected account_email tester@example.com, got %q", got)
	}
}

func TestHandleWebLogs_DeleteClearsTrafficLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	if err := logger.Append(trafficlog.Entry{Path: "/v1/messages", Method: http.MethodPost}); err != nil {
		t.Fatalf("append traffic log: %v", err)
	}

	s := &Server{traffic: logger}
	req := httptest.NewRequest(http.MethodDelete, "/api/logs", nil)
	rec := httptest.NewRecorder()

	s.handleWebLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	lines, err := logger.ReadTail(10)
	if err != nil {
		t.Fatalf("read traffic log: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected logs to be cleared, got %d line(s)", len(lines))
	}
}

func TestDetectTrafficModelAndStream_SupportsNewClaudePath(t *testing.T) {
	model, stream := detectTrafficModelAndStream("/v1/messages", []byte(`{"model":"gpt-5.2-codex","stream":true}`))
	if model != "gpt-5.2-codex" {
		t.Fatalf("expected model gpt-5.2-codex, got %q", model)
	}
	if !stream {
		t.Fatalf("expected stream=true")
	}
}
