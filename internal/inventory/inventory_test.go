package inventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanPrivacyStatesAndShadowing(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "dev", "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustWrite(t, filepath.Join(home, ".codex", "skills", "shared", "SKILL.md"), "---\nname: shared\n---\n")
	mustMkdir(t, filepath.Join(home, ".codex", "hooks", "__pycache__"))
	mustWrite(t, filepath.Join(home, ".codex", "hooks", "active.py"), "DO-NOT-EXPOSE-HOOK-BODY")
	mustWrite(t, filepath.Join(home, ".codex", "hooks", "metadata.jsonc"), "DO-NOT-EXPOSE-METADATA")
	mustMkdir(t, filepath.Join(home, ".codex", "plugins", "cache", "cached-only"))
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), `
[mcp_servers.local]
command = "DO-NOT-EXPOSE-COMMAND"
[agents.reviewer]
prompt = "DO-NOT-EXPOSE-PROMPT"
`)
	mustWrite(t, filepath.Join(home, ".codex", "hooks.json"), `{
  "hooks": {"PostToolUse": [{"command": "DO-NOT-EXPOSE-HOOK-COMMAND"}]}
}`)
	mustWrite(t, filepath.Join(repo, ".codex", "skills", "shared", "SKILL.md"), "---\nname: shared\n---\n")
	mustWrite(t, filepath.Join(repo, ".codex", "AGENTS.md"), "DO-NOT-EXPOSE-INSTRUCTIONS")
	mustMkdir(t, filepath.Join(repo, ".claude"))
	mustWrite(t, filepath.Join(repo, ".claude", "settings.json"), `{
  "mcpServers": {"safe-server-name": {"env": {"API_KEY": "DO-NOT-EXPOSE-SECRET"}}},
  "hooks": {"PreToolUse": [{"command": "DO-NOT-EXPOSE-HOOK"}]},
  "plugins": {"off-plugin": false, "on-plugin": true},
  "disabledPlugins": {"object-disabled": {"reason": "private"}}
}`)

	items, err := Scan(context.Background(), home, repo)
	if err != nil {
		t.Fatal(err)
	}
	serialized := componentsText(items)
	for _, secret := range []string{"DO-NOT-EXPOSE", "API_KEY", "command", "prompt", string(filepath.Separator) + "dev" + string(filepath.Separator)} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("private content leaked (%q): %s", secret, serialized)
		}
	}

	assertComponent(t, items, ProviderClaude, "mcp_server", "safe-server-name", StateConfiguredEnabled)
	assertComponent(t, items, ProviderClaude, "hook", "PreToolUse", StateConfiguredEnabled)
	assertComponent(t, items, ProviderClaude, "plugin", "off-plugin", StateConfiguredDisabled)
	assertComponent(t, items, ProviderClaude, "plugin", "object-disabled", StateConfiguredDisabled)
	assertComponent(t, items, ProviderClaude, "plugin", "on-plugin", StateConfiguredEnabled)
	assertComponent(t, items, ProviderCodex, "plugin", "cached-only", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "hook", "active", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "hook", "PostToolUse", StateConfiguredEnabled)
	for _, item := range items {
		if item.Provider == ProviderCodex && item.Kind == "plugin" && item.DisplayName == "cache" && item.State == StateConfiguredEnabled {
			t.Fatal("plugin cache directory reported as enabled")
		}
		if item.Provider == ProviderCodex && item.Kind == "hook" && (item.DisplayName == "__pycache__" || item.DisplayName == "metadata") {
			t.Fatalf("non-hook directory entry reported as a hook: %#v", item)
		}
	}
	assertComponent(t, items, ProviderCodex, "instruction", "AGENTS.md", StateEffectiveInferred)

	var globalShared, projectShared *Component
	for i := range items {
		item := &items[i]
		if item.Provider == ProviderCodex && item.Kind == "skill" && item.DisplayName == "shared" {
			if item.Scope == "global" {
				globalShared = item
			} else {
				projectShared = item
			}
		}
	}
	if globalShared == nil || projectShared == nil {
		t.Fatalf("missing shadowed skills: %#v", items)
	}
	if len(globalShared.ShadowedBy) != 1 || projectShared.State != StateEffectiveInferred {
		t.Fatalf("shadowing not resolved: global=%#v project=%#v", globalShared, projectShared)
	}
}

