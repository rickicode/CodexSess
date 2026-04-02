package httpapi

import "testing"

func TestSanitizeCodingErrorPolicy_UsageRecoveryCodes(t *testing.T) {
	t.Run("usage_account_switch_failed", func(t *testing.T) {
		code, category, message, retryable, actions := sanitizeCodingErrorPolicy(
			"usage_account_switch_failed",
			"usage_limit",
			"",
			true,
		)
		if code != "usage_account_switch_failed" {
			t.Fatalf("expected usage_account_switch_failed code, got %q", code)
		}
		if category != "usage_limit" {
			t.Fatalf("expected usage_limit category, got %q", category)
		}
		if message == "" {
			t.Fatalf("expected non-empty message")
		}
		if !retryable {
			t.Fatalf("expected retryable=true")
		}
		if len(actions) == 0 {
			t.Fatalf("expected suggested actions")
		}
	})

	t.Run("usage_continue_failed", func(t *testing.T) {
		code, category, message, retryable, actions := sanitizeCodingErrorPolicy(
			"usage_continue_failed",
			"usage_limit",
			"",
			true,
		)
		if code != "usage_continue_failed" {
			t.Fatalf("expected usage_continue_failed code, got %q", code)
		}
		if category != "usage_limit" {
			t.Fatalf("expected usage_limit category, got %q", category)
		}
		if message == "" {
			t.Fatalf("expected non-empty message")
		}
		if !retryable {
			t.Fatalf("expected retryable=true")
		}
		if len(actions) == 0 {
			t.Fatalf("expected suggested actions")
		}
	})
}

func TestSanitizeCodingErrorPolicy_UnknownRuntimeErrorEmptyResponse(t *testing.T) {
	t.Run("runtime generic", func(t *testing.T) {
		code, category, message, retryable, actions := sanitizeCodingErrorPolicy(
			"unknown_runtime_error",
			"unknown_runtime_error",
			"empty response from codex",
			true,
		)
		if code != "runtime_unavailable" {
			t.Fatalf("expected runtime_unavailable code, got %q", code)
		}
		if category != "runtime_unavailable" {
			t.Fatalf("expected runtime_unavailable category, got %q", category)
		}
		if got := message; got != "Codex did not return a usable response. Retry the request." {
			t.Fatalf("unexpected message: %q", got)
		}
		if !retryable {
			t.Fatalf("expected retryable=true")
		}
		if len(actions) == 0 {
			t.Fatalf("expected suggested actions")
		}
	})

	t.Run("runtime planning", func(t *testing.T) {
		code, category, message, retryable, actions := sanitizeCodingErrorPolicy(
			"unknown_runtime_error",
			"unknown_runtime_error",
			"empty response from codex planning phase",
			true,
		)
		if code != "runtime_unavailable" {
			t.Fatalf("expected runtime_unavailable code, got %q", code)
		}
		if category != "runtime_unavailable" {
			t.Fatalf("expected runtime_unavailable category, got %q", category)
		}
		if got := message; got != "Codex did not return a usable response. Retry the request." {
			t.Fatalf("unexpected message: %q", got)
		}
		if !retryable {
			t.Fatalf("expected retryable=true")
		}
		if len(actions) == 0 {
			t.Fatalf("expected suggested actions")
		}
	})
}

func TestSanitizeCodingErrorPolicy_EmptyCodexResponseUsesGenericChatOnlyMessage(t *testing.T) {
	code, category, message, retryable, actions := sanitizeCodingErrorPolicy(
		"unknown_runtime_error",
		"unknown_runtime_error",
		"empty response from codex",
		true,
	)
	if code != "runtime_unavailable" {
		t.Fatalf("expected runtime_unavailable code, got %q", code)
	}
	if category != "runtime_unavailable" {
		t.Fatalf("expected runtime_unavailable category, got %q", category)
	}
	if message != "Codex did not return a usable response. Retry the request." {
		t.Fatalf("unexpected message: %q", message)
	}
	if !retryable {
		t.Fatalf("expected retryable=true")
	}
	if len(actions) == 0 {
		t.Fatalf("expected suggested actions")
	}
}
