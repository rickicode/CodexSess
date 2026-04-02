package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/trafficlog"
)

func (s *Server) withTrafficLog(protocol string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.traffic == nil {
			next(w, r)
			return
		}
		captureBody := &limitedCaptureReadCloser{
			rc:    r.Body,
			limit: maxTrafficRequestCaptureBytes,
		}
		r.Body = captureBody
		start := time.Now()
		rec := &trafficRecorder{
			ResponseWriter:    w,
			status:            http.StatusOK,
			responseBodyLimit: maxTrafficResponseCaptureBytes,
		}
		next(rec, r)
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}

		bodyBytes := captureBody.Captured()
		model, stream := detectTrafficModelAndStream(r.URL.Path, bodyBytes)
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			remote = host
		}
		responseBody := strings.TrimSpace(string(rec.responseBody))
		requestTokens, responseTokens, totalTokens := parseTrafficUsageTokens(responseBody)
		_ = s.traffic.Append(trafficlog.Entry{
			Timestamp:         time.Now().UTC(),
			Protocol:          protocol,
			Method:            r.Method,
			Path:              r.URL.Path,
			Status:            rec.status,
			LatencyMS:         time.Since(start).Milliseconds(),
			RemoteAddr:        strings.TrimSpace(remote),
			UserAgent:         strings.TrimSpace(r.UserAgent()),
			AccountHint:       strings.TrimSpace(r.Header.Get("X-Codex-Account")),
			AccountID:         strings.TrimSpace(rec.accountID),
			AccountEmail:      strings.TrimSpace(rec.accountEmail),
			Model:             model,
			Stream:            stream,
			RequestBody:       strings.TrimSpace(string(bodyBytes)),
			ResponseBody:      responseBody,
			RequestTokens:     requestTokens,
			ResponseTokens:    responseTokens,
			TotalTokens:       totalTokens,
			RequestTruncated:  captureBody.Truncated(),
			ResponseTruncated: rec.bodyTruncated,
		})
	}
}

type limitedCaptureReadCloser struct {
	rc        io.ReadCloser
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (l *limitedCaptureReadCloser) Read(p []byte) (int, error) {
	if l.rc == nil {
		return 0, io.EOF
	}
	n, err := l.rc.Read(p)
	if n > 0 {
		l.capture(p[:n])
	}
	return n, err
}

func (l *limitedCaptureReadCloser) Close() error {
	if l.rc == nil {
		return nil
	}
	return l.rc.Close()
}

func (l *limitedCaptureReadCloser) capture(p []byte) {
	if l.limit <= 0 || l.truncated || len(p) == 0 {
		if l.limit <= 0 {
			l.truncated = true
		}
		return
	}
	remaining := l.limit - l.buf.Len()
	if remaining <= 0 {
		l.truncated = true
		return
	}
	if len(p) > remaining {
		_, _ = l.buf.Write(p[:remaining])
		l.truncated = true
		return
	}
	_, _ = l.buf.Write(p)
}

func (l *limitedCaptureReadCloser) Captured() []byte {
	return l.buf.Bytes()
}

func (l *limitedCaptureReadCloser) Truncated() bool {
	return l.truncated
}

type trafficRecorder struct {
	http.ResponseWriter
	status            int
	responseBody      []byte
	responseBodyLimit int
	bodyTruncated     bool
	accountID         string
	accountEmail      string
}

func (r *trafficRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *trafficRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.responseBodyLimit <= 0 {
		r.responseBody = append(r.responseBody, p...)
	} else if !r.bodyTruncated {
		remaining := r.responseBodyLimit - len(r.responseBody)
		if remaining > 0 {
			if len(p) <= remaining {
				r.responseBody = append(r.responseBody, p...)
			} else {
				r.responseBody = append(r.responseBody, p[:remaining]...)
				r.bodyTruncated = true
			}
		} else {
			r.bodyTruncated = true
		}
	}
	return r.ResponseWriter.Write(p)
}

func (r *trafficRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func detectTrafficModelAndStream(path string, body []byte) (string, bool) {
	switch strings.TrimSpace(path) {
	case "/v1":
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err == nil {
			model, _ := anyBody["model"].(string)
			stream, _ := anyBody["stream"].(bool)
			return strings.TrimSpace(model), stream
		}
	case "/zo/v1":
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err == nil {
			model, _ := anyBody["model"].(string)
			stream, _ := anyBody["stream"].(bool)
			return strings.TrimSpace(model), stream
		}
	case "/v1/chat/completions":
		var req ChatCompletionsRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/zo/v1/chat/completions":
		var req ChatCompletionsRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/responses":
		var req ResponsesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/zo/v1/responses":
		var req ResponsesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/messages", "/claude/v1/messages":
		var req ClaudeMessagesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/zo/v1/messages":
		var req ClaudeMessagesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	}
	return "", false
}

func parseTrafficUsageTokens(body string) (int, int, int) {
	body = strings.TrimSpace(body)
	if body == "" {
		return 0, 0, 0
	}
	if strings.Contains(body, "data:") {
		return parseTrafficUsageTokensFromSSE(body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return 0, 0, 0
	}
	return extractTrafficUsageTokens(payload)
}

func parseTrafficUsageTokensFromSSE(body string) (int, int, int) {
	lines := strings.Split(body, "\n")
	requestTokens := 0
	responseTokens := 0
	totalTokens := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue
		}
		req, resp, total := extractTrafficUsageTokens(payload)
		if req != 0 || resp != 0 || total != 0 {
			requestTokens = req
			responseTokens = resp
			totalTokens = total
		}
	}
	return requestTokens, responseTokens, totalTokens
}

func extractTrafficUsageTokens(payload map[string]any) (int, int, int) {
	if usage, ok := payload["usage"].(map[string]any); ok {
		return normalizeTrafficUsageTokens(usage)
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if usage, ok := response["usage"].(map[string]any); ok {
			return normalizeTrafficUsageTokens(usage)
		}
	}
	if message, ok := payload["message"].(map[string]any); ok {
		if usage, ok := message["usage"].(map[string]any); ok {
			return normalizeTrafficUsageTokens(usage)
		}
	}
	return 0, 0, 0
}

func normalizeTrafficUsageTokens(usage map[string]any) (int, int, int) {
	requestTokens := intFromAny(usage["prompt_tokens"])
	responseTokens := intFromAny(usage["completion_tokens"])
	if requestTokens == 0 {
		requestTokens = intFromAny(usage["input_tokens"])
	}
	if responseTokens == 0 {
		responseTokens = intFromAny(usage["output_tokens"])
	}
	totalTokens := intFromAny(usage["total_tokens"])
	if totalTokens == 0 && (requestTokens != 0 || responseTokens != 0) {
		totalTokens = requestTokens + responseTokens
	}
	return requestTokens, responseTokens, totalTokens
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

func truncateForLog(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
