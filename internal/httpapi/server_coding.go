package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleWebCodingMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			respondErr(w, 400, "bad_request", "limit must be an integer")
			return
		}
		if v < 1 {
			v = 1
		}
		if v > 200 {
			v = 200
		}
		limit = v
	}
	beforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	viewMode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("view")))
	if viewMode != "" && viewMode != "compact" {
		respondErr(w, 400, "bad_request", "view must be compact")
		return
	}
	rows, hasMore, source, err := s.loadCompactCodingMessagesPage(r.Context(), sessionID, "compact", limit, beforeID)
	if err != nil {
		respondSanitizedCodingError(w, 400, "runtime_unavailable", "runtime_unavailable", err, true)
		return
	}
	rows = sanitizeCompactViewRows(rows)
	if canonical, err := s.buildCanonicalCompactRowsFromRawHistory(r.Context(), sessionID); err == nil {
		if compactSnapshotNeedsCanonicalRebuild(rows) || compactSnapshotIsIncomplete(rows, canonical) {
			rows = canonical
			_ = s.persistCompactCodingView(r.Context(), sessionID, "compact", canonical)
		}
	}
	oldestID := ""
	newestID := ""
	if len(rows) > 0 {
		oldestID = codingSnapshotMessageID(rows[0])
		newestID = codingSnapshotMessageID(rows[len(rows)-1])
	}
	respondJSON(w, 200, map[string]any{
		"ok":        true,
		"messages":  rows,
		"has_more":  hasMore,
		"oldest_id": oldestID,
		"newest_id": newestID,
		"source":    source,
	})
}

func (s *Server) persistCompactCodingView(ctx context.Context, sessionID, viewMode string, rows []map[string]any) error {
	sanitized := sanitizeCompactViewRows(rows)
	if err := s.svc.Store.ReplaceCodingViewMessages(ctx, sessionID, viewMode, sanitized); err != nil {
		return err
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return err
	}
	return s.svc.Store.UpsertCodingMessageSnapshot(ctx, sessionID, viewMode, string(encoded))
}

