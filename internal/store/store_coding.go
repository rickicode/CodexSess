package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func sanitizeCodingSessionStateStore(session *CodingSession) {
	if session == nil {
		return
	}
	session.CodexThreadID = normalizeCodingThreadID(session.CodexThreadID)
}

func normalizeLoadedCodingSession(session *CodingSession, restartPending int, createdAt, updatedAt, lastMessageAt string) {
	if session == nil {
		return
	}
	session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
	session.RestartPending = restartPending != 0
	sanitizeCodingSessionStateStore(session)
	session.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	session.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	session.LastMessageAt, _ = time.Parse(time.RFC3339, lastMessageAt)
}

func normalizeCodingThreadID(threadID string) string {
	return strings.TrimSpace(threadID)
}

func (s *Store) CreateCodingSession(ctx context.Context, session CodingSession) (CodingSession, error) {
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = now
	}
	if session.LastMessageAt.IsZero() {
		session.LastMessageAt = now
	}
	if strings.TrimSpace(session.Title) == "" {
		session.Title = "New Session"
	}
	if strings.TrimSpace(session.Model) == "" {
		session.Model = "gpt-5.2-codex"
	}
	session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
	if strings.TrimSpace(session.WorkDir) == "" {
		session.WorkDir = "~/"
	}
	sanitizeCodingSessionStateStore(&session)
	_, err := s.execWithRetry(ctx, `
		INSERT INTO coding_sessions(
			id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,
			restart_pending,artifact_version,last_applied_event_seq,created_at,updated_at,last_message_at
		)
		VALUES(
				?,?,?,?,?,?,?,?,?,?,?,?,?
		)
	`, session.ID, session.Title, session.Model, session.ReasoningLevel, session.WorkDir, session.SandboxMode, session.CodexThreadID, boolToInt(session.RestartPending), session.ArtifactVersion, session.LastAppliedEventSeq, session.CreatedAt.UTC().Format(time.RFC3339), session.UpdatedAt.UTC().Format(time.RFC3339), session.LastMessageAt.UTC().Format(time.RFC3339))
	if err != nil {
		return CodingSession{}, err
	}
	return s.GetCodingSession(ctx, session.ID)
}

