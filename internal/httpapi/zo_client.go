package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func callZoAsk(ctx context.Context, rawKey string, req zoAskRequest) (zoAskResult, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, zoAskEndpoint, bytes.NewReader(payload))
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rawKey))
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return zoAskResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zoAskResult{}, fmt.Errorf("zo api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed zoAskResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return zoAskResult{}, err
	}
	text := firstNonEmpty(coerceAnyText(parsed.Output), coerceAnyText(parsed.Response), coerceAnyText(parsed.Text))
	if strings.TrimSpace(text) == "" {
		return zoAskResult{}, fmt.Errorf("empty response from zo api")
	}
	result := zoAskResult{
		Text:           text,
		ConversationID: strings.TrimSpace(parsed.ConversationID),
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("x-conversation-id"))
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("X-Conversation-Id"))
	}
	return result, nil
}

//nolint:unused // used by Zo-specific handlers when route wiring is enabled
func isZoConversationBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "status=409") && strings.Contains(msg, "conversation is busy")
}

func callZoAskStream(ctx context.Context, rawKey string, req zoAskRequest, onDelta func(string) error) (zoAskResult, error) {
	req.Stream = true
	payload, err := json.Marshal(req)
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, zoAskEndpoint, bytes.NewReader(payload))
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rawKey))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(httpReq)
	if err != nil {
		return zoAskResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		return zoAskResult{}, fmt.Errorf("zo api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 128*1024), 8*1024*1024)
	var (
		result     zoAskResult
		eventType  string
		dataLines  []string
		eventIsEnd bool
	)
	flushFrame := func() error {
		if len(dataLines) == 0 {
			eventType = ""
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" || payload == "[DONE]" {
			eventType = ""
			return nil
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			eventType = ""
			return nil
		}
		if conv := strings.TrimSpace(coerceAnyText(evt["conversation_id"])); conv != "" {
			result.ConversationID = conv
		}
		if errMsg := strings.TrimSpace(coerceAnyText(evt["message"])); errMsg != "" && strings.Contains(strings.ToLower(eventType), "error") {
			return fmt.Errorf("zo api error: %s", errMsg)
		}
		delta := ""
		switch strings.ToLower(strings.TrimSpace(eventType)) {
		case "frontendmodelresponse":
			delta = coerceAnyText(evt["content"])
		case "end":
			if strings.TrimSpace(result.Text) == "" {
				delta = firstNonEmpty(coerceAnyText(evt["output"]), coerceAnyText(evt["content"]))
			}
		default:
			delta = firstNonEmpty(
				coerceAnyText(evt["delta"]),
				coerceAnyText(evt["output"]),
				coerceAnyText(evt["response"]),
				coerceAnyText(evt["text"]),
				coerceAnyText(evt["content"]),
			)
		}
		if delta != "" {
			result.Text += delta
			if onDelta != nil {
				if err := onDelta(delta); err != nil {
					return err
				}
			}
		}
		if strings.Contains(strings.ToLower(eventType), "end") {
			eventIsEnd = true
		}
		eventType = ""
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if err := flushFrame(); err != nil {
				return zoAskResult{}, err
			}
			if eventIsEnd {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := sc.Err(); err != nil {
		return zoAskResult{}, err
	}
	if err := flushFrame(); err != nil {
		return zoAskResult{}, err
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("x-conversation-id"))
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("X-Conversation-Id"))
	}
	if strings.TrimSpace(result.Text) == "" {
		return zoAskResult{}, fmt.Errorf("empty response from zo api")
	}
	return result, nil
}