func (s *Server) rebuildCompactCodingViewFromRawHistory(ctx context.Context, sessionID, viewMode string) ([]map[string]any, error) {
	snapshot, err := s.buildCanonicalCompactRowsFromRawHistory(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(snapshot) == 0 {
		return []map[string]any{}, nil
	}
	if err := s.persistCompactCodingView(ctx, sessionID, viewMode, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *Server) buildCanonicalCompactRowsFromRawHistory(ctx context.Context, sessionID string) ([]map[string]any, error) {
	history, err := s.svc.Store.ListCodingMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	builder := newCodingCompactBuilder()
	builder.SeedFromRawMessages(history)
	return sanitizeCompactViewRows(builder.Snapshot()), nil
}

func (s *Server) loadCompactCodingMessagesPage(ctx context.Context, sessionID, viewMode string, limit int, beforeID string) ([]map[string]any, bool, string, error) {
	if rows, hasMore, err := s.svc.Store.ListCodingViewMessagesPage(ctx, sessionID, viewMode, limit, beforeID); err != nil {
		return nil, false, "", err
	} else if len(rows) > 0 {
		if session, sessionErr := s.svc.Store.GetCodingSession(ctx, sessionID); sessionErr == nil && compactRowsAreStale(rows, session.LastMessageAt) {
			_ = s.persistCompactCodingView(ctx, sessionID, viewMode, []map[string]any{})
		} else {
			if fullRows, fullErr := s.svc.Store.ListCodingViewMessages(ctx, sessionID, viewMode); fullErr == nil && compactRowsNeedSanitization(fullRows) {
				sanitized := sanitizeCompactViewRows(fullRows)
				if persistErr := s.persistCompactCodingView(ctx, sessionID, viewMode, sanitized); persistErr == nil {
					if refreshed, refreshedHasMore, refreshedErr := s.svc.Store.ListCodingViewMessagesPage(ctx, sessionID, viewMode, limit, beforeID); refreshedErr == nil {
						return refreshed, refreshedHasMore, "canonical", nil
					}
				}
			}
			return rows, hasMore, "canonical", nil
		}
	}

	if payload, ok, err := s.svc.Store.GetCodingMessageSnapshot(ctx, sessionID, viewMode); err != nil {
		return nil, false, "", err
	} else if ok {
		var snapshot []map[string]any
		if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
			return nil, false, "", fmt.Errorf("stored coding snapshot is invalid")
		}
		snapshot = sanitizeCompactViewRows(snapshot)
		_ = s.persistCompactCodingView(ctx, sessionID, viewMode, snapshot)
		if len(snapshot) > 0 {
			if rows, hasMore, err := s.svc.Store.ListCodingViewMessagesPage(ctx, sessionID, viewMode, limit, beforeID); err == nil && len(rows) > 0 {
				return rows, hasMore, "canonical", nil
			}
		}
	}

	snapshot, err := s.rebuildCompactCodingViewFromRawHistory(ctx, sessionID, viewMode)
	if err != nil {
		return nil, false, "", err
	}
	if len(snapshot) == 0 {
		return []map[string]any{}, false, "canonical", nil
	}
	if rows, hasMore, err := s.svc.Store.ListCodingViewMessagesPage(ctx, sessionID, viewMode, limit, beforeID); err == nil && len(rows) > 0 {
		return rows, hasMore, "canonical", nil
	}
	page, hasMore := paginateCodingSnapshot(snapshot, limit, beforeID)
	return page, hasMore, "canonical", nil
}

func compactRowsAreStale(rows []map[string]any, lastMessageAt time.Time) bool {
	if len(rows) == 0 || lastMessageAt.IsZero() {
		return false
	}
	newest := time.Time{}
	for _, row := range rows {
		candidate := parseCompactRowTime(row)
		if candidate.After(newest) {
			newest = candidate
		}
	}
	if newest.IsZero() {
		return false
	}
	return newest.Before(lastMessageAt.UTC())
}

func compactSnapshotIsIncomplete(clientRows, canonicalRows []map[string]any) bool {
	if len(canonicalRows) == 0 {
		return false
	}
	if len(clientRows) < len(canonicalRows) {
		return true
	}
	clientIDs := map[string]struct{}{}
	for _, row := range clientRows {
		if id := strings.TrimSpace(codingSnapshotMessageID(row)); id != "" {
			clientIDs[id] = struct{}{}
		}
	}
	for _, row := range canonicalRows {
		id := strings.TrimSpace(codingSnapshotMessageID(row))
		if id == "" {
			continue
		}
		if _, ok := clientIDs[id]; !ok {
			return true
		}
	}
	return false
}

func compactSnapshotNeedsCanonicalRebuild(rows []map[string]any) bool {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(strings.ToLower(stringFromAny(row["role"]))) != "activity" {
			continue
		}
		content := strings.TrimSpace(strings.ToLower(stringFromAny(row["content"])))
		switch {
		case strings.HasPrefix(content, "item/started:"),
			strings.HasPrefix(content, "item/completed:"),
			strings.HasPrefix(content, "rawresponseitem/completed:"),
			strings.HasPrefix(content, "thread/tokenusage/updated:"):
			return true
		}
	}
	return false
}

func parseCompactRowTime(row map[string]any) time.Time {
	for _, key := range []string{"updated_at", "created_at"} {
		raw := strings.TrimSpace(fmt.Sprintf("%v", row[key]))
		if raw == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func (s *Server) handleWebCodingMessageSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		SessionID string          `json:"session_id"`
		ViewMode  string          `json:"view_mode"`
		Messages  json.RawMessage `json:"messages"`
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
	viewMode := strings.TrimSpace(strings.ToLower(req.ViewMode))
	if viewMode == "" {
		viewMode = "compact"
	}
	if viewMode != "compact" {
		respondErr(w, 400, "bad_request", "view_mode must be compact")
		return
	}
	payload := strings.TrimSpace(string(req.Messages))
	if payload == "" {
		payload = "[]"
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		respondErr(w, 400, "bad_request", "messages must be a JSON array of objects")
		return
	}
	if len(payload) > 4*1024*1024 {
		respondErr(w, 400, "bad_request", "snapshot too large")
		return
	}
	decoded = sanitizeCompactViewRows(decoded)
	if canonical, err := s.buildCanonicalCompactRowsFromRawHistory(r.Context(), sessionID); err == nil {
		if compactSnapshotNeedsCanonicalRebuild(decoded) || compactSnapshotIsIncomplete(decoded, canonical) {
			decoded = canonical
		}
	}
	if err := s.persistCompactCodingView(r.Context(), sessionID, viewMode, decoded); err != nil {
		respondSanitizedCodingError(w, 500, "runtime_unavailable", "runtime_unavailable", err, true)
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true})
}