func (s *Store) ListCodingSessions(ctx context.Context) ([]CodingSession, error) {
	rows, err := s.db.QueryContext(ctx, `
			SELECT id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			       artifact_version,last_applied_event_seq,
		       created_at,updated_at,last_message_at
		FROM coding_sessions
		ORDER BY last_message_at DESC, updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CodingSession
	for rows.Next() {
		var session CodingSession
		var createdAt, updatedAt, lastMessageAt string
		var restartPending int
		if err := rows.Scan(&session.ID, &session.Title, &session.Model, &session.ReasoningLevel, &session.WorkDir, &session.SandboxMode, &session.CodexThreadID, &restartPending, &session.ArtifactVersion, &session.LastAppliedEventSeq, &createdAt, &updatedAt, &lastMessageAt); err != nil {
			return nil, err
		}
		normalizeLoadedCodingSession(&session, restartPending, createdAt, updatedAt, lastMessageAt)
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetCodingSession(ctx context.Context, id string) (CodingSession, error) {
	row := s.db.QueryRowContext(ctx, `
			SELECT id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			       artifact_version,last_applied_event_seq,
		       created_at,updated_at,last_message_at
		FROM coding_sessions
		WHERE id=?
		LIMIT 1
	`, strings.TrimSpace(id))
	var session CodingSession
	var restartPending int
	var createdAt, updatedAt, lastMessageAt string
	if err := row.Scan(&session.ID, &session.Title, &session.Model, &session.ReasoningLevel, &session.WorkDir, &session.SandboxMode, &session.CodexThreadID, &restartPending, &session.ArtifactVersion, &session.LastAppliedEventSeq, &createdAt, &updatedAt, &lastMessageAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return session, fmt.Errorf("coding session not found")
		}
		return session, err
	}
	normalizeLoadedCodingSession(&session, restartPending, createdAt, updatedAt, lastMessageAt)
	return session, nil
}

func (s *Store) UpdateCodingSession(ctx context.Context, session CodingSession) error {
	if strings.TrimSpace(session.ID) == "" {
		return fmt.Errorf("coding session id is required")
	}
	if strings.TrimSpace(session.Title) == "" {
		session.Title = "New Session"
	}
	if strings.TrimSpace(session.Model) == "" {
		session.Model = "gpt-5.2-codex"
	}
	session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
	if strings.TrimSpace(session.WorkDir) == "" {
		session.WorkDir = "~/"
	}
	if strings.TrimSpace(session.SandboxMode) == "" {
		session.SandboxMode = "full-access"
	}
	sanitizeCodingSessionStateStore(&session)
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now().UTC()
	}
	if session.LastMessageAt.IsZero() {
		session.LastMessageAt = session.UpdatedAt
	}
	_, err := s.execWithRetry(ctx, `
		UPDATE coding_sessions
		SET title=?, model=?, reasoning_level=?, work_dir=?, sandbox_mode=?, codex_thread_id=?, restart_pending=?,
		    artifact_version=?, last_applied_event_seq=?, updated_at=?, last_message_at=?
		WHERE id=?
	`, session.Title, session.Model, session.ReasoningLevel, session.WorkDir, session.SandboxMode, session.CodexThreadID, boolToInt(session.RestartPending), session.ArtifactVersion, session.LastAppliedEventSeq, session.UpdatedAt.UTC().Format(time.RFC3339), session.LastMessageAt.UTC().Format(time.RFC3339), session.ID)
	return err
}

func (s *Store) UpdateCodingSessionIfArtifactVersion(ctx context.Context, session CodingSession, expectedArtifactVersion int64) (bool, error) {
	if strings.TrimSpace(session.ID) == "" {
		return false, fmt.Errorf("coding session id is required")
	}
	if strings.TrimSpace(session.Title) == "" {
		session.Title = "New Session"
	}
	if strings.TrimSpace(session.Model) == "" {
		session.Model = "gpt-5.2-codex"
	}
	session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
	if strings.TrimSpace(session.WorkDir) == "" {
		session.WorkDir = "~/"
	}
	if strings.TrimSpace(session.SandboxMode) == "" {
		session.SandboxMode = "full-access"
	}
	sanitizeCodingSessionStateStore(&session)
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now().UTC()
	}
	if session.LastMessageAt.IsZero() {
		session.LastMessageAt = session.UpdatedAt
	}
	res, err := s.execWithRetry(ctx, `
		UPDATE coding_sessions
		SET title=?, model=?, reasoning_level=?, work_dir=?, sandbox_mode=?, codex_thread_id=?, restart_pending=?,
		    artifact_version=?, last_applied_event_seq=?, updated_at=?, last_message_at=?
		WHERE id=? AND artifact_version=?
	`, session.Title, session.Model, session.ReasoningLevel, session.WorkDir, session.SandboxMode, session.CodexThreadID, boolToInt(session.RestartPending), session.ArtifactVersion, session.LastAppliedEventSeq, session.UpdatedAt.UTC().Format(time.RFC3339), session.LastMessageAt.UTC().Format(time.RFC3339), session.ID, expectedArtifactVersion)
	if err != nil {
		return false, err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (s *Store) ClaimCodingSessionArtifactVersion(ctx context.Context, sessionID string, expectedArtifactVersion int64) (int64, bool, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return 0, false, fmt.Errorf("coding session id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.execWithRetry(ctx, `
		UPDATE coding_sessions
		SET artifact_version = artifact_version + 1, updated_at=?
		WHERE id=? AND artifact_version=?
	`, now, sid, expectedArtifactVersion)
	if err != nil {
		return 0, false, err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if rowsAffected == 0 {
		return 0, false, nil
	}
	return expectedArtifactVersion + 1, true, nil
}

func (s *Store) RestoreCodingSessionArtifactVersion(ctx context.Context, sessionID string, currentArtifactVersion, targetArtifactVersion int64) (bool, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false, fmt.Errorf("coding session id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.execWithRetry(ctx, `
		UPDATE coding_sessions
		SET artifact_version=?, updated_at=?
		WHERE id=? AND artifact_version=?
	`, targetArtifactVersion, now, sid, currentArtifactVersion)
	if err != nil {
		return false, err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (s *Store) DeleteCodingSession(ctx context.Context, id string) error {
	sessionID := strings.TrimSpace(id)
	if sessionID == "" {
		return fmt.Errorf("coding session id is required")
	}
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM coding_messages WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM coding_message_snapshots WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM coding_view_messages WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM coding_sessions WHERE id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendCodingMessage(ctx context.Context, msg CodingMessage) (CodingMessage, error) {
	if strings.TrimSpace(msg.ID) == "" {
		return CodingMessage{}, fmt.Errorf("coding message id is required")
	}
	if strings.TrimSpace(msg.SessionID) == "" {
		return CodingMessage{}, fmt.Errorf("coding message session_id is required")
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	res, err := s.execWithRetry(ctx, `
		INSERT INTO coding_messages(id,session_id,role,actor,account_email,content,input_tokens,output_tokens,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)
	`, msg.ID, msg.SessionID, msg.Role, strings.TrimSpace(msg.Actor), strings.TrimSpace(msg.AccountEmail), msg.Content, msg.InputTokens, msg.OutputTokens, msg.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return CodingMessage{}, err
	}
	if seq, seqErr := res.LastInsertId(); seqErr == nil {
		msg.Sequence = seq
	}
	messageAt := msg.CreatedAt.UTC()
	if messageAt.IsZero() {
		messageAt = time.Now().UTC()
	}
	if _, err := s.execWithRetry(ctx, `
		UPDATE coding_sessions
		SET updated_at=?, last_message_at=?
		WHERE id=?
	`, messageAt.Format(time.RFC3339), messageAt.Format(time.RFC3339), msg.SessionID); err != nil {
		return CodingMessage{}, err
	}
	return msg, nil
}

func (s *Store) UpdateCodingMessage(ctx context.Context, msg CodingMessage) (CodingMessage, error) {
	if strings.TrimSpace(msg.ID) == "" {
		return CodingMessage{}, fmt.Errorf("coding message id is required")
	}
	if strings.TrimSpace(msg.SessionID) == "" {
		return CodingMessage{}, fmt.Errorf("coding message session_id is required")
	}
	if msg.CreatedAt.IsZero() {
		row := s.db.QueryRowContext(ctx, `
			SELECT created_at, rowid
			FROM coding_messages
			WHERE id=? AND session_id=?
			LIMIT 1
		`, strings.TrimSpace(msg.ID), strings.TrimSpace(msg.SessionID))
		var createdAt string
		var rowID int64
		if err := row.Scan(&createdAt, &rowID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CodingMessage{}, fmt.Errorf("coding message not found")
			}
			return CodingMessage{}, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		msg.Sequence = rowID
	}
	_, err := s.execWithRetry(ctx, `
		UPDATE coding_messages
		SET role=?, actor=?, account_email=?, content=?, input_tokens=?, output_tokens=?
		WHERE id=? AND session_id=?
	`, msg.Role, strings.TrimSpace(msg.Actor), strings.TrimSpace(msg.AccountEmail), msg.Content, msg.InputTokens, msg.OutputTokens, msg.ID, msg.SessionID)
	if err != nil {
		return CodingMessage{}, err
	}
	messageAt := time.Now().UTC()
	if _, err := s.execWithRetry(ctx, `
		UPDATE coding_sessions
		SET updated_at=?, last_message_at=?
		WHERE id=?
	`, messageAt.Format(time.RFC3339), messageAt.Format(time.RFC3339), msg.SessionID); err != nil {
		return CodingMessage{}, err
	}
	return msg, nil
}

func (s *Store) ListCodingMessages(ctx context.Context, sessionID string) ([]CodingMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rowid,id,session_id,role,actor,account_email,content,input_tokens,output_tokens,created_at
		FROM coding_messages
		WHERE session_id=?
		ORDER BY created_at ASC, rowid ASC
	`, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CodingMessage
	for rows.Next() {
		var msg CodingMessage
		var sequence int64
		var createdAt string
		if err := rows.Scan(&sequence, &msg.ID, &msg.SessionID, &msg.Role, &msg.Actor, &msg.AccountEmail, &msg.Content, &msg.InputTokens, &msg.OutputTokens, &createdAt); err != nil {
			return nil, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		msg.Sequence = sequence
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListCodingMessagesPage(ctx context.Context, sessionID string, limit int, beforeID string) ([]CodingMessage, bool, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, false, fmt.Errorf("coding session id is required")
	}
	if limit <= 0 {
		return nil, false, fmt.Errorf("limit must be positive")
	}
	cursorID := strings.TrimSpace(beforeID)
	var cursorCreatedAt string
	var cursorRowID int64
	if cursorID != "" {
		row := s.db.QueryRowContext(ctx, `
			SELECT created_at, rowid
			FROM coding_messages
			WHERE session_id=? AND id=?
			LIMIT 1
		`, sid, cursorID)
		if err := row.Scan(&cursorCreatedAt, &cursorRowID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Cursor can become stale when history is refreshed/deleted while user scrolls.
				// Treat as end-of-history instead of failing the whole request.
				return []CodingMessage{}, false, nil
			}
			return nil, false, err
		}
	}
	var (
		rows *sql.Rows
		err  error
	)
	if cursorID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT rowid,id,session_id,role,actor,account_email,content,input_tokens,output_tokens,created_at
			FROM coding_messages
			WHERE session_id=?
				AND (created_at < ? OR (created_at = ? AND rowid < ?))
			ORDER BY created_at DESC, rowid DESC
			LIMIT ?
		`, sid, cursorCreatedAt, cursorCreatedAt, cursorRowID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT rowid,id,session_id,role,actor,account_email,content,input_tokens,output_tokens,created_at
			FROM coding_messages
			WHERE session_id=?
			ORDER BY created_at DESC, rowid DESC
			LIMIT ?
		`, sid, limit)
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]CodingMessage, 0, limit)
	rowIDs := make([]int64, 0, limit)
	for rows.Next() {
		var msg CodingMessage
		var rowID int64
		var createdAt string
		if err := rows.Scan(&rowID, &msg.ID, &msg.SessionID, &msg.Role, &msg.Actor, &msg.AccountEmail, &msg.Content, &msg.InputTokens, &msg.OutputTokens, &createdAt); err != nil {
			return nil, false, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		msg.Sequence = rowID
		out = append(out, msg)
		rowIDs = append(rowIDs, rowID)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
		rowIDs[i], rowIDs[j] = rowIDs[j], rowIDs[i]
	}
	hasMore := false
	if len(out) == limit && len(out) > 0 {
		oldest := out[0]
		oldestCreatedAt := oldest.CreatedAt.UTC().Format(time.RFC3339)
		oldestRowID := rowIDs[0]
		row := s.db.QueryRowContext(ctx, `
			SELECT 1
			FROM coding_messages
			WHERE session_id=?
				AND (created_at < ? OR (created_at = ? AND rowid < ?))
			LIMIT 1
		`, sid, oldestCreatedAt, oldestCreatedAt, oldestRowID)
		var exists int
		if err := row.Scan(&exists); err == nil {
			hasMore = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}
	return out, hasMore, nil
}

func (s *Store) UpsertCodingMessageSnapshot(ctx context.Context, sessionID, viewMode, snapshotJSON string) error {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return fmt.Errorf("coding session id is required")
	}
	mode := strings.TrimSpace(strings.ToLower(viewMode))
	if mode == "" {
		mode = "compact"
	}
	payload := strings.TrimSpace(snapshotJSON)
	if payload == "" {
		payload = "[]"
	}
	_, err := s.execWithRetry(ctx, `
		INSERT INTO coding_message_snapshots(session_id,view_mode,snapshot_json,updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(session_id, view_mode)
		DO UPDATE SET snapshot_json=excluded.snapshot_json, updated_at=excluded.updated_at
	`, sid, mode, payload, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) AppendCodingViewMessage(ctx context.Context, sessionID, viewMode string, item map[string]any, keepRows int) error {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return fmt.Errorf("coding session id is required")
	}
	mode := strings.TrimSpace(strings.ToLower(viewMode))
	if mode == "" {
		mode = "compact"
	}
	if item == nil {
		return fmt.Errorf("coding view message is required")
	}
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var nextSeq int
	row := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) + 1
		FROM coding_view_messages
		WHERE session_id=? AND view_mode=?
	`, sid, mode)
	if err := row.Scan(&nextSeq); err != nil {
		return err
	}

	messageID := strings.TrimSpace(fmt.Sprintf("%v", item["id"]))
	if messageID == "" {
		messageID = fmt.Sprintf("%s-%s-%06d", sid, mode, nextSeq)
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := txExecWithRetry(ctx, tx, `
		INSERT INTO coding_view_messages(session_id,view_mode,message_id,seq,payload_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(session_id, view_mode, message_id)
		DO UPDATE SET seq=excluded.seq, payload_json=excluded.payload_json, updated_at=excluded.updated_at
	`, sid, mode, messageID, nextSeq, string(payload), now, now); err != nil {
		return err
	}

	if keepRows > 0 {
		cutoffSeq := nextSeq - keepRows
		if cutoffSeq >= 0 {
			if _, err := txExecWithRetry(ctx, tx, `
				DELETE FROM coding_view_messages
				WHERE session_id=? AND view_mode=? AND seq <= ?
			`, sid, mode, cutoffSeq); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) GetCodingMessageSnapshot(ctx context.Context, sessionID, viewMode string) (string, bool, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return "", false, fmt.Errorf("coding session id is required")
	}
	mode := strings.TrimSpace(strings.ToLower(viewMode))
	if mode == "" {
		mode = "compact"
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT snapshot_json
		FROM coding_message_snapshots
		WHERE session_id=? AND view_mode=?
		LIMIT 1
	`, sid, mode)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	if strings.TrimSpace(payload) == "" {
		payload = "[]"
	}
	return payload, true, nil
}

