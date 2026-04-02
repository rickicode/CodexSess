package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
)

func normalizePublicLane(lane string) string {
	switch strings.TrimSpace(strings.ToLower(lane)) {
	case "executor":
		return "executor"
	case "chat", "user", "assistant":
		return "chat"
	default:
		return ""
	}
}

func codingMessageProjectionLane(item store.CodingMessage) string {
	if lane := normalizePublicLane(item.Actor); lane != "" {
		return lane
	}
	role := strings.TrimSpace(strings.ToLower(item.Role))
	switch role {
	case "user", "assistant":
		return "chat"
	case "exec", "subagent", "activity", "event", "stderr":
		return ""
	default:
		return ""
	}
}

func (s *Server) handleWebCodingWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(req *http.Request) bool {
			return wsOriginAllowed(req)
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	connClosed := atomic.Bool{}
	var writeMu sync.Mutex
	writeJSON := func(payload map[string]any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if connClosed.Load() {
			return websocket.ErrCloseSent
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := conn.WriteJSON(payload)
		if err != nil {
			connClosed.Store(true)
		}
		return err
	}

	type codingWSRequest struct {
		Type             string `json:"type"`
		RequestID        string `json:"request_id"`
		SessionID        string `json:"session_id"`
		Lane             string `json:"lane"`
		Content          string `json:"content"`
		Model            string `json:"model"`
		ReasoningLevel   string `json:"reasoning_level"`
		WorkDir          string `json:"work_dir"`
		SandboxMode      string `json:"sandbox_mode"`
		Command          string `json:"command"`
		Force            bool   `json:"force"`
		LastSeenEventSeq int64  `json:"last_seen_event_seq"`
	}

	nextEventSeq := func(sessionID string) int64 {
		sid := strings.TrimSpace(sessionID)
		if sid == "" {
			return time.Now().UTC().UnixNano()
		}
		seq, err := s.svc.Store.NextCodingSessionEventSeq(context.Background(), sid)
		if err != nil {
			if session, getErr := s.svc.Store.GetCodingSession(context.Background(), sid); getErr == nil && session.LastAppliedEventSeq > 0 {
				return session.LastAppliedEventSeq
			}
			return time.Now().UTC().UnixNano()
		}
		return seq
	}
	emit := func(sessionID, eventType string, data map[string]any) error {
		seq := nextEventSeq(sessionID)
		payload := map[string]any{
			"event":      eventType,
			"event_type": eventType,
			"event_id":   "evt_" + strings.TrimSpace(sessionID) + "_" + fmt.Sprintf("%d", seq),
			"event_seq":  seq,
			"session_id": strings.TrimSpace(sessionID),
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"payload":    data,
		}
		for k, v := range data {
			payload[k] = v
		}
		return writeJSON(payload)
	}
	isCodingWSDisconnectError := func(err error) bool {
		if err == nil {
			return false
		}
		if errors.Is(err, websocket.ErrCloseSent) {
			return true
		}
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
			return true
		}
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if msg == "" {
			return false
		}
		return strings.Contains(msg, "websocket: close sent") ||
			strings.Contains(msg, "broken pipe") ||
			strings.Contains(msg, "connection reset by peer") ||
			strings.Contains(msg, "use of closed network connection")
	}
	replayFrom := func(sessionID string, lastSeen int64) {
		sid := strings.TrimSpace(sessionID)
		if sid == "" {
			return
		}
		session, err := s.svc.Store.GetCodingSession(context.Background(), sid)
		if err != nil {
			return
		}
		if session.LastAppliedEventSeq <= lastSeen {
			return
		}
		messages := []map[string]any{}
		compactRows, _, _, compactErr := s.loadCompactCodingMessagesPage(context.Background(), sid, "compact", 1000, "")
		if compactErr == nil && len(compactRows) > 0 {
			messages = compactRows
		} else {
			rawMessages, err := s.svc.Store.ListCodingMessages(context.Background(), sid)
			if err != nil {
				return
			}
			messages = mapCodingEventMessages(rawMessages)
		}
		_ = emit(sid, "session.snapshot", map[string]any{
			"session":            mapCodingSession(session),
			"messages":           messages,
			"last_event_seq":     session.LastAppliedEventSeq,
			"replay_from_seq":    lastSeen,
			"replay_until_seq":   session.LastAppliedEventSeq,
			"replay_message_ct":  len(messages),
			"snapshot_generated": time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	defaultLaneForSession := func(session store.CodingSession) string {
		return "chat"
	}
	laneAllowedForSession := func(session store.CodingSession, lane string) bool {
		l := strings.TrimSpace(strings.ToLower(lane))
		if l == "" {
			return true
		}
		return l == "chat"
	}
	persistSessionErrorMessage := func(sessionID, actor, message string, occurredAt time.Time) {
		sid := strings.TrimSpace(sessionID)
		laneActor := normalizePublicLane(actor)
		content := strings.TrimSpace(message)
		if sid == "" || laneActor == "" || content == "" {
			return
		}
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		content = "Run failed: " + content
		if _, err := s.svc.Store.AppendCodingMessage(context.Background(), store.CodingMessage{
			ID:        "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SessionID: sid,
			Role:      "assistant",
			Actor:     laneActor,
			Content:   content,
			CreatedAt: occurredAt.UTC(),
		}); err != nil {
			rawLen, rawHash := safeTextDiagnostics(err.Error())
			log.Printf("[coding-ws] session.error persist session_id=%s actor=%s raw_len=%d raw_sha=%s", sid, laneActor, rawLen, rawHash)
		}
	}
	emitSessionError := func(sessionID, requestID, actor, code, category, rawMessage string, retryable bool, details map[string]any) {
		errorCode, normalizedCategory, safeMessage, safeRetryable, actions := sanitizeCodingErrorPolicy(code, category, rawMessage, retryable)
		if strings.TrimSpace(rawMessage) != "" {
			rawLen, rawHash := safeTextDiagnostics(rawMessage)
			log.Printf("[coding-ws] session.error session_id=%s request_id=%s code=%s category=%s raw_len=%d raw_sha=%s", strings.TrimSpace(sessionID), strings.TrimSpace(requestID), errorCode, normalizedCategory, rawLen, rawHash)
		}
		payload := map[string]any{
			"request_id":        requestID,
			"actor":             strings.TrimSpace(actor),
			"category":          normalizedCategory,
			"message":           safeMessage,
			"error_code":        errorCode,
			"retryable":         safeRetryable,
			"suggested_actions": actions,
			"draft_preserved":   true,
			"occurred_at":       time.Now().UTC().Format(time.RFC3339Nano),
			"error": map[string]any{
				"code":      errorCode,
				"category":  normalizedCategory,
				"message":   safeMessage,
				"retryable": safeRetryable,
			},
		}
		if len(details) > 0 {
			payload["error_details"] = details
		}
		targetLane := normalizePublicLane(actor)
		if targetLane == "" {
			if session, err := s.svc.Store.GetCodingSession(context.Background(), strings.TrimSpace(sessionID)); err == nil {
				targetLane = defaultLaneForSession(session)
			}
		}
		persistSessionErrorMessage(sessionID, targetLane, safeMessage, time.Now().UTC())
		_ = emit(sessionID, "session.error", payload)
	}
	inFlight := atomic.Bool{}
	for {
		var req codingWSRequest
		if err := conn.ReadJSON(&req); err != nil {
			connClosed.Store(true)
			return
		}
		reqType := strings.ToLower(strings.TrimSpace(req.Type))
		reqID := strings.TrimSpace(req.RequestID)
		sessionID := strings.TrimSpace(req.SessionID)
		stateChanging := reqType == "session.send" || reqType == "session.stop"
		if stateChanging {
			if sessionID == "" {
				emitSessionError(sessionID, reqID, "none", "bad_request", "unknown_runtime_error", "session_id is required", false, nil)
				continue
			}
			if reqID == "" {
				emitSessionError(sessionID, reqID, "none", "bad_request", "unknown_runtime_error", "request_id is required for state-changing commands", false, nil)
				continue
			}
			claimed, err := s.svc.Store.ClaimCodingWSRequestID(context.Background(), sessionID, reqID, 24*time.Hour)
			if err != nil {
				emitSessionError(sessionID, reqID, "none", "idempotency_unavailable", "runtime_unavailable", "idempotency persistence failed", true, nil)
				continue
			}
			if !claimed {
				_ = emit(sessionID, "session.duplicate_request", map[string]any{"request_id": reqID})
				continue
			}
		}
		switch reqType {
		case "ping":
			_ = emit(req.SessionID, "pong", map[string]any{"request_id": reqID})
		case "session.stop":
			if sessionID == "" {
				emitSessionError(sessionID, reqID, "none", "bad_request", "unknown_runtime_error", "session_id is required", false, nil)
				continue
			}
			stopped := s.svc.StopCodingRun(sessionID, req.Force)
			if !stopped {
				code := "not_running"
				message := "session not running"
				fallbackActor := "chat"
				emitSessionError(sessionID, reqID, fallbackActor, code, "runtime_unavailable", message, false, nil)
				continue
			}
			_ = emit(sessionID, "session.stopped", map[string]any{
				"request_id": reqID,
				"force":      req.Force,
			})
		case "session.send":
			if inFlight.Load() {
				emitSessionError(req.SessionID, reqID, "none", "runtime_busy", "session_busy", service.ErrCodingSessionBusy.Error(), true, nil)
				continue
			}
			if sessionID == "" {
				emitSessionError(sessionID, reqID, "none", "bad_request", "unknown_runtime_error", "session_id is required", false, nil)
				continue
			}
			session, err := s.svc.Store.GetCodingSession(context.Background(), sessionID)
			if err != nil {
				emitSessionError(sessionID, reqID, "none", "runtime_unavailable", "runtime_unavailable", err.Error(), true, nil)
				continue
			}
			lane := strings.TrimSpace(strings.ToLower(firstNonEmpty(req.Lane, defaultLaneForSession(session))))
			if !laneAllowedForSession(session, lane) {
				emitSessionError(sessionID, reqID, lane, "invalid_lane_for_session", "unknown_runtime_error", "selected lane is not writable for this session", false, map[string]any{"lane": lane})
				continue
			}
			if req.LastSeenEventSeq > 0 {
				replayFrom(sessionID, req.LastSeenEventSeq)
			}
			inFlight.Store(true)
			_ = emit(sessionID, "session.started", map[string]any{"request_id": reqID})
			go func(payload codingWSRequest, activeLane string) {
				clearedInFlight := false
				clearInFlight := func() {
					if clearedInFlight {
						return
					}
					inFlight.Store(false)
					clearedInFlight = true
				}
				defer clearInFlight()
				bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
				defer cancel()
				finalLaneForActor := func(actor string) string {
					if lane := normalizePublicLane(actor); lane != "" {
						return lane
					}
					return activeLane
				}
				result, runErr := s.svc.SendCodingMessageStream(
					bgCtx,
					payload.SessionID,
					payload.Content,
					payload.Model,
					payload.ReasoningLevel,
					payload.WorkDir,
					payload.SandboxMode,
					"chat",
					func(evt provider.ChatEvent) error {
						streamType := strings.TrimSpace(strings.ToLower(evt.Type))
						rawPayload := ""
						if streamType == "raw_event" && strings.TrimSpace(evt.Text) != "" {
							rawPayload = evt.Text
						}
						clientText := evt.Text
						streamActor := normalizePublicLane(evt.Actor)
						if streamActor == "" {
							_, _, runtimeActor := s.svc.CodingRunStatus(payload.SessionID)
							streamActor = normalizePublicLane(runtimeActor)
						}
						if streamActor == "" {
							streamActor = normalizePublicLane(activeLane)
						}
						streamPayload := map[string]any{
							"event_type":  "session.stream",
							"stream_type": evt.Type,
							"text":        clientText,
							"actor":       streamActor,
						}
						streamLane := streamActor
						if streamLane == "" {
							streamLane = activeLane
						}
						streamPayload["lane"] = streamLane
						clientStreamPayload := map[string]any{}
						for key, value := range streamPayload {
							clientStreamPayload[key] = value
						}
						if rawPayload != "" {
							clientStreamPayload["raw_payload"] = rawPayload
							clientStreamPayload["text"] = ""
						}
						if err := emit(payload.SessionID, "session.stream", clientStreamPayload); err != nil {
							if isCodingWSDisconnectError(err) {
								return nil
							}
							return err
						}
						return nil
					},
				)
				if runErr != nil {
					errorCode, category, retryable := codingErrorMetaFromErr(runErr, "unknown_runtime_error", "unknown_runtime_error", false)
					_, _, runtimeActor := s.svc.CodingRunStatus(payload.SessionID)
					clearInFlight()
					emitSessionError(payload.SessionID, payload.RequestID, runtimeActor, errorCode, category, runErr.Error(), retryable, nil)
					return
				}
				finalActor := strings.TrimSpace(result.Assistant.Actor)
				finalLane := finalLaneForActor(finalActor)
				clearInFlight()
				_ = emit(payload.SessionID, "session.done", map[string]any{
					"request_id":         payload.RequestID,
					"session":            mapCodingSession(result.Session),
					"user":               mapCodingMessage(result.User),
					"assistant":          mapCodingMessage(result.Assistant),
					"event_messages":     mapCodingEventMessages(result.EventMessages),
					"assistant_messages": mapCodingMessages(result.Assistants),
					"actor":              finalActor,
					"lane":               finalLane,
				})
			}(req, lane)
		default:
			emitSessionError(req.SessionID, reqID, "none", "bad_request", "unknown_runtime_error", "unsupported request type", false, nil)
		}
	}
}

func wsOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil || strings.TrimSpace(originURL.Host) == "" {
		return false
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return false
	}
	host = strings.TrimSpace(strings.Split(host, ",")[0])
	if strings.EqualFold(originURL.Host, host) {
		return true
	}
	originHost := strings.ToLower(strings.TrimSpace(originURL.Hostname()))
	requestHost := strings.ToLower(strings.TrimSpace(host))
	if parsedHost, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = strings.ToLower(strings.TrimSpace(parsedHost))
	}
	if originHost != "" && requestHost != "" && originHost == requestHost {
		switch originHost {
		case "127.0.0.1", "localhost", "::1":
			return true
		}
	}
	return false
}

func (s *Server) handleWebCodingChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		SessionID      string `json:"session_id"`
		Content        string `json:"content"`
		Model          string `json:"model"`
		ReasoningLevel string `json:"reasoning_level"`
		WorkDir        string `json:"work_dir"`
		SandboxMode    string `json:"sandbox_mode"`
		Command        string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	if _, getErr := s.svc.Store.GetCodingSession(r.Context(), sessionID); getErr != nil {
		respondSanitizedCodingError(w, 400, "runtime_unavailable", "runtime_unavailable", getErr, true)
		return
	}
	result, err := s.svc.SendCodingMessage(r.Context(), req.SessionID, req.Content, req.Model, req.ReasoningLevel, req.WorkDir, req.SandboxMode, "chat")
	if err != nil {
		if errors.Is(err, service.ErrCodingSessionBusy) {
			respondSanitizedCodingError(w, 409, "runtime_busy", "session_busy", err, true)
			return
		}
		errorCode, category, retryable := codingErrorMetaFromErr(err, "unknown_runtime_error", "unknown_runtime_error", false)
		statusCode := 400
		if normalizeCodingErrorCategory(category, errorCode) == "session_busy" {
			statusCode = 409
		}
		respondSanitizedCodingError(w, statusCode, errorCode, category, err, retryable)
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok":                 true,
		"session":            mapCodingSession(result.Session),
		"user":               mapCodingMessage(result.User),
		"assistant":          mapCodingMessage(result.Assistant),
		"event_messages":     mapCodingEventMessages(result.EventMessages),
		"assistant_messages": mapCodingMessages(result.Assistants),
	})
}

func (s *Server) handleWebCodingRuntimeRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Force     bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	deferred, err := s.svc.RestartCodingRuntime(r.Context(), req.SessionID, req.Force)
	if err != nil {
		respondSanitizedCodingError(w, 400, "runtime_unavailable", "runtime_unavailable", err, true)
		return
	}
	runtime, _ := s.svc.CodingRuntimeStatusDetail(r.Context(), req.SessionID)
	respondJSON(w, 200, map[string]any{
		"ok":         true,
		"accepted":   true,
		"deferred":   deferred,
		"session_id": strings.TrimSpace(req.SessionID),
		"in_flight":  runtime.InFlight,
	})
}

func (s *Server) handleWebCodingPathSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("prefix"))
	if raw == "" {
		raw = "~/"
	}
	expanded := expandSuggestionPath(raw)
	parent := expanded
	if !strings.HasSuffix(raw, "/") {
		parent = filepath.Dir(expanded)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		respondJSON(w, 200, map[string]any{"ok": true, "suggestions": []string{raw}})
		return
	}
	out := make([]string, 0, len(entries)+1)
	needle := strings.ToLower(strings.TrimSpace(filepath.Base(expanded)))
	prefixRoot := raw
	if !strings.HasSuffix(prefixRoot, "/") {
		prefixRoot = filepath.Dir(raw)
	}
	prefixRoot = strings.TrimSuffix(prefixRoot, "/")
	if prefixRoot == "." {
		prefixRoot = ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if needle != "" && !strings.HasPrefix(strings.ToLower(name), needle) {
			continue
		}
		var suggestion string
		switch {
		case strings.HasPrefix(raw, "~/"):
			base := strings.TrimPrefix(prefixRoot, "~")
			suggestion = filepath.ToSlash(filepath.Join("~", base, name)) + "/"
		case strings.HasPrefix(raw, "/"):
			suggestion = filepath.ToSlash(filepath.Join(prefixRoot, name)) + "/"
		default:
			if prefixRoot == "" {
				suggestion = name + "/"
			} else {
				suggestion = filepath.ToSlash(filepath.Join(prefixRoot, name)) + "/"
			}
		}
		out = append(out, suggestion)
	}
	if len(out) == 0 {
		out = append(out, raw)
	}
	sort.Strings(out)
	respondJSON(w, 200, map[string]any{"ok": true, "suggestions": out})
}

