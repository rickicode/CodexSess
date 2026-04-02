package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type authJSON struct {
	AccessToken string `json:"access_token"`
	Tokens      struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
}

type jwtClaims struct {
	Auth struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

func main() {
	base := flag.String("base", "https://chatgpt.com/backend-api", "OAuth backend base URL")
	model := flag.String("model", "gpt-5.2-codex", "model name")
	prompt := flag.String("prompt", "Reply exactly with: OK", "test prompt")
	authPath := flag.String("auth-json", defaultAuthJSONPath(), "path to auth.json")
	accessToken := flag.String("access-token", strings.TrimSpace(os.Getenv("CODEXSESS_OAUTH_ACCESS_TOKEN")), "OAuth access token (optional, overrides auth-json)")
	timeout := flag.Duration("timeout", 45*time.Second, "request timeout")
	flag.Parse()

	token := strings.TrimSpace(*accessToken)
	if token == "" {
		t, err := loadAccessTokenFromAuthJSON(strings.TrimSpace(*authPath))
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load token: %v\n", err)
			os.Exit(1)
		}
		token = t
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "empty access token")
		os.Exit(1)
	}
	accountID := extractChatGPTAccountID(token)
	if accountID == "" {
		fmt.Fprintln(os.Stderr, "warning: chatgpt_account_id missing in access token claims")
	}

	reqBody := map[string]any{
		"model": strings.TrimSpace(*model),
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
						"text": strings.TrimSpace(*prompt),
					},
				},
			},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal request: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	paths := []string{
		"/codex/responses",
		"/responses",
	}
	client := &http.Client{Timeout: *timeout}
	for _, p := range paths {
		url := strings.TrimRight(strings.TrimSpace(*base), "/") + p
		status, body, err := doRequest(ctx, client, url, token, accountID, payload)
		if err != nil {
			fmt.Printf("[probe] %s error: %v\n", url, err)
			continue
		}
		fmt.Printf("[probe] %s status=%d\n", url, status)
		if len(body) > 0 {
			fmt.Printf("%s\n", body)
		}
		if status >= 200 && status < 300 {
			fmt.Println("[probe] success: direct OAuth backend is reachable")
			return
		}
	}
	fmt.Println("[probe] failed: no endpoint accepted request")
	os.Exit(2)
}

func doRequest(ctx context.Context, client *http.Client, url string, token string, accountID string, payload []byte) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	if strings.TrimSpace(accountID) != "" {
		req.Header.Set("chatgpt-account-id", strings.TrimSpace(accountID))
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, strings.TrimSpace(string(b)), nil
}

func defaultAuthJSONPath() string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return filepath.Join(v, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "auth.json"
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func loadAccessTokenFromAuthJSON(path string) (string, error) {
	rawPath := strings.TrimSpace(path)
	if rawPath == "" {
		return "", fmt.Errorf("auth path is empty")
	}
	b, err := os.ReadFile(rawPath)
	if err != nil {
		return "", err
	}
	var parsed authJSON
	if err := json.Unmarshal(b, &parsed); err != nil {
		return "", err
	}
	return strings.TrimSpace(firstNonEmpty(parsed.Tokens.AccessToken, parsed.AccessToken)), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func extractChatGPTAccountID(accessToken string) string {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Auth.ChatGPTAccountID)
}