func (s *Store) ReplaceCodingViewMessages(ctx context.Context, sessionID, viewMode string, messages []map[string]any) error {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return fmt.Errorf("coding session id is required")
	}
	mode := strings.TrimSpace(strings.ToLower(viewMode))
	if mode == "" {
		mode = "compact"
	}
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM coding_view_messages WHERE session_id=? AND view_mode=?`, sid, mode); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for idx, item := range messages {
		messageID := strings.TrimSpace(fmt.Sprintf("%v", item["id"]))
		if messageID == "" {
			messageID = fmt.Sprintf("%s-%s-%06d", sid, mode, idx+1)
		}
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		seq := idx + 1
		if _, err := txExecWithRetry(ctx, tx, `
			INSERT INTO coding_view_messages(session_id,view_mode,message_id,seq,payload_json,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)
		`, sid, mode, messageID, seq, string(payload), now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListCodingViewMessagesPage(ctx context.Context, sessionID, viewMode string, limit int, beforeID string) ([]map[string]any, bool, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, false, fmt.Errorf("coding session id is required")
	}
	mode := strings.TrimSpace(strings.ToLower(viewMode))
	if mode == "" {
		mode = "compact"
	}
	if limit <= 0 {
		return nil, false, fmt.Errorf("limit must be positive")
	}
	cursorID := strings.TrimSpace(beforeID)
	cursorSeq := 0
	if cursorID != "" {
		row := s.db.QueryRowContext(ctx, `
			SELECT seq
			FROM coding_view_messages
			WHERE session_id=? AND view_mode=? AND message_id=?
			LIMIT 1
		`, sid, mode, cursorID)
		if err := row.Scan(&cursorSeq); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return []map[string]any{}, false, nil
			}
			return nil, false, err
		}
	}
	var (
		rows *sql.Rows
		err  error
	)
	if cursorID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT message_id,payload_json
			FROM coding_view_messages
			WHERE session_id=? AND view_mode=? AND seq < ?
			ORDER BY seq DESC
			LIMIT ?
		`, sid, mode, cursorSeq, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT message_id,payload_json
			FROM coding_view_messages
			WHERE session_id=? AND view_mode=?
			ORDER BY seq DESC
			LIMIT ?
		`, sid, mode, limit)
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	reversed := make([]map[string]any, 0, limit)
	for rows.Next() {
		var messageID string
		var payload string
		if err := rows.Scan(&messageID, &payload); err != nil {
			return nil, false, err
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, false, err
		}
		if strings.TrimSpace(fmt.Sprintf("%v", item["id"])) == "" {
			item["id"] = messageID
		}
		reversed = append(reversed, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	out := make([]map[string]any, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		out = append(out, reversed[i])
	}
	hasMore := false
	if len(reversed) > 0 {
		lastID := strings.TrimSpace(fmt.Sprintf("%v", out[0]["id"]))
		if lastID != "" {
			row := s.db.QueryRowContext(ctx, `
				SELECT 1
				FROM coding_view_messages
				WHERE session_id=? AND view_mode=? AND seq < (
					SELECT seq FROM coding_view_messages
					WHERE session_id=? AND view_mode=? AND message_id=?
					LIMIT 1
				)
				LIMIT 1
			`, sid, mode, sid, mode, lastID)
			var marker int
			if err := row.Scan(&marker); err == nil {
				hasMore = true
			} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, false, err
			}
		}
	}
	return out, hasMore, nil
}

func (s *Store) ListCodingViewMessages(ctx context.Context, sessionID, viewMode string) ([]map[string]any, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, fmt.Errorf("coding session id is required")
	}
	mode := strings.TrimSpace(strings.ToLower(viewMode))
	if mode == "" {
		mode = "compact"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id,payload_json
		FROM coding_view_messages
		WHERE session_id=? AND view_mode=?
		ORDER BY seq ASC
	`, sid, mode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]map[string]any, 0, 64)
	for rows.Next() {
		var messageID string
		var payload string
		if err := rows.Scan(&messageID, &payload); err != nil {
			return nil, err
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		if strings.TrimSpace(fmt.Sprintf("%v", item["id"])) == "" {
			item["id"] = messageID
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) NextCodingSessionEventSeq(ctx context.Context, sessionID string) (int64, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return 0, fmt.Errorf("coding session id is required")
	}
	var lastErr error
	delay := 20 * time.Millisecond
	for attempt := 0; attempt < sqliteBusyRetryAttempts; attempt++ {
		row := s.db.QueryRowContext(ctx, `
			UPDATE coding_sessions
			SET last_applied_event_seq = last_applied_event_seq + 1,
			    updated_at=?
			WHERE id=?
			RETURNING last_applied_event_seq
		`, time.Now().UTC().Format(time.RFC3339), sid)
		var next int64
		if err := row.Scan(&next); err == nil {
			return next, nil
		} else if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("coding session not found")
		} else {
			lastErr = err
			if !isSQLiteBusyError(err) {
				return 0, err
			}
		}
		if ctx != nil && ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if attempt == sqliteBusyRetryAttempts-1 {
			break
		}
		time.Sleep(delay)
		if delay < 240*time.Millisecond {
			delay *= 2
		}
	}
	return 0, lastErr
}