func codingSnapshotMessageID(item map[string]any) string {
	return strings.TrimSpace(fmt.Sprintf("%v", item["id"]))
}

func paginateCodingSnapshot(messages []map[string]any, limit int, beforeID string) ([]map[string]any, bool) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if len(messages) == 0 {
		return []map[string]any{}, false
	}
	cursorID := strings.TrimSpace(beforeID)
	end := len(messages)
	if cursorID != "" {
		end = -1
		for idx, item := range messages {
			if codingSnapshotMessageID(item) == cursorID {
				end = idx
				break
			}
		}
		if end < 0 {
			return []map[string]any{}, false
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	out := append([]map[string]any(nil), messages[start:end]...)
	return out, start > 0
}

func (s *Server) handleWebCodingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	runtime, err := s.svc.CodingRuntimeStatusDetail(r.Context(), sessionID)
	if err != nil {
		respondSanitizedCodingError(w, 400, "runtime_unavailable", "runtime_unavailable", err, true)
		return
	}
	startedAtValue := ""
	if runtime.InFlight {
		startedAtValue = runtime.StartedAt.UTC().Format(time.RFC3339)
	}
	respondJSON(w, 200, map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"in_flight":  runtime.InFlight,
		"started_at": startedAtValue,
	})
}

func (s *Server) handleWebCodingRuntimeDebug(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	debug, err := s.svc.CodingRuntimeDebugSnapshot(r.Context(), sessionID)
	if err != nil {
		respondSanitizedCodingError(w, 400, "runtime_unavailable", "runtime_unavailable", err, true)
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok":      true,
		"session": mapCodingRuntimeDebugSnapshot(debug),
	})
}

func mapCodingRuntimeDebugSnapshot(debug service.CodingRuntimeDebugSnapshot) map[string]any {
	return map[string]any{
		"session_id":      strings.TrimSpace(debug.SessionID),
		"thread_id":       strings.TrimSpace(debug.ThreadID),
		"restart_pending": debug.RestartPending,
		"in_flight":       debug.InFlight,
		"runner_role":     strings.TrimSpace(debug.RunnerRole),
		"roles":           debug.Roles,
	}
}

func (s *Server) handleWebCodingStop(w http.ResponseWriter, r *http.Request) {
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
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	stopped := s.svc.StopCodingRun(sessionID, req.Force)
	respondJSON(w, 200, map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"stopped":    stopped,
		"force":      req.Force,
	})
}

func mapCodingMessages(items []store.CodingMessage) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mapCodingMessage(item))
	}
	return out
}

func mapCodingEventMessages(items []store.CodingMessage) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mapCodingMessage(item))
	}
	return out
}

func mapCodingMessage(item store.CodingMessage) map[string]any {
	lane := codingMessageProjectionLane(item)
	return map[string]any{
		"id":            item.ID,
		"session_id":    item.SessionID,
		"role":          item.Role,
		"actor":         item.Actor,
		"lane":          lane,
		"account_email": item.AccountEmail,
		"content":       item.Content,
		"input_tokens":  item.InputTokens,
		"output_tokens": item.OutputTokens,
		"created_at":    item.CreatedAt.UTC().Format(time.RFC3339),
		"sequence":      item.Sequence,
	}
}

func sanitizeCompactViewRows(rows []map[string]any) []map[string]any {
	if len(rows) == 0 {
		return rows
	}
	out := make([]map[string]any, 0, len(rows))
	for _, item := range rows {
		if item == nil {
			continue
		}
		clone := make(map[string]any, len(item))
		for k, v := range item {
			clone[k] = v
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(clone["role"])))
		if role == "exec" && strings.TrimSpace(stringFromAny(clone["exec_output"])) != "" {
			clone["exec_output"] = codingCompactRedactedText
			clone["redacted"] = true
		}
		delete(clone, "subagent_raw")
		out = append(out, clone)
	}
	return out
}

func compactRowsNeedSanitization(rows []map[string]any) bool {
	for _, row := range rows {
		if row == nil {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(row["role"])))
		if role == "exec" {
			output := strings.TrimSpace(stringFromAny(row["exec_output"]))
			if output != "" && output != codingCompactRedactedText {
				return true
			}
		}
		if _, exists := row["subagent_raw"]; exists {
			return true
		}
	}
	return false
}
