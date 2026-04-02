package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/ricki/codexsess/internal/service"
)

func normalizeCodingErrorCategory(category, code string) string {
	rawCategory := strings.TrimSpace(strings.ToLower(category))
	rawCode := strings.TrimSpace(strings.ToLower(code))
	switch rawCode {
	case "session_busy", "runtime_busy":
		return "session_busy"
	case "idempotency_unavailable", "runtime_unavailable":
		return "runtime_unavailable"
	case "artifact_version_conflict":
		return "runtime_unavailable"
	case "account_switch_failed", "continue_failed", "same_session_continue_not_supported":
		return "model_capacity"
	case "usage_account_switch_failed", "usage_continue_failed":
		return "usage_limit"
	}
	switch rawCategory {
	case "model_capacity", "usage_limit", "rate_limited", "auth_failed", "runtime_unavailable", "session_busy", "unknown_runtime_error":
		return rawCategory
	default:
		return "unknown_runtime_error"
	}
}

func codingSuggestedActionsForError(code, category string) []string {
	switch normalizeCodingErrorCategory(category, code) {
	case "model_capacity":
		return []string{"retry", "change_model", "switch_to_chat"}
	case "session_busy":
		return []string{"wait_and_retry", "stop_active_worker"}
	case "runtime_unavailable":
		return []string{"retry", "check_runtime_health"}
	default:
		return []string{"retry", "adjust_request"}
	}
}

func sanitizeCodingErrorPolicy(code, category, rawMessage string, retryable bool) (string, string, string, bool, []string) {
	rawCode := strings.TrimSpace(strings.ToLower(code))
	normalizedCategory := normalizeCodingErrorCategory(category, code)
	if rawCode == "unknown_runtime_error" && codingRawErrorLooksModelCapacity(rawMessage) {
		return "model_capacity", "model_capacity", "Selected model is currently at capacity. Retry, change model, or switch mode.", true, []string{"retry", "change_model", "switch_to_chat"}
	}
	if rawCode == "unknown_runtime_error" && codingRawErrorLooksUsageLimit(rawMessage) {
		return "usage_limit", "usage_limit", "Codex account is rate limited or quota exhausted. Retry later, switch account, or switch mode.", true, []string{"retry", "switch_account", "switch_to_chat"}
	}
	if rawCode == "unknown_runtime_error" && codingRawErrorLooksAuthFailure(rawMessage) {
		return "auth_failed", "auth_failed", "Codex authentication failed for the active account. Re-authenticate or switch account.", true, []string{"retry", "switch_account", "reauthenticate"}
	}
	if rawCode == "unknown_runtime_error" && codingRawErrorLooksEmptyResponse(rawMessage) {
		return "runtime_unavailable", "runtime_unavailable", codingSafeEmptyResponseMessage(rawMessage), true, []string{"retry", "check_runtime_health"}
	}
	switch rawCode {
	case "bad_request":
		return "bad_request", "unknown_runtime_error", "Request validation failed.", false, []string{"fix_request", "retry"}
	case "invalid_mode_transition":
		return "invalid_mode_transition", "unknown_runtime_error", "Mode transition is not allowed in the current state.", false, []string{"set_valid_mode"}
	case "invalid_lane_for_mode", "invalid_lane_for_session":
		return "invalid_lane_for_session", "unknown_runtime_error", "Selected lane is not writable for this session.", false, []string{"choose_writable_lane"}
	case "idempotency_unavailable":
		return "idempotency_unavailable", "runtime_unavailable", "Command idempotency service is temporarily unavailable.", true, []string{"retry"}
	case "runtime_busy", "session_busy":
		return "runtime_busy", "session_busy", "Session already has an active run.", true, []string{"wait_and_retry", "stop_active_worker"}
	case "runtime_unavailable":
		return "runtime_unavailable", "runtime_unavailable", "Runtime service is unavailable.", true, []string{"retry", "check_runtime_health"}
	case "artifact_version_conflict":
		return "artifact_version_conflict", "runtime_unavailable", "Artifact write was rejected because session state is stale.", true, []string{"retry"}
	case "account_switch_failed":
		return "account_switch_failed", "model_capacity", "Model-capacity recovery could not switch account.", true, []string{"retry", "change_model", "switch_to_chat"}
	case "continue_failed":
		return "continue_failed", "model_capacity", "Model-capacity recovery could not continue on the same session.", true, []string{"retry", "change_model", "switch_to_chat"}
	case "same_session_continue_not_supported":
		return "same_session_continue_not_supported", "model_capacity", "Provider does not support same-session continuation after account switch.", false, []string{"change_model", "switch_to_chat"}
	case "usage_account_switch_failed":
		return "usage_account_switch_failed", "usage_limit", "Usage-limit recovery could not switch account.", true, []string{"retry", "switch_account", "switch_to_chat"}
	case "usage_continue_failed":
		return "usage_continue_failed", "usage_limit", "Usage-limit recovery could not continue on the same session.", true, []string{"retry", "switch_account", "switch_to_chat"}
	case "not_running":
		return "not_running", "runtime_unavailable", "No active worker is running for this session.", false, []string{"refresh_status"}
	case "unknown_runtime_error", "run_failed":
		return "unknown_runtime_error", "unknown_runtime_error", "Runtime failed to complete the request.", retryable, []string{"retry", "check_runtime_health"}
	default:
		return firstNonEmpty(rawCode, "unknown_runtime_error"), normalizedCategory, "Request failed.", retryable, codingSuggestedActionsForError(rawCode, normalizedCategory)
	}
}

