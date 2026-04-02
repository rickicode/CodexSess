package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeCodingRunnerRole_CollapsesLegacyDualRunnerLabels(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"":         "",
		"chat":     "chat",
		"executor": "chat",
		"Executor": "chat",
		"other":    "",
	}
	for input, want := range cases {
		if got := normalizeCodingRunnerRole(input); got != want {
			t.Fatalf("normalizeCodingRunnerRole(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCodingRuntimeStatusDetail_InFlightOverridesIdle(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	sessionID := "sess_runtime_inflight"
	createCodingTestSession(t, st, cfg, sessionID)

	release, err := svc.beginCodingRun(sessionID)
	if err != nil {
		t.Fatalf("beginCodingRun failed: %v", err)
	}
	defer release()

	runtime, err := svc.CodingRuntimeStatusDetail(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("CodingRuntimeStatusDetail failed: %v", err)
	}
	if !runtime.InFlight {
		t.Fatalf("expected in_flight true")
	}
	if runtime.RunnerRole != "" {
		t.Fatalf("expected runner role to stay empty without active actor, got %q", runtime.RunnerRole)
	}
}

func TestEnsureCodingRuntimeHome_ChatIncludesCoreSuperpowers(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)
	configureRoleSkillFixtureEnv(t)
	account := seedCodingTestAccount(t, st, cry, cfg, "acc_chat_skills", "chat-skills@example.com", false)
	if _, err := svc.UseAccountCLI(context.Background(), account.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}

	home, runtimeAccount, err := svc.ensureCodingRuntimeHome(context.Background(), "sess_chat_skills", codingRuntimeRoleChat)
	if err != nil {
		t.Fatalf("ensure runtime home: %v", err)
	}
	if runtimeAccount.ID != account.ID {
		t.Fatalf("expected chat runtime to keep active cli account %q, got %q", account.ID, runtimeAccount.ID)
	}

	skillsRoot := filepath.Join(home, "skills")
	assertSkillPresent(t, skillsRoot, "using-superpowers")
	assertSkillPresent(t, skillsRoot, "systematic-debugging")
	assertSkillPresent(t, skillsRoot, "verification-before-completion")
	assertSkillAbsent(t, skillsRoot, "executing-plans")
	assertSkillAbsent(t, skillsRoot, "brainstorming")
	assertSkillAbsent(t, skillsRoot, "writing-plans")
}

func configureRoleSkillFixtureEnv(t *testing.T) {
	t.Helper()
	fixtureRoot := t.TempDir()
	skillsRoot := filepath.Join(fixtureRoot, "skills")
	skills := []string{
		"using-superpowers",
		"brainstorming",
		"writing-plans",
		"executing-plans",
		"subagent-driven-development",
		"systematic-debugging",
		"verification-before-completion",
		"using-git-worktrees",
	}
	for _, name := range skills {
		skillRoot := filepath.Join(skillsRoot, name)
		if err := os.MkdirAll(skillRoot, 0o700); err != nil {
			t.Fatalf("mkdir superpowers fixture skill %s: %v", name, err)
		}
		body := fmt.Sprintf("---\nname: %s\n---\n\n# %s\n", name, name)
		if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("write superpowers fixture skill %s: %v", name, err)
		}
	}
	t.Setenv(codingSuperpowersRepoPathEnv, fixtureRoot)
	t.Setenv(codingSuperpowersRepoURLEnv, "https://example.invalid/superpowers.git")
}

func assertSkillPresent(t *testing.T, skillsRoot, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(skillsRoot, name, "SKILL.md")); err != nil {
		t.Fatalf("expected skill %q to be present: %v", name, err)
	}
}

func assertSkillAbsent(t *testing.T, skillsRoot, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(skillsRoot, name, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("expected skill %q to be absent, got err=%v", name, err)
	}
}
