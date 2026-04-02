package service

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	defaultCodingTemplateSuperpowersRepoURL = "https://github.com/obra/superpowers.git"
	codingSuperpowersRepoURLEnv             = "CODEXSESS_SUPERPOWERS_REPO_URL"
	codingSuperpowersRepoPathEnv            = "CODEXSESS_SUPERPOWERS_REPO_PATH"
)

var requiredCodingTemplateSuperpowersSkills = []string{
	"using-superpowers",
	"brainstorming",
	"writing-plans",
	"executing-plans",
	"subagent-driven-development",
	"systematic-debugging",
	"verification-before-completion",
	"using-git-worktrees",
}

func codingRuntimeRoleSkills(role string) []string {
	switch strings.TrimSpace(role) {
	case codingRuntimeRoleExecutor:
		return []string{
			"executing-plans",
			"systematic-debugging",
			"verification-before-completion",
		}
	default:
		return []string{
			"using-superpowers",
			"systematic-debugging",
			"verification-before-completion",
		}
	}
}

func ensureCodingTemplateSuperpowers(root string) error {
	templateRoot := strings.TrimSpace(root)
	if templateRoot == "" {
		return fmt.Errorf("template root is required")
	}
	repoRoot := filepath.Join(templateRoot, "superpowers")
	if err := ensureTemplateSuperpowersRepo(repoRoot); err != nil {
		return err
	}
	superpowersSkillsRoot, err := resolveTemplateSuperpowersSkillsRoot(repoRoot)
	if err != nil {
		return err
	}
	skillsRoot := filepath.Join(templateRoot, "skills")
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		return err
	}
	if err := wireTemplateSkillsFromSuperpowers(skillsRoot, superpowersSkillsRoot); err != nil {
		return err
	}
	missing := missingRequiredSkills(skillsRoot)
	if len(missing) > 0 {
		return fmt.Errorf("template superpowers skills missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func ensureTemplateSuperpowersRepo(repoRoot string) error {
	info, err := os.Stat(repoRoot)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("template superpowers path is not a directory: %s", repoRoot)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}

	sourcePath := strings.TrimSpace(os.Getenv(codingSuperpowersRepoPathEnv))
	if sourcePath != "" {
		if err := copyDirReplace(sourcePath, repoRoot); err != nil {
			return fmt.Errorf("install template superpowers from %s: %w", sourcePath, err)
		}
		return nil
	}

	repoURL := strings.TrimSpace(os.Getenv(codingSuperpowersRepoURLEnv))
	if repoURL == "" {
		repoURL = defaultCodingTemplateSuperpowersRepoURL
	}
	cloneCmd := exec.Command("git", "clone", "--depth", "1", repoURL, repoRoot)
	output, cloneErr := cloneCmd.CombinedOutput()
	if cloneErr != nil {
		return fmt.Errorf("clone template superpowers repo %s: %w: %s", repoURL, cloneErr, strings.TrimSpace(string(output)))
	}
	return nil
}

func resolveTemplateSuperpowersSkillsRoot(repoRoot string) (string, error) {
	candidates := []string{
		filepath.Join(repoRoot, "skills"),
		filepath.Join(repoRoot, ".codex", "skills"),
		filepath.Join(repoRoot, ".claude", "skills"),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		if missing := missingRequiredSkills(candidate); len(missing) == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("superpowers skills root missing required skills in %s", repoRoot)
}

func missingRequiredSkills(skillsRoot string) []string {
	missing := make([]string, 0, len(requiredCodingTemplateSuperpowersSkills))
	for _, name := range requiredCodingTemplateSuperpowersSkills {
		path := filepath.Join(skillsRoot, strings.TrimSpace(name), "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}

func wireTemplateSkillsFromSuperpowers(templateSkillsRoot, superpowersSkillsRoot string) error {
	required := map[string]struct{}{}
	for _, name := range requiredCodingTemplateSuperpowersSkills {
		required[strings.TrimSpace(name)] = struct{}{}
	}

	for _, name := range requiredCodingTemplateSuperpowersSkills {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		src := filepath.Join(superpowersSkillsRoot, trimmed)
		dst := filepath.Join(templateSkillsRoot, trimmed)
		if err := linkOrCopySkillDir(src, dst, true); err != nil {
			return err
		}
	}

	return nil
}

func linkOrCopySkillDir(src, dst string, replace bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("skill source is not directory: %s", src)
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		return fmt.Errorf("skill source missing SKILL.md: %s", src)
	}
	if !replace {
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
	}
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	_ = os.RemoveAll(dst)
	return copyDirReplace(src, dst)
}

func copyDirReplace(src, dst string) error {
	src = strings.TrimSpace(src)
	dst = strings.TrimSpace(dst)
	if src == "" || dst == "" {
		return fmt.Errorf("copyDirReplace requires src and dst")
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("copyDirReplace source is not a directory: %s", src)
	}
	_ = os.RemoveAll(dst)
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFile(path, target)
	})
}
