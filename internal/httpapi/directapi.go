package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const directCodexBaseURL = "https://chatgpt.com/backend-api"

type directAPIResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
	ToolCalls    []ChatToolCall
}

type directCodexRequestOptions struct {
	MaxOutputTokens int
	StopSequences   []string
	Tools           []ChatToolDef
	ToolChoice      json.RawMessage
	ClaudeProtocol  bool
	AnthropicVer    string
	TextFormat      *ResponseFormat
}

type directAPIHTTPError struct {
	StatusCode int
	Body       string
}

func (e *directAPIHTTPError) Error() string {
	return fmt.Sprintf("direct_api status=%d body=%s", e.StatusCode, strings.TrimSpace(e.Body))
}

func isDirectAPIRevokedError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode != http.StatusUnauthorized {
			return false
		}
		return usageErrorLooksRevoked(httpErr.Body)
	}
	msg := strings.TrimSpace(err.Error())
	return usageErrorLooksRevoked(msg)
}
func isDirectAPIStatus(err error, status int) bool {
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == status
	}
	needle := "status=" + strconv.Itoa(status)
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), needle)
}
