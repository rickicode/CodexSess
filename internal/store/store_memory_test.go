package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryItemsUpsertAndListByNaturalKey(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	first, err := st.UpsertMemoryItem(ctx, MemoryItem{
		ID:         "mem_a",
		Scope:      "session",
		ScopeID:    "sess_1",
		Kind:       "constraint",
		Key:        "goal",
		ValueJSON:  `{"text":"ship feature"}`,
		SourceType: "user",
		SourceRef:  "msg_1",
		Verified:   true,
		Confidence: 90,
	})
	if err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	if first.ID == "" {
		t.Fatalf("expected memory id")
	}

	second, err := st.UpsertMemoryItem(ctx, MemoryItem{
		ID:         "mem_b",
		Scope:      "session",
		ScopeID:    "sess_1",
		Kind:       "constraint",
		Key:        "goal",
		ValueJSON:  `{"text":"ship feature v2"}`,
		SourceType: "user",
		SourceRef:  "msg_2",
		Verified:   true,
		Confidence: 95,
	})
	if err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected upsert to keep natural-key record id=%q, got=%q", first.ID, second.ID)
	}
	if second.ValueJSON != `{"text":"ship feature v2"}` {
		t.Fatalf("expected updated value_json, got %q", second.ValueJSON)
	}

	items, err := st.ListMemoryItems(ctx, MemoryQuery{
		Scope:   "session",
		ScopeID: "sess_1",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("list memory: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one memory item after upsert, got %d", len(items))
	}
}

func TestMemoryItemsListFiltersAndDeleteByScope(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "memory-filters.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	exp := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	_, err = st.UpsertMemoryItem(ctx, MemoryItem{
		ID:         "mem_1",
		Scope:      "session",
		ScopeID:    "sess_2",
		Kind:       "fact",
		Key:        "stack",
		ValueJSON:  `{"name":"svelte"}`,
		SourceType: "repo_scan",
		Verified:   true,
		Confidence: 88,
		ExpiresAt:  &exp,
	})
	if err != nil {
		t.Fatalf("upsert mem_1: %v", err)
	}
	_, err = st.UpsertMemoryItem(ctx, MemoryItem{
		ID:         "mem_2",
		Scope:      "session",
		ScopeID:    "sess_2",
		Kind:       "fact",
		Key:        "old_hint",
		ValueJSON:  `{"text":"stale"}`,
		SourceType: "runtime_observation",
		Verified:   false,
		Confidence: 40,
		Stale:      true,
	})
	if err != nil {
		t.Fatalf("upsert mem_2: %v", err)
	}

	verifiedOnly, err := st.ListMemoryItems(ctx, MemoryQuery{
		Scope:        "session",
		ScopeID:      "sess_2",
		Kinds:        []string{"fact"},
		VerifiedOnly: true,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list verified: %v", err)
	}
	if len(verifiedOnly) != 1 || verifiedOnly[0].Key != "stack" {
		t.Fatalf("expected only verified 'stack' memory, got %#v", verifiedOnly)
	}

	withStale, err := st.ListMemoryItems(ctx, MemoryQuery{
		Scope:        "session",
		ScopeID:      "sess_2",
		Kinds:        []string{"fact"},
		IncludeStale: true,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list include stale: %v", err)
	}
	if len(withStale) != 2 {
		t.Fatalf("expected stale-inclusive query to return 2, got %d", len(withStale))
	}

	if err := st.MarkMemoryItemStale(ctx, "session", "sess_2", "fact", "stack", true); err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	staleOnly, err := st.ListMemoryItems(ctx, MemoryQuery{
		Scope:        "session",
		ScopeID:      "sess_2",
		Kinds:        []string{"fact"},
		IncludeStale: true,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list after mark stale: %v", err)
	}
	foundStaleStack := false
	for _, item := range staleOnly {
		if item.Key == "stack" && item.Stale {
			foundStaleStack = true
			break
		}
	}
	if !foundStaleStack {
		t.Fatalf("expected stack memory to be marked stale, got %#v", staleOnly)
	}

	if err := st.DeleteMemoryItemsByScope(ctx, "session", "sess_2"); err != nil {
		t.Fatalf("delete by scope: %v", err)
	}
	afterDelete, err := st.ListMemoryItems(ctx, MemoryQuery{
		Scope:   "session",
		ScopeID: "sess_2",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(afterDelete) != 0 {
		t.Fatalf("expected no memory after delete, got %d", len(afterDelete))
	}
}
