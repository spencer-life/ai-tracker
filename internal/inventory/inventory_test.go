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
	mustMkdir(t, filepath.Join(home, ".codex", "skills", "shared"))
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
	mustMkdir(t, filepath.Join(repo, ".codex", "skills", "shared"))
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
