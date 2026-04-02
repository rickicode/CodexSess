package httpapi

import "testing"

func TestResolveAccountHeader(t *testing.T) {
	v, err := ResolveAccountHeader("acc_123")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v != "acc_123" {
		t.Fatalf("got %q", v)
	}
	empty, err := ResolveAccountHeader("  ")
	if err != nil {
		t.Fatalf("unexpected err for empty header: %v", err)
	}
	if empty != "" {
		t.Fatalf("expected empty selector, got %q", empty)
	}
}
