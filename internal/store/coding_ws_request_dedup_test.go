package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCodingWSRequestDedup_SaveAndSeen(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "coding-ws-dedup.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	seen, err := st.CodingWSRequestSeen(ctx, "sess_a", "req_1")
	if err != nil {
		t.Fatalf("CodingWSRequestSeen initial: %v", err)
	}
	if seen {
		t.Fatalf("expected request id to be unseen initially")
	}
	if err := st.SaveCodingWSRequestID(ctx, "sess_a", "req_1", 24*time.Hour); err != nil {
		t.Fatalf("SaveCodingWSRequestID: %v", err)
	}
	seen, err = st.CodingWSRequestSeen(ctx, "sess_a", "req_1")
	if err != nil {
		t.Fatalf("CodingWSRequestSeen after save: %v", err)
	}
	if !seen {
		t.Fatalf("expected request id to be seen after save")
	}
}

func TestCodingWSRequestDedup_PrunesByWindow(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "coding-ws-dedup-prune.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	_, err = st.execWithRetry(ctx, `
		INSERT INTO coding_ws_request_dedup(session_id,request_id,created_at)
		VALUES(?,?,?)
	`, "sess_a", "old_req", time.Now().UTC().Add(-48*time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed old request id: %v", err)
	}
	if err := st.SaveCodingWSRequestID(ctx, "sess_a", "new_req", 1*time.Hour); err != nil {
		t.Fatalf("SaveCodingWSRequestID: %v", err)
	}
	oldSeen, err := st.CodingWSRequestSeen(ctx, "sess_a", "old_req")
	if err != nil {
		t.Fatalf("CodingWSRequestSeen old: %v", err)
	}
	if oldSeen {
		t.Fatalf("expected old request id to be pruned")
	}
	newSeen, err := st.CodingWSRequestSeen(ctx, "sess_a", "new_req")
	if err != nil {
		t.Fatalf("CodingWSRequestSeen new: %v", err)
	}
	if !newSeen {
		t.Fatalf("expected new request id to remain")
	}
}

func TestCodingWSRequestDedup_ClaimIsAtomic(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "coding-ws-dedup-claim.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	first, err := st.ClaimCodingWSRequestID(ctx, "sess_a", "req_claim", 24*time.Hour)
	if err != nil {
		t.Fatalf("ClaimCodingWSRequestID first: %v", err)
	}
	if !first {
		t.Fatalf("expected first claim to be admitted")
	}
	second, err := st.ClaimCodingWSRequestID(ctx, "sess_a", "req_claim", 24*time.Hour)
	if err != nil {
		t.Fatalf("ClaimCodingWSRequestID second: %v", err)
	}
	if second {
		t.Fatalf("expected second claim to be rejected as duplicate")
	}
}