func (s *Server) handleWebCodingSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	home, _ := os.UserHomeDir()
	searchRoots := []string{
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(".", ".codex", "skills"),
	}
	searchRoots = append(searchRoots, additionalSkillRootsFromEnv()...)
	seen := map[string]struct{}{}
	out := make([]string, 0, 64)
	for _, root := range searchRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		collectSkillNames(root, seen, &out)
	}
	sort.Strings(out)
	respondJSON(w, 200, map[string]any{
		"ok":     true,
		"skills": out,
	})
}

func collectSkillNames(root string, seen map[string]struct{}, out *[]string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		skillPath := filepath.Join(root, name, "SKILL.md")
		if _, err := os.Stat(skillPath); err == nil {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			*out = append(*out, name)
			continue
		}
		childRoot := filepath.Join(root, name)
		children, err := os.ReadDir(childRoot)
		if err != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			childName := strings.TrimSpace(child.Name())
			if childName == "" {
				continue
			}
			childSkillPath := filepath.Join(childRoot, childName, "SKILL.md")
			if _, err := os.Stat(childSkillPath); err != nil {
				continue
			}
			if _, ok := seen[childName]; ok {
				continue
			}
			seen[childName] = struct{}{}
			*out = append(*out, childName)
		}
	}
}

func additionalSkillRootsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_SKILL_DIRS"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		out = append(out, expandSuggestionPath(p))
	}
	return out
}

