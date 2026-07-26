package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type providerSpec struct {
	provider string
	dir      string
	settings []string
	markers  []string
}

var providers = []providerSpec{
	{ProviderCodex, ".codex", []string{"config.toml", "settings.json", "hooks.json"}, []string{"AGENTS.md"}},
	{ProviderClaude, ".claude", []string{"settings.json", "settings.local.json"}, []string{"CLAUDE.md"}},
	{ProviderAgy, ".gemini", []string{"settings.json", "config.toml"}, []string{"GEMINI.md"}},
	{ProviderAgy, ".agy", []string{"settings.json", "config.toml"}, nil},
}

type collector struct {
	ctx   context.Context
	items []Component
	seen  map[string]struct{}
}

func scan(ctx context.Context, home, cwd string, roots Roots) ([]Component, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	homeRoot, err := canonicalDir(home)
	if err != nil {
		return nil, fmt.Errorf("invalid home: %w", err)
	}
	cwdRoot, err := canonicalDir(cwd)
	if err != nil {
		return nil, fmt.Errorf("invalid cwd: %w", err)
	}
	c := &collector{ctx: ctx, seen: make(map[string]struct{})}

	for _, spec := range providers {
		boundary := homeRoot
		configRoot := filepath.Join(homeRoot, spec.dir)
		override := ""
		switch spec.provider {
		case ProviderCodex:
			override = roots.CodexHome
		case ProviderClaude:
			override = roots.ClaudeHome
		}
		if strings.TrimSpace(override) != "" {
			resolvedOverride, rootErr := canonicalDir(override)
			if rootErr != nil {
				return nil, fmt.Errorf("invalid %s configuration home: %w", spec.provider, rootErr)
			}
			boundary, configRoot = resolvedOverride, resolvedOverride
		}
		c.scanRoot(boundary, configRoot, spec, "global")
		if spec.provider == ProviderCodex {
			c.scanSkillTree(boundary, filepath.Join(configRoot, "plugins", "cache"), spec.provider, "plugin-cache", StateDiscovered, "plugin cache skill discovered; activation not inferred")
		}
	}
	// When Codex Desktop supplies a distinct Windows-backed CODEX_HOME to a WSL
	// process, retain visibility into native ~/.codex configuration as archived
	// discoveries without claiming it is active in the configured profile.
	nativeCodexRoot := filepath.Join(homeRoot, ".codex")
	if strings.TrimSpace(roots.CodexHome) != "" && filepath.Clean(roots.CodexHome) != filepath.Clean(nativeCodexRoot) {
		for _, spec := range providers {
			if spec.provider != ProviderCodex {
				continue
			}
			for _, name := range spec.settings {
				c.scanSettingsWithState(homeRoot, filepath.Join(nativeCodexRoot, name), ProviderCodex, "global-native-archive", StateDiscovered)
			}
			break
		}
		for _, dir := range []struct {
			path string
			kind string
		}{
			{"agents", "agent"},
			{"hooks", "hook"},
			{"rules", "rule"},
			{"plugins/cache", "plugin"},
		} {
			scope := "global-native-archive"
			if dir.kind == "plugin" {
				scope = "global-native-plugin-cache"
			}
			c.scanNamedDirectory(homeRoot, filepath.Join(nativeCodexRoot, filepath.FromSlash(dir.path)), ProviderCodex, dir.kind, scope, StateDiscovered)
		}
		c.scanSkillTree(homeRoot, filepath.Join(nativeCodexRoot, "skills"), ProviderCodex, "global-native-archive", StateDiscovered, "skill found in secondary native Codex configuration archive; activation not inferred")
		c.scanSkillTree(homeRoot, filepath.Join(nativeCodexRoot, "plugins", "cache"), ProviderCodex, "global-native-plugin-cache", StateDiscovered, "plugin cache skill found in secondary native Codex archive; activation not inferred")
		c.scanMarkerWithState(homeRoot, filepath.Join(nativeCodexRoot, "AGENTS.md"), ProviderCodex, "global-native-archive", StateDiscovered, "instruction filename found in secondary native Codex configuration; activation not inferred")
	}
	c.scanSkillTree(homeRoot, filepath.Join(homeRoot, ".agents", "skills"), ProviderCodex, "global-shared", StateConfiguredEnabled, "shared agent skill definition present")
	ancestry := repositoryAncestry(cwdRoot, homeRoot)
	for i, root := range ancestry {
		scope := "repository"
		if i > 0 {
			scope = fmt.Sprintf("repository-level-%d", i)
		}
		for _, spec := range providers {
			c.scanRoot(root, filepath.Join(root, spec.dir), spec, scope)
			for _, marker := range spec.markers {
				c.scanMarker(root, filepath.Join(root, marker), spec.provider, scope)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.applyShadowing()
	sort.Slice(c.items, func(i, j int) bool {
		a, b := c.items[i], c.items[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.DisplayName != b.DisplayName {
			return a.DisplayName < b.DisplayName
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.Source.Hash < b.Source.Hash
	})
	return c.items, nil
}

func repositoryAncestry(cwd, home string) []string {
	cur := cwd
	root := cwd
	for {
		if _, err := os.Lstat(filepath.Join(cur, ".git")); err == nil {
			root = cur
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur || (!within(home, parent) && within(home, cur)) {
			break
		}
		cur = parent
	}
	var reversed []string
	for cur = cwd; ; cur = filepath.Dir(cur) {
		reversed = append(reversed, cur)
		if cur == root {
			break
		}
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

func (c *collector) scanRoot(boundary, configRoot string, spec providerSpec, scope string) {
	if c.ctx.Err() != nil {
		return
	}
	resolved, info, err := resolveInside(boundary, configRoot)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		if _, lerr := os.Lstat(configRoot); lerr == nil {
			c.broken(spec.provider, "configuration", configRoot, scope, diagnostic(err))
		}
		return
	}
	if !info.IsDir() {
		c.broken(spec.provider, "configuration", configRoot, scope, "configuration root is not a directory")
		return
	}
	for _, name := range spec.settings {
		c.scanSettings(resolved, filepath.Join(resolved, name), spec.provider, scope)
	}
	for _, dir := range []struct {
		path, kind string
		state      State
	}{
		{"agents", "agent", StateConfiguredEnabled},
		// Hook directories often contain helpers, caches, and retired scripts.
		// Presence is useful inventory, but activation must come from settings.
		{"hooks", "hook", StateDiscovered},
		{"rules", "rule", StateConfiguredEnabled},
		{"plugins", "plugin", StateConfiguredEnabled},
		{"plugins/cache", "plugin", StateDiscovered},
	} {
		c.scanNamedDirectory(resolved, filepath.Join(resolved, filepath.FromSlash(dir.path)), spec.provider, dir.kind, scope, dir.state)
	}
	c.scanSkillTree(resolved, filepath.Join(resolved, "skills"), spec.provider, scope, StateConfiguredEnabled, "skill definition present in configured component directory")
	for _, marker := range spec.markers {
		c.scanMarker(resolved, filepath.Join(resolved, marker), spec.provider, scope)
	}
}

func (c *collector) scanSkillTree(root, path, provider, scope string, state State, provenance string) {
	resolved, info, err := resolveInside(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		if _, lerr := os.Lstat(path); lerr == nil {
			c.broken(provider, "skill", path, scope, diagnostic(err))
		}
		return
	}
	if !info.IsDir() {
		c.brokenAt(provider, "skill", path, scope, info.ModTime(), "skill root is not a directory")
		return
	}
	count := 0
	_ = filepath.WalkDir(resolved, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || c.ctx.Err() != nil {
			return filepath.SkipDir
		}
		relative, relErr := filepath.Rel(resolved, candidate)
		if relErr != nil {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if relative != "." && len(strings.Split(filepath.ToSlash(relative), "/")) > 8 {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || !strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}
		if count >= maxEntries {
			return filepath.SkipAll
		}
		resolvedSkill, skillInfo, resolveErr := resolveInside(root, candidate)
		if resolveErr != nil {
			c.broken(provider, "skill", candidate, scope, diagnostic(resolveErr))
			return nil
		}
		count++
		c.add(Component{Provider: provider, Kind: "skill", DisplayName: filepath.Base(filepath.Dir(resolvedSkill)), Source: safeSource(resolvedSkill), Scope: scope, State: state, Provenance: provenance, LastObserved: skillInfo.ModTime()})
		return nil
	})
}

func (c *collector) scanSettings(root, path, provider, scope string) {
	c.scanSettingsWithState(root, path, provider, scope, "")
}

func (c *collector) scanSettingsWithState(root, path, provider, scope string, stateOverride State) {
	data, info, err := readMetadata(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		if _, e := os.Lstat(path); e == nil {
			c.broken(provider, "configuration", path, scope, diagnostic(err))
		}
		return
	}
	var decls []declaration
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		decls, err = jsonDeclarations(data)
	} else {
		decls = tomlDeclarations(data)
	}
	if err != nil {
		c.brokenAt(provider, "configuration", path, scope, info.ModTime(), "malformed structural metadata")
		return
	}
	for _, d := range decls {
		state := StateConfiguredEnabled
		if d.enabled != nil && !*d.enabled {
			state = StateConfiguredDisabled
		}
		provenance := "declared in configuration metadata"
		if stateOverride != "" {
			state = stateOverride
			provenance = "declared in secondary configuration metadata; activation not inferred"
		}
		if d.count > 0 {
			if stateOverride != "" {
				provenance = fmt.Sprintf("secondary configuration declares collection with %d entries; activation not inferred", d.count)
			} else {
				provenance = fmt.Sprintf("declared collection with %d entries", d.count)
			}
		}
		c.add(Component{Provider: provider, Kind: d.kind, DisplayName: d.name, Source: safeSource(path), Scope: scope, State: state, Provenance: provenance, LastObserved: info.ModTime()})
	}
}

func (c *collector) scanNamedDirectory(root, path, provider, kind, scope string, state State) {
	resolved, info, err := resolveInside(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		if _, e := os.Lstat(path); e == nil {
			c.broken(provider, kind, path, scope, diagnostic(err))
		}
		return
	}
	if !info.IsDir() {
		c.brokenAt(provider, kind, path, scope, info.ModTime(), "component collection is not a directory")
		return
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		c.brokenAt(provider, kind, path, scope, info.ModTime(), "component collection is unreadable")
		return
	}
	if len(entries) > maxEntries {
		c.brokenAt(provider, kind, path, scope, info.ModTime(), "component collection exceeds entry limit")
		entries = entries[:maxEntries]
	}
	for _, entry := range entries {
		if c.ctx.Err() != nil {
			return
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if kind == "plugin" && state == StateConfiguredEnabled && strings.EqualFold(name, "cache") {
			continue
		}
		child := filepath.Join(resolved, name)
		resolvedChild, childInfo, err := resolveInside(root, child)
		if err != nil {
			c.broken(provider, kind, child, scope, diagnostic(err))
			continue
		}
		if !childInfo.IsDir() && !childInfo.Mode().IsRegular() {
			continue
		}
		if kind == "hook" {
			if childInfo.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".sh" && ext != ".py" && ext != ".js" {
				continue
			}
		}
		itemState := state
		provenance := directoryProvenance(kind, state)
		displayName := name
		if (kind == "agent" || kind == "rule") && inactiveDefinition(name) {
			itemState = StateDiscovered
			provenance = "archived, backup, or disabled definition discovered; activation not inferred"
			displayName = trimInactiveSuffix(displayName)
		}
		display := strings.TrimSuffix(displayName, filepath.Ext(displayName))
		c.add(Component{Provider: provider, Kind: kind, DisplayName: display, Source: safeSource(resolvedChild), Scope: scope, State: itemState, Provenance: provenance, LastObserved: childInfo.ModTime()})
	}
}

func inactiveDefinition(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, "~") {
		return true
	}
	for _, suffix := range []string{".archived", ".disabled", ".bak", ".backup"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func trimInactiveSuffix(name string) string {
	if strings.HasSuffix(name, "~") {
		return strings.TrimSuffix(name, "~")
	}
	lower := strings.ToLower(name)
	for _, suffix := range []string{".archived", ".disabled", ".bak", ".backup"} {
		if strings.HasSuffix(lower, suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return name
}

func directoryProvenance(kind string, state State) string {
	if state == StateDiscovered {
		if kind == "hook" {
			return "hook-directory script discovered; activation not inferred"
		}
		if kind == "plugin" {
			return "cache presence only; installation and enablement not inferred"
		}
		return "definition discovered in a secondary component directory; activation not inferred"
	}
	return "definition present in configured component directory"
}

func (c *collector) scanMarker(root, path, provider, scope string) {
	c.scanMarkerWithState(root, path, provider, scope, StateEffectiveInferred, "instruction filename observed; body not read")
}

func (c *collector) scanMarkerWithState(root, path, provider, scope string, state State, provenance string) {
	resolved, info, err := resolveInside(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		if _, e := os.Lstat(path); e == nil {
			c.broken(provider, "instruction", path, scope, diagnostic(err))
		}
		return
	}
	if !info.Mode().IsRegular() {
		c.brokenAt(provider, "instruction", path, scope, info.ModTime(), "instruction source is not a regular file")
		return
	}
	c.add(Component{Provider: provider, Kind: "instruction", DisplayName: filepath.Base(path), Source: safeSource(resolved), Scope: scope, State: state, Provenance: provenance, LastObserved: info.ModTime()})
}

func (c *collector) broken(provider, kind, path, scope, message string) {
	c.brokenAt(provider, kind, path, scope, time.Time{}, message)
}

func (c *collector) brokenAt(provider, kind, path, scope string, observed time.Time, message string) {
	c.add(Component{Provider: provider, Kind: kind, DisplayName: filepath.Base(path), Source: safeSource(path), Scope: scope, State: StateBroken, Provenance: "source rejected during privacy-safe scan", LastObserved: observed, Diagnostics: []string{message}})
}

func diagnostic(err error) string {
	if strings.Contains(err.Error(), "escapes scan root") {
		return "symlink escapes scan root"
	}
	if strings.Contains(err.Error(), "exceeds") {
		return "metadata source exceeds safety limit"
	}
	if errors.Is(err, os.ErrPermission) {
		return "source is not readable"
	}
	return "source cannot be safely inspected"
}

func (c *collector) add(item Component) {
	key := strings.Join([]string{item.Provider, item.Kind, item.DisplayName, item.Scope, item.Source.Hash, string(item.State)}, "\x00")
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.items = append(c.items, item)
}

func (c *collector) applyShadowing() {
	groups := make(map[string][]int)
	for i, item := range c.items {
		if item.State == StateBroken || item.State == StateDiscovered || item.State == StateConfiguredDisabled {
			continue
		}
		key := strings.Join([]string{item.Provider, item.Kind, strings.ToLower(item.DisplayName)}, "\x00")
		groups[key] = append(groups[key], i)
	}
	for _, indexes := range groups {
		if len(indexes) < 2 {
			continue
		}
		winner := indexes[len(indexes)-1]
		c.items[winner].State = StateEffectiveInferred
		for _, i := range indexes[:len(indexes)-1] {
			c.items[i].ShadowedBy = []string{c.items[winner].Source.Hash}
		}
	}
}
