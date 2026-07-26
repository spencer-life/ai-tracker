// Package inventory discovers privacy-safe metadata about AI-agent customizations.
// It deliberately does not inspect session stores or customization bodies.
package inventory

import (
	"context"
	"time"
)

const (
	ProviderCodex  = "codex"
	ProviderClaude = "claude"
	ProviderAgy    = "agy"
)

type State string

const (
	StateDiscovered         State = "discovered"
	StateConfiguredEnabled  State = "configured_enabled"
	StateConfiguredDisabled State = "configured_disabled"
	StateEffectiveInferred  State = "effective_inferred"
	StateRuntimeObserved    State = "runtime_observed"
	StateBroken             State = "broken"
)

// Source identifies a file without exposing its full local path.
type Source struct {
	Base string `json:"base"`
	Hash string `json:"hash"`
}

// Component is one discovered customization. DisplayName is a declared name or
// basename, never content from a prompt, command, environment value, or secret.
type Component struct {
	Provider     string    `json:"provider"`
	Kind         string    `json:"kind"`
	DisplayName  string    `json:"display_name"`
	Source       Source    `json:"source"`
	Scope        string    `json:"scope"`
	State        State     `json:"state"`
	Provenance   string    `json:"provenance"`
	ShadowedBy   []string  `json:"shadowed_by,omitempty"`
	LastObserved time.Time `json:"last_observed"`
	Diagnostics  []string  `json:"diagnostics,omitempty"`
}

// Roots supplies effective provider configuration homes when they differ from
// the operating-system user home.
type Roots struct {
	CodexHome  string
	ClaudeHome string
}

// Scan inventories supported global configuration and configuration in the
// current repository ancestry. Individual malformed or unsafe sources are
// returned as broken components; error is reserved for invalid scan roots or
// cancellation.
func Scan(ctx context.Context, home, cwd string) ([]Component, error) {
	return scan(ctx, home, cwd, Roots{})
}

// ScanWithRoots inventories effective provider homes plus repository-local
// configuration. Empty roots use the conventional directories under home.
func ScanWithRoots(ctx context.Context, home, cwd string, roots Roots) ([]Component, error) {
	return scan(ctx, home, cwd, roots)
}