func codingRawErrorLooksEmptyResponse(raw string) bool {
	msg := strings.ToLower(strings.TrimSpace(raw))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "empty response from ") ||
		strings.Contains(msg, "empty response from codex")
}

func codingSafeEmptyResponseMessage(raw string) string {
	_ = raw
	return "Codex did not return a usable response. Retry the request."
}

func codingErrorMetaFromErr(err error, fallbackCode, fallbackCategory string, fallbackRetryable bool) (string, string, bool) {
	if err == nil {
		return fallbackCode, fallbackCategory, fallbackRetryable
	}
	var runtimeErr *service.CodingRuntimeError
	if errors.As(err, &runtimeErr) && runtimeErr != nil {
		return firstNonEmpty(strings.TrimSpace(runtimeErr.Code), fallbackCode), firstNonEmpty(strings.TrimSpace(runtimeErr.Category), fallbackCategory), runtimeErr.Retryable
	}
	return fallbackCode, fallbackCategory, fallbackRetryable
}

func codingModeTransitionErrorMetaFromErr(err error) (string, string, bool) {
	if err == nil {
		return "invalid_mode_transition", "unknown_runtime_error", false
	}
	raw := strings.TrimSpace(strings.ToLower(err.Error()))
	if strings.Contains(raw, "illegal mode transition") ||
		strings.Contains(raw, "unsupported public mode") ||
		strings.Contains(raw, "unsupported persisted mode") ||
		strings.Contains(raw, "illegal state:") {
		return "invalid_mode_transition", "unknown_runtime_error", false
	}
	return codingErrorMetaFromErr(err, "unknown_runtime_error", "unknown_runtime_error", false)
}

func respondSanitizedCodingError(w http.ResponseWriter, statusCode int, code, category string, rawErr error, retryable bool) {
	rawMessage := ""
	if rawErr != nil {
		rawMessage = strings.TrimSpace(rawErr.Error())
	}
	errorCode, normalizedCategory, safeMessage, safeRetryable, actions := sanitizeCodingErrorPolicy(code, category, rawMessage, retryable)
	if rawMessage != "" {
		rawLen, rawHash := safeTextDiagnostics(rawMessage)
		log.Printf("[coding-rest] error code=%s category=%s raw_len=%d raw_sha=%s", errorCode, normalizedCategory, rawLen, rawHash)
	}
	respondJSON(w, statusCode, map[string]any{
		"error": map[string]any{
			"type":              errorCode,
			"code":              errorCode,
			"category":          normalizedCategory,
			"message":           safeMessage,
			"retryable":         safeRetryable,
			"suggested_actions": actions,
			"param":             nil,
		},
	})
}

func safeTextDiagnostics(raw string) (int, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return len([]rune(trimmed)), hex.EncodeToString(sum[:8])
}
