package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestListCodingMessagesPage_SameSecondKeepsInsertOrder(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "coding-order.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	sessionID := "sess_order"
	now := time.Now().UTC().Truncate(time.Second)
	_, err = st.CreateCodingSession(ctx, CodingSession{
		ID:            sessionID,
		Title:         "Order",
		Model:         "gpt-5.2-codex",
		WorkDir:       "~/",
		SandboxMode:   "write",
		CreatedAt:     now,
		UpdatedAt:     now,
		LastMessageAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	inserted := []string{"msg_zeta", "msg_alpha", "msg_mid"}
	insertedSeq := make([]int64, 0, len(inserted))
	for _, id := range inserted {
		saved, err := st.AppendCodingMessage(ctx, CodingMessage{
			ID:        id,
			SessionID: sessionID,
			Role:      "assistant",
			Content:   id,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		if saved.Sequence <= 0 {
			t.Fatalf("expected positive sequence for %s", id)
		}
		insertedSeq = append(insertedSeq, saved.Sequence)
	}

	got, hasMore, err := st.ListCodingMessagesPage(ctx, sessionID, 50, "")
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if hasMore {
		t.Fatalf("expected hasMore=false")
	}
	if len(got) != len(inserted) {
		t.Fatalf("expected %d messages, got %d", len(inserted), len(got))
	}
	for i := range inserted {
		if got[i].ID != inserted[i] {
			t.Fatalf("expected message[%d]=%s, got %s", i, inserted[i], got[i].ID)
		}
		if got[i].Sequence != insertedSeq[i] {
			t.Fatalf("expected sequence[%d]=%d, got %d", i, insertedSeq[i], got[i].Sequence)
		}
	}

	older, hasOlder, err := st.ListCodingMessagesPage(ctx, sessionID, 50, inserted[1])
	if err != nil {
		t.Fatalf("list page before cursor: %v", err)
	}
	if hasOlder {
		t.Fatalf("expected hasOlder=false with single older message")
	}
	if len(older) != 1 {
		t.Fatalf("expected 1 older message, got %d", len(older))
	}
	if older[0].ID != inserted[0] {
		t.Fatalf("expected older[0]=%s, got %s", inserted[0], older[0].ID)
	}

	session, err := st.GetCodingSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !session.LastMessageAt.Equal(now) {
		t.Fatalf("expected last_message_at to update to %s, got %s", now, session.LastMessageAt)
	}
}

func TestCodingMessages_AccountEmailRoundTrip(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "coding-account-email.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	sessionID := "sess_account_email"
	_, err = st.CreateCodingSession(ctx, CodingSession{
		ID:            sessionID,
		Title:         "Account Email",
		Model:         "gpt-5.2-codex",
		WorkDir:       "~/",
		SandboxMode:   "write",
		CreatedAt:     now,
		UpdatedAt:     now,
		LastMessageAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	saved, err := st.AppendCodingMessage(ctx, CodingMessage{
		ID:           "msg_account_email",
		SessionID:    sessionID,
		Role:         "assistant",
		Actor:        "chat",
		AccountEmail: "chat-runtime@example.com",
		Content:      "hello",
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if saved.AccountEmail != "chat-runtime@example.com" {
		t.Fatalf("expected saved account_email, got %q", saved.AccountEmail)
	}

	history, err := st.ListCodingMessages(ctx, sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if got := history[0].AccountEmail; got != "chat-runtime@example.com" {
		t.Fatalf("expected list account_email chat-runtime@example.com, got %q", got)
	}
}
