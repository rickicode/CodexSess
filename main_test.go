package main

import (
	"strings"
	"testing"
)

func TestCodexMissingInstallHintWindows(t *testing.T) {
	msg := codexMissingInstallHint("windows", "codex")
	for _, needle := range []string{"manual", "codex.cmd", "codex.exe", "CODEXSESS_CODEX_BIN"} {
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(needle)) {
			t.Fatalf("expected %q in message: %s", needle, msg)
		}
	}
}

func TestCodexExecutableInstallHintWindows(t *testing.T) {
	msg := codexExecutableInstallHint("windows", `C:\Users\me\AppData\Roaming\npm\codex.cmd`)
	for _, needle := range []string{"manual", "CODEXSESS_CODEX_BIN", "codex.cmd"} {
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(needle)) {
			t.Fatalf("expected %q in message: %s", needle, msg)
		}
	}
}

func TestWindowsExitPrompt(t *testing.T) {
	msg := windowsExitPrompt()
	for _, needle := range []string{"Enter", "close"} {
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(needle)) {
			t.Fatalf("expected %q in prompt: %s", needle, msg)
		}
	}
}
