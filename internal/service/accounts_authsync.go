package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/store"
)

func (s *Service) accountDir(id string) string {
	return filepath.Join(s.Cfg.AuthStoreDir, id)
}

// APICodexHome returns isolated CODEX_HOME path for proxy API execution.
// This prevents API traffic from mutating global CLI auth context.
func (s *Service) APICodexHome(accountID string) string {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return strings.TrimSpace(s.Cfg.CodexHome)
	}
	return s.accountDir(id)
}

func (s *Service) syncAccountAuthToCodexHome(a store.Account) error {
	s.cliAuthStateMu.Lock()
	defer s.cliAuthStateMu.Unlock()
	return s.syncAccountAuthToPathUnlocked(a, s.Cfg.CodexHome)
}

func (s *Service) syncAccountAuthToCodexHomeUnlocked(a store.Account) error {
	return s.syncAccountAuthToPathUnlocked(a, s.Cfg.CodexHome)
}

func (s *Service) syncAccountAuthToPath(a store.Account, codexHome string) error {
	s.cliAuthStateMu.Lock()
	defer s.cliAuthStateMu.Unlock()
	return s.syncAccountAuthToPathUnlocked(a, codexHome)
}

func (s *Service) syncAccountAuthToPathUnlocked(a store.Account, codexHome string) error {
	src := filepath.Join(s.accountDir(a.ID), "auth.json")
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	home := strings.TrimSpace(codexHome)
	if home == "" {
		return fmt.Errorf("codex home is required")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(home, "auth.json")
	tmp, err := os.CreateTemp(home, "auth.json.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	replaced := false
	var replaceErr error
	for attempt := 0; attempt < 5; attempt++ {
		replaceErr = os.Rename(tmpPath, dst)
		if replaceErr == nil {
			replaced = true
			break
		}
		if runtime.GOOS == "windows" {
			replaceErr = replaceFileWindowsPreservingExisting(tmpPath, dst)
			if replaceErr == nil {
				replaced = true
				break
			}
		}
		if replaced {
			break
		}
		if replaceErr == nil {
			replaceErr = fmt.Errorf("replace auth.json failed")
		}
		if !isTransientFileReplaceError(replaceErr) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	if !replaced {
		return replaceErr
	}
	if runtime.GOOS != "windows" {
		if err := syncDir(home); err != nil {
			return err
		}
	}
	committed = true
	return nil
}

func replaceFileWindowsPreservingExisting(tmpPath, dst string) error {
	backupPath := fmt.Sprintf("%s.bak-%d-%d", dst, os.Getpid(), time.Now().UnixNano())
	movedCurrent := false
	if _, statErr := os.Stat(dst); statErr == nil {
		if err := os.Rename(dst, backupPath); err != nil {
			return err
		}
		movedCurrent = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	replaceErr := os.Rename(tmpPath, dst)
	if replaceErr == nil {
		if movedCurrent {
			_ = os.Remove(backupPath)
		}
		return nil
	}
	if !movedCurrent {
		return replaceErr
	}
	if restoreErr := restoreFileWithRetry(backupPath, dst, 5); restoreErr != nil {
		return fmt.Errorf("%w (restore original auth.json: %v)", replaceErr, restoreErr)
	}
	return replaceErr
}

func restoreFileWithRetry(src, dst string, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return err
}

func isTransientFileReplaceError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "resource busy") || strings.Contains(msg, "text file busy") {
		return true
	}
	if strings.Contains(msg, "used by another process") {
		return true
	}
	if strings.Contains(msg, "access is denied") {
		return true
	}
	if strings.Contains(msg, "permission denied") {
		return true
	}
	return false
}

func syncDir(dir string) error {
	path := strings.TrimSpace(dir)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
