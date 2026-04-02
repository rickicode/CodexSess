package service

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed defaults/codex-agents/*.toml defaults/codex-agents/UPSTREAM-LICENSE
var bundledCodingTemplateAgents embed.FS

func seedBundledCodingTemplateAgents(root string) error {
	agentsRoot := filepath.Join(strings.TrimSpace(root), "agents")
	if err := os.MkdirAll(agentsRoot, 0o700); err != nil {
		return err
	}
	entries, err := fs.ReadDir(bundledCodingTemplateAgents, "defaults/codex-agents")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		raw, err := bundledCodingTemplateAgents.ReadFile(path.Join("defaults/codex-agents", name))
		if err != nil {
			return err
		}
		dstPath := filepath.Join(agentsRoot, name)
		if _, err := os.Stat(dstPath); err == nil {
			continue
		}
		if err := os.WriteFile(dstPath, raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}
