package httpapi

import (
	"errors"
	"net/http"
	"strings"
)

func normalizeAnthropicVersion(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "2023-06-01"
	}
	return trimmed
}

func respondClaudeErr(w http.ResponseWriter, code int, errType, msg, requestID string) {
	body := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(msg),
		},
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		body["request_id"] = rid
	}
	respondJSON(w, code, body)
}

func classifyDirectUpstreamError(err error) (int, string) {
	if err == nil {
		return 500, "upstream_error"
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 400:
			return 400, "bad_request"
		case 401:
			return 401, "unauthorized"
		case 403:
			return 403, "forbidden"
		case 404:
			return 404, "not_found"
		case 408:
			return 408, "timeout"
		case 409:
			return 409, "conflict"
		case 422:
			return 422, "unprocessable_entity"
		case 429:
			return 429, "quota_exhausted"
		default:
			if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
				return httpErr.StatusCode, "upstream_error"
			}
			if httpErr.StatusCode > 0 {
				return httpErr.StatusCode, "upstream_error"
			}
		}
	}
	return 500, "upstream_error"
}

func classifyDirectUpstreamClaudeError(err error) (int, string) {
	if err == nil {
		return 500, "api_error"
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 400:
			return 400, "invalid_request_error"
		case 401:
			return 401, "authentication_error"
		case 403:
			return 403, "permission_error"
		case 404:
			return 404, "not_found_error"
		case 408:
			return 408, "timeout_error"
		case 429:
			return 429, "rate_limit_error"
		default:
			if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
				return httpErr.StatusCode, "api_error"
			}
			if httpErr.StatusCode > 0 {
				return httpErr.StatusCode, "api_error"
			}
		}
	}
	return 500, "api_error"
}

func classifyOpenAISetupError(err error) (int, string, string) {
	if err == nil {
		return 500, "internal_error", "internal error"
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "not found"):
		return 404, "account_not_found", err.Error()
	case strings.Contains(msg, "exhausted"):
		return 429, "quota_exhausted", "target account quota exhausted"
	default:
		return 500, "internal_error", err.Error()
	}
}

func classifyClaudeSetupError(err error) (int, string, string) {
	if err == nil {
		return 500, "api_error", "internal error"
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "not found"):
		return 404, "not_found_error", err.Error()
	case strings.Contains(msg, "exhausted"):
		return 429, "rate_limit_error", "target account quota exhausted"
	default:
		return 500, "api_error", err.Error()
	}
}
