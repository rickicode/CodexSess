package httpapi

import (
	"fmt"
	"testing"
	"time"
)

func TestResolveDirectAPITimeout_Default(t *testing.T) {
	t.Setenv("CODEXSESS_DIRECT_API_TIMEOUT_SECONDS", "")
	if got := resolveDirectAPITimeout(); got != 180*time.Second {
		t.Fatalf("expected default timeout 180s, got %s", got)
	}
}

func TestResolveDirectAPITimeout_ClampsRange(t *testing.T) {
	t.Setenv("CODEXSESS_DIRECT_API_TIMEOUT_SECONDS", "10")
	if got := resolveDirectAPITimeout(); got != 30*time.Second {
		t.Fatalf("expected min-clamped timeout 30s, got %s", got)
	}

	t.Setenv("CODEXSESS_DIRECT_API_TIMEOUT_SECONDS", "9999")
	if got := resolveDirectAPITimeout(); got != 600*time.Second {
		t.Fatalf("expected max-clamped timeout 600s, got %s", got)
	}
}

func TestResolveDirectAPITimeout_ParsesValidValue(t *testing.T) {
	t.Setenv("CODEXSESS_DIRECT_API_TIMEOUT_SECONDS", "240")
	if got := resolveDirectAPITimeout(); got != 240*time.Second {
		t.Fatalf("expected timeout 240s, got %s", got)
	}
}

func TestIsDirectAPIStatus_TypedError(t *testing.T) {
	err := &directAPIHTTPError{StatusCode: 429, Body: `{"error":"rate_limit"}`}
	if !isDirectAPIStatus(err, 429) {
		t.Fatalf("expected typed direct api error to match status 429")
	}
	if isDirectAPIStatus(err, 500) {
		t.Fatalf("did not expect typed direct api error to match status 500")
	}
}

func TestIsDirectAPIStatus_FallbackString(t *testing.T) {
	err := fmt.Errorf("direct_api status=429 body=too many requests")
	if !isDirectAPIStatus(err, 429) {
		t.Fatalf("expected fallback status parser to match status 429")
	}
}

func TestResolveDirectAPIAnthropicBetaHeader_Default(t *testing.T) {
	t.Setenv("CODEXSESS_DIRECT_API_ANTHROPIC_BETA", "")
	got := resolveDirectAPIAnthropicBetaHeader()
	want := "claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"
	if got != want {
		t.Fatalf("expected default anthropic beta header %q, got %q", want, got)
	}
}

func TestResolveDirectAPIAnthropicBetaHeader_Off(t *testing.T) {
	t.Setenv("CODEXSESS_DIRECT_API_ANTHROPIC_BETA", "off")
	if got := resolveDirectAPIAnthropicBetaHeader(); got != "" {
		t.Fatalf("expected empty anthropic beta header when off, got %q", got)
	}
}

func TestUsageErrorLooksRevoked(t *testing.T) {
	if !usageErrorLooksRevoked(`401 {"error":{"code":"token_revoked","message":"Encountered invalidated oauth token for user"}}`) {
		t.Fatalf("expected token_revoked payload to be detected as revoked")
	}
	if usageErrorLooksRevoked("timeout while refreshing usage") {
		t.Fatalf("did not expect timeout to be detected as revoked")
	}
}

func TestIsDirectAPIRevokedError(t *testing.T) {
	err := &directAPIHTTPError{
		StatusCode: 401,
		Body:       `{"error":{"code":"token_revoked","message":"Encountered invalidated oauth token for user"}}`,
	}
	if !isDirectAPIRevokedError(err) {
		t.Fatalf("expected direct 401 token_revoked error to be treated as revoked")
	}
}