func TestScanWithEffectiveProviderHomes(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	mustWrite(t, filepath.Join(codexHome, "skills", "active-codex", "SKILL.md"), "---\nname: active-codex\n---\n")
	mustWrite(t, filepath.Join(claudeHome, "skills", "active-claude", "SKILL.md"), "---\nname: active-claude\n---\n")
	mustWrite(t, filepath.Join(home, ".codex", "skills", "native-archive", "SKILL.md"), "---\nname: native-archive\n---\n")
	mustWrite(t, filepath.Join(home, ".codex", "agents", "native-file-agent.toml"), "model = \"private\"\n")
	mustWrite(t, filepath.Join(home, ".codex", "hooks", "native-hook.py"), "PRIVATE-HOOK-BODY")
	mustWrite(t, filepath.Join(home, ".codex", "rules", "default.rules"), "PRIVATE-RULE-BODY")
	mustMkdir(t, filepath.Join(home, ".codex", "plugins", "cache", "native-cached-plugin"))
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), "[agents.native-config-agent]\nprompt = \"private\"\n")
	mustWrite(t, filepath.Join(home, ".codex", "hooks.json"), `{"hooks":{"PostToolUse":[{"command":"private"}]}}`)
	mustWrite(t, filepath.Join(home, ".codex", "AGENTS.md"), "PRIVATE-INSTRUCTIONS")

	items, err := ScanWithRoots(context.Background(), home, repo, Roots{CodexHome: codexHome, ClaudeHome: claudeHome})
	if err != nil {
		t.Fatal(err)
	}
	assertComponent(t, items, ProviderCodex, "skill", "active-codex", StateConfiguredEnabled)
	assertComponent(t, items, ProviderClaude, "skill", "active-claude", StateConfiguredEnabled)
	assertComponent(t, items, ProviderCodex, "skill", "native-archive", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "agent", "native-file-agent", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "agent", "native-config-agent", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "hook", "native-hook", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "hook", "PostToolUse", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "rule", "default", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "plugin", "native-cached-plugin", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "instruction", "AGENTS.md", StateDiscovered)
	for _, item := range items {
		if item.Provider == ProviderCodex && item.Scope == "global-native-archive" && item.State == StateConfiguredEnabled {
			t.Fatalf("secondary native Codex component reported enabled: %#v", item)
		}
	}
}

func TestScanMarksInactiveClaudeDefinitionsDiscovered(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustWrite(t, filepath.Join(home, ".claude", "agents", "active.md"), "PRIVATE-ACTIVE-BODY")

	inactive := []struct {
		kind string
		file string
		name string
	}{
		{"agent", "cli-skill-author.md.archived", "cli-skill-author"},
		{"agent", "retired.md.disabled", "retired"},
		{"agent", "legacy.md.bak", "legacy"},
		{"rule", "old.rules.backup", "old"},
		{"rule", "temporary.rules~", "temporary"},
	}
	for _, fixture := range inactive {
		directory := fixture.kind + "s"
		mustWrite(t, filepath.Join(home, ".claude", directory, fixture.file), "PRIVATE-INACTIVE-BODY")
	}

	items, err := Scan(context.Background(), home, repo)
	if err != nil {
		t.Fatal(err)
	}
	assertComponent(t, items, ProviderClaude, "agent", "active", StateConfiguredEnabled)
	for _, fixture := range inactive {
		assertComponent(t, items, ProviderClaude, fixture.kind, fixture.name, StateDiscovered)
		for _, item := range items {
			if item.Provider == ProviderClaude && item.Kind == fixture.kind && item.Source.Base == fixture.file && item.State == StateConfiguredEnabled {
				t.Fatalf("inactive Claude definition reported enabled: %#v", item)
			}
		}
	}
}

