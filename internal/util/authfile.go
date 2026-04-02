package util

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type AuthFile struct {
	OpenAIAPIKey any `json:"OPENAI_API_KEY"`
	Tokens       struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token,omitempty"`
		AccountID    string `json:"account_id,omitempty"`
	} `json:"tokens"`
	LastRefresh any `json:"last_refresh,omitempty"`
}

func WriteAuthJSON(codexHome, idToken, accessToken, refreshToken, accountID string) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	f := AuthFile{}
	f.Tokens.IDToken = idToken
	f.Tokens.AccessToken = accessToken
	f.Tokens.RefreshToken = refreshToken
	f.Tokens.AccountID = accountID
	f.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(codexHome, "auth.json"), b, 0o600)
}
