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

	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

const directCodexBaseURL = "https://chatgpt.com/backend-api"

type directAPIResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

func (s *Server) resolveAPIAccountWithTokens(ctx context.Context, selector string) (store.Account, service.TokenSet, error) {
	account, err := s.resolveAPIAccount(ctx, selector)
	if err != nil {
		return store.Account{}, service.TokenSet{}, err
	}
	resolved, tk, err := s.svc.ResolveForRequest(ctx, account.ID)
	if err != nil {
		return store.Account{}, service.TokenSet{}, err
	}
	return resolved, tk, nil
}

func (s *Server) callDirectCodexResponses(
	ctx context.Context,
	account store.Account,
	tk service.TokenSet,
	model string,
	prompt string,
	onDelta func(string) error,
) (directAPIResult, error) {
	claims, err := util.ParseClaims(tk.IDToken, tk.AccessToken)
	if err != nil {
		return directAPIResult{}, fmt.Errorf("parse oauth claims: %w", err)
	}
	accountID := strings.TrimSpace(claims.AccountID)
	if accountID == "" {
		accountID = strings.TrimSpace(account.AccountID)
	}

	payload := map[string]any{
		"model": strings.TrimSpace(model),
		"instructions": "You are Codex. Be concise, accurate, and focus on coding tasks. " +
			"Use available context and respond directly.",
		"store":  false,
		"stream": true,
		"reasoning": map[string]any{
			"effort":  "medium",
			"summary": "auto",
		},
		"text": map[string]any{
			"verbosity": "medium",
		},
		"include": []string{"reasoning.encrypted_content"},
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": strings.TrimSpace(prompt),
					},
				},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return directAPIResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, directCodexBaseURL+"/codex/responses", bytes.NewReader(b))
	if err != nil {
		return directAPIResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(tk.AccessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}

	client := &http.Client{Timeout: 75 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return directAPIResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		return directAPIResult{}, fmt.Errorf("direct_api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseDirectResponseSSE(resp.Body, onDelta)
}

func parseDirectResponseSSE(r io.Reader, onDelta func(string) error) (directAPIResult, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var res directAPIResult
	var dataLines []string

	flushFrame := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			return nil
		}
		eventType, _ := evt["type"].(string)
		eventType = strings.TrimSpace(strings.ToLower(eventType))

		switch eventType {
		case "response.output_text.delta":
			delta, _ := evt["delta"].(string)
			if strings.TrimSpace(delta) != "" {
				res.Text += delta
				if onDelta != nil {
					if err := onDelta(delta); err != nil {
						return err
					}
				}
			}
		case "response.completed":
			response, _ := evt["response"].(map[string]any)
			if usage, _ := response["usage"].(map[string]any); usage != nil {
				res.InputTokens = int(anyNumber(usage["input_tokens"]))
				res.OutputTokens = int(anyNumber(usage["output_tokens"]))
			}
			if strings.TrimSpace(res.Text) == "" {
				res.Text = strings.TrimSpace(extractOutputTextFromCompleted(response))
			}
		}
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if err := flushFrame(); err != nil {
				return directAPIResult{}, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := sc.Err(); err != nil {
		return directAPIResult{}, err
	}
	if err := flushFrame(); err != nil {
		return directAPIResult{}, err
	}
	if strings.TrimSpace(res.Text) == "" {
		return directAPIResult{}, fmt.Errorf("empty response from direct_api")
	}
	return res, nil
}

func extractOutputTextFromCompleted(response map[string]any) string {
	if response == nil {
		return ""
	}
	output, _ := response["output"].([]any)
	if len(output) == 0 {
		return ""
	}
	var parts []string
	for _, itemRaw := range output {
		item, _ := itemRaw.(map[string]any)
		if item == nil {
			continue
		}
		if strings.TrimSpace(strings.ToLower(asString(item["type"]))) != "message" {
			continue
		}
		content, _ := item["content"].([]any)
		for _, partRaw := range content {
			part, _ := partRaw.(map[string]any)
			if part == nil {
				continue
			}
			if strings.TrimSpace(strings.ToLower(asString(part["type"]))) != "output_text" {
				continue
			}
			text := strings.TrimSpace(asString(part["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func anyNumber(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return 0
	}
}