func codingRawErrorLooksModelCapacity(raw string) bool {
	msg := strings.ToLower(strings.TrimSpace(raw))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "selected model is at capacity") ||
		strings.Contains(msg, "model is at capacity") ||
		strings.Contains(msg, "model capacity") ||
		strings.Contains(msg, "model_capacity")
}

func codingRawErrorLooksUsageLimit(raw string) bool {
	msg := strings.ToLower(strings.TrimSpace(raw))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "insufficient_quota") ||
		strings.Contains(msg, "usage limit") ||
		strings.Contains(msg, "quota exhausted") ||
		strings.Contains(msg, "quota exceeded") ||
		(strings.Contains(msg, "rate limited") && strings.Contains(msg, "codex")) ||
		strings.Contains(msg, "too many requests")
}

func codingRawErrorLooksAuthFailure(raw string) bool {
	msg := strings.ToLower(strings.TrimSpace(raw))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "account deactivated") ||
		strings.Contains(msg, "account_deactivated") ||
		strings.Contains(msg, "auth failed") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "unexpected status 401")
}

func mapCodingSessions(items []store.CodingSession) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mapCodingSession(item))
	}
	return out
}

func mapCodingSession(item store.CodingSession) map[string]any {
	return map[string]any{
		"id":                     item.ID,
		"thread_id":              strings.TrimSpace(item.CodexThreadID),
		"title":                  item.Title,
		"model":                  item.Model,
		"reasoning_level":        item.ReasoningLevel,
		"work_dir":               item.WorkDir,
		"sandbox_mode":           item.SandboxMode,
		"last_applied_event_seq": item.LastAppliedEventSeq,
		"created_at":             item.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":             item.UpdatedAt.UTC().Format(time.RFC3339),
		"last_message_at":        item.LastMessageAt.UTC().Format(time.RFC3339),
	}
}