func TestScanRejectsEscapingSymlink(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, filepath.Join(home, ".codex"))
	external := t.TempDir()
	mustMkdir(t, filepath.Join(external, "secret-skill"))
	if err := os.Symlink(external, filepath.Join(home, ".codex", "skills")); err != nil {
		t.Fatal(err)
	}

	items, err := Scan(context.Background(), home, repo)
	if err != nil {
		t.Fatal(err)
	}
	assertComponent(t, items, ProviderCodex, "skill", "skills", StateBroken)
	if strings.Contains(componentsText(items), external) {
		t.Fatal("escaped path leaked")
	}
}

func TestScanMalformedAndOversizeMetadataAreBroken(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), "{not-json")
	mustMkdir(t, filepath.Join(home, ".codex"))
	big := strings.Repeat("x", maxMetadataFile+1)
	mustWrite(t, filepath.Join(home, ".codex", "settings.json"), big)

	items, err := Scan(context.Background(), home, repo)
	if err != nil {
		t.Fatal(err)
	}
	assertComponent(t, items, ProviderClaude, "configuration", "settings.json", StateBroken)
	assertComponent(t, items, ProviderCodex, "configuration", "settings.json", StateBroken)
}

func TestJSONMetadataCountsArraysWithoutKeepingValues(t *testing.T) {
	decls, err := jsonDeclarations([]byte(`{"hooks":{"PreToolUse":[{"command":"secret"},{"command":"other"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 1 || decls[0].kind != "hook" || decls[0].name != "PreToolUse" || decls[0].count != 2 {
		t.Fatalf("unexpected declarations: %#v", decls)
	}
}

func TestJSONMetadataDoesNotPromoteNestedHookFields(t *testing.T) {
	decls, err := jsonDeclarations([]byte(`{"hooks":{"PreToolUse":[{"hooks":{"type":"command"},"command":"secret"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 1 || decls[0].kind != "hook" || decls[0].name != "PreToolUse" {
		t.Fatalf("unexpected nested declarations: %#v", decls)
	}
}

func TestScanFindsNestedAndSharedSkills(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustWrite(t, filepath.Join(home, ".codex", "skills", ".system", "system-skill", "SKILL.md"), "---\nname: system-skill\n---\n")
	mustWrite(t, filepath.Join(home, ".codex", "plugins", "cache", "vendor", "plugin", "1.0", "skills", "plugin-skill", "SKILL.md"), "---\nname: plugin-skill\n---\n")
	mustWrite(t, filepath.Join(home, ".agents", "skills", "shared-skill", "SKILL.md"), "---\nname: shared-skill\n---\n")

	items, err := Scan(context.Background(), home, repo)
	if err != nil {
		t.Fatal(err)
	}
	assertComponent(t, items, ProviderCodex, "skill", "system-skill", StateConfiguredEnabled)
	assertComponent(t, items, ProviderCodex, "skill", "plugin-skill", StateDiscovered)
	assertComponent(t, items, ProviderCodex, "skill", "shared-skill", StateConfiguredEnabled)
}

func TestScanHonorsCancellation(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, home, home); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertComponent(t *testing.T, items []Component, provider, kind, name string, state State) {
	t.Helper()
	for _, item := range items {
		if item.Provider == provider && item.Kind == kind && item.DisplayName == name && item.State == state {
			return
		}
	}
	t.Fatalf("missing component provider=%s kind=%s name=%s state=%s: %#v", provider, kind, name, state, items)
}

func componentsText(items []Component) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Provider)
		b.WriteString(item.Kind)
		b.WriteString(item.DisplayName)
		b.WriteString(item.Source.Base)
		b.WriteString(item.Source.Hash)
		b.WriteString(item.Scope)
		b.WriteString(string(item.State))
		b.WriteString(item.Provenance)
		for _, d := range item.Diagnostics {
			b.WriteString(d)
		}
	}
	return b.String()
}
