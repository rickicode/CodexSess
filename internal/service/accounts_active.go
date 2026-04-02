package service

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func (s *Service) ActiveCLIAccountID(ctx context.Context) (string, error) {
	s.cliActiveMu.RLock()
	if s.cliActiveCachedID != "" && time.Since(s.cliActiveCachedAt) < cliActiveCacheTTL {
		id := s.cliActiveCachedID
		s.cliActiveMu.RUnlock()
		return id, nil
	}
	s.cliActiveMu.RUnlock()

	s.cliAuthStateMu.Lock()
	defer s.cliAuthStateMu.Unlock()

	selected, err := s.Store.ActiveCLIAccount(ctx)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "account not found") {
			return "", nil
		}
		return "", err
	}
	selectedID := strings.TrimSpace(selected.ID)
	if selectedID == "" {
		return "", nil
	}

	s.cliActiveMu.RLock()
	if s.cliActiveCachedID != "" && s.cliActiveCachedID == selectedID && time.Since(s.cliActiveCachedAt) < cliActiveCacheTTL {
		id := s.cliActiveCachedID
		s.cliActiveMu.RUnlock()
		return id, nil
	}
	s.cliActiveMu.RUnlock()

	needsHeal := false
	authPath := filepath.Join(s.Cfg.CodexHome, "auth.json")
	b, readErr := os.ReadFile(authPath)
	if readErr != nil {
		needsHeal = true
	} else {
		var f struct {
			IDToken     string `json:"id_token"`
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
			Tokens      struct {
				IDToken     string `json:"id_token"`
				AccessToken string `json:"access_token"`
				AccountID   string `json:"account_id"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(b, &f); err != nil {
			needsHeal = true
		} else {
			idToken := firstNonEmpty(f.Tokens.IDToken, f.IDToken)
			accessToken := firstNonEmpty(f.Tokens.AccessToken, f.AccessToken)
			authAccountID := firstNonEmpty(f.Tokens.AccountID, f.AccountID)
			if strings.TrimSpace(idToken) == "" || strings.TrimSpace(accessToken) == "" {
				needsHeal = true
			} else {
				matched := false
				accIDTokenRaw, decErr := s.Crypto.Decrypt(selected.TokenID)
				if decErr == nil {
					accAccessTokenRaw, decErr2 := s.Crypto.Decrypt(selected.TokenAccess)
					if decErr2 == nil && string(accIDTokenRaw) == idToken && string(accAccessTokenRaw) == accessToken {
						matched = true
					}
				}
				if !matched {
					claims, claimErr := util.ParseClaims(idToken, accessToken)
					if claimErr == nil {
						claimAccountID := firstNonEmpty(authAccountID, claims.AccountID)
						if claimAccountID != "" && selected.AccountID != "" && selected.AccountID == claimAccountID {
							matched = true
						}
						if claims.Email != "" && strings.EqualFold(selected.Email, claims.Email) {
							matched = true
						}
					}
				}
				needsHeal = !matched
			}
		}
	}
	if needsHeal {
		if err := s.syncAccountAuthToCodexHomeUnlocked(selected); err != nil {
			log.Printf("[cli-auth] active cli auth.json heal failed for %s: %v", selectedID, err)
		}
	}
	s.setCLIActiveCache(selected.ID)
	return selected.ID, nil
}

func (s *Service) setCLIActiveCache(id string) {
	s.cliActiveMu.Lock()
	s.cliActiveCachedID = strings.TrimSpace(id)
	s.cliActiveCachedAt = time.Now()
	s.cliActiveMu.Unlock()
}

func (s *Service) notifyCLISwitch(ctx context.Context, fromID string, to store.Account) {
	cmd := strings.TrimSpace(s.Cfg.CLISwitchNotifyCmd)
	if cmd == "" {
		return
	}
	fromID = strings.TrimSpace(fromID)
	toID := strings.TrimSpace(to.ID)
	if fromID == "" || toID == "" || fromID == toID {
		return
	}
	reason := cliSwitchReason(ctx)
	if reason == "" {
		reason = "unknown"
	}
	env := []string{
		"CODEXSESS_CLI_SWITCH_FROM=" + fromID,
		"CODEXSESS_CLI_SWITCH_TO=" + toID,
		"CODEXSESS_CLI_SWITCH_REASON=" + reason,
	}
	if email := strings.TrimSpace(to.Email); email != "" {
		env = append(env, "CODEXSESS_CLI_SWITCH_TO_EMAIL="+email)
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := runNotifyCommand(runCtx, cmd, env); err != nil {
			log.Printf("[notify] cli switch command failed: %v", err)
		}
	}()
}

func runNotifyCommand(ctx context.Context, command string, extraEnv []string) error {
	cmdLine := strings.TrimSpace(command)
	if cmdLine == "" {
		return nil
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdLine)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdLine)
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}