func (s *Store) CodingWSRequestSeen(ctx context.Context, sessionID, requestID string) (bool, error) {
	sid := strings.TrimSpace(sessionID)
	rid := strings.TrimSpace(requestID)
	if sid == "" || rid == "" {
		return false, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM coding_ws_request_dedup
		WHERE session_id=? AND request_id=?
		LIMIT 1
	`, sid, rid)
	var marker int
	if err := row.Scan(&marker); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) SaveCodingWSRequestID(ctx context.Context, sessionID, requestID string, keepWindow time.Duration) error {
	sid := strings.TrimSpace(sessionID)
	rid := strings.TrimSpace(requestID)
	if sid == "" || rid == "" {
		return nil
	}
	now := time.Now().UTC()
	if _, err := s.execWithRetry(ctx, `
		INSERT INTO coding_ws_request_dedup(session_id,request_id,created_at)
		VALUES(?,?,?)
		ON CONFLICT(session_id, request_id)
		DO NOTHING
	`, sid, rid, now.Format(time.RFC3339)); err != nil {
		return err
	}
	if keepWindow <= 0 {
		keepWindow = 24 * time.Hour
	}
	cutoff := now.Add(-keepWindow).Format(time.RFC3339)
	_, _ = s.execWithRetry(ctx, `
		DELETE FROM coding_ws_request_dedup
		WHERE created_at < ?
	`, cutoff)
	return nil
}

func (s *Store) ClaimCodingWSRequestID(ctx context.Context, sessionID, requestID string, keepWindow time.Duration) (bool, error) {
	sid := strings.TrimSpace(sessionID)
	rid := strings.TrimSpace(requestID)
	if sid == "" || rid == "" {
		return false, nil
	}
	now := time.Now().UTC()
	res, err := s.execWithRetry(ctx, `
		INSERT INTO coding_ws_request_dedup(session_id,request_id,created_at)
		VALUES(?,?,?)
		ON CONFLICT(session_id, request_id)
		DO NOTHING
	`, sid, rid, now.Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if keepWindow <= 0 {
		keepWindow = 24 * time.Hour
	}
	cutoff := now.Add(-keepWindow).Format(time.RFC3339)
	if _, err := s.execWithRetry(ctx, `
		DELETE FROM coding_ws_request_dedup
		WHERE created_at < ?
	`, cutoff); err != nil {
		return false, err
	}
	return rows > 0, nil
}
