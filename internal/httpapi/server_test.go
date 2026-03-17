package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/trafficlog"
)

func TestHandleOpenAIV1Root_RejectsInvalidPayload(t *testing.T) {
	s := &Server{apiKey: "sk-test"}

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader("{"))
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()

		s.handleOpenAIV1Root(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "invalid JSON body") {
			t.Fatalf("expected invalid JSON message, got %s", body)
		}
	})

	t.Run("unsupported payload shape", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(`{"foo":"bar"}`))
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()

		s.handleOpenAIV1Root(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "unsupported /v1 payload") {
			t.Fatalf("expected unsupported payload message, got %s", body)
		}
	})
}

func TestHandleModels_Unauthorized(t *testing.T) {
	s := &Server{apiKey: "sk-test"}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	s.handleModels(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleClaudeMessages_Unauthorized(t *testing.T) {
	s := &Server{apiKey: "sk-test"}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.2-codex"}`))
	rec := httptest.NewRecorder()

	s.handleClaudeMessages(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleWebSettings_ClaudeEndpointUsesV1Messages(t *testing.T) {
	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3052",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{"default": "gpt-5.2-codex"},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Host = "127.0.0.1:3052"
	rec := httptest.NewRecorder()

	s.handleWebSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claudeEndpoint, _ := body["claude_endpoint"].(string)
	if !strings.HasSuffix(claudeEndpoint, "/v1/messages") {
		t.Fatalf("expected claude endpoint to end with /v1/messages, got %q", claudeEndpoint)
	}
}

func TestHandleWebModelMappings_CRUD(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &Server{
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/model-mappings", strings.NewReader(`{"alias":"default","model":"gpt-5.2-codex"}`))
	postRec := httptest.NewRecorder()
	s.handleWebModelMappings(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("post expected 200, got %d body=%s", postRec.Code, postRec.Body.String())
	}
	if got := s.resolveMappedModel("default"); got != "gpt-5.2-codex" {
		t.Fatalf("expected mapped model gpt-5.2-codex, got %q", got)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/model-mappings?alias=default", nil)
	delRec := httptest.NewRecorder()
	s.handleWebModelMappings(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete expected 200, got %d body=%s", delRec.Code, delRec.Body.String())
	}
	if got := s.resolveMappedModel("default"); got != "default" {
		t.Fatalf("expected mapping to be removed, got %q", got)
	}
}

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

func TestDetectTrafficModelAndStream_SupportsNewClaudePath(t *testing.T) {
	model, stream := detectTrafficModelAndStream("/v1/messages", []byte(`{"model":"gpt-5.2-codex","stream":true}`))
	if model != "gpt-5.2-codex" {
		t.Fatalf("expected model gpt-5.2-codex, got %q", model)
	}
	if !stream {
		t.Fatalf("expected stream=true")
	}
}

