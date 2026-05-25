package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptional_CodexHeaderDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
codex-header-defaults:
  user-agent: "  my-codex-client/1.0  "
  originator: "  Codex Desktop  "
  beta-features: "  feature-a,feature-b  "
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if got := cfg.CodexHeaderDefaults.UserAgent; got != "my-codex-client/1.0" {
		t.Fatalf("UserAgent = %q, want %q", got, "my-codex-client/1.0")
	}
	if got := cfg.CodexHeaderDefaults.Originator; got != "Codex Desktop" {
		t.Fatalf("Originator = %q, want %q", got, "Codex Desktop")
	}
	if got := cfg.CodexHeaderDefaults.BetaFeatures; got != "feature-a,feature-b" {
		t.Fatalf("BetaFeatures = %q, want %q", got, "feature-a,feature-b")
	}
}

func TestLoadConfigOptional_CodexWebsocketPool(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
codex-websocket-pool:
  enabled: true
  max-active-per-auth: 30
  max-idle-per-auth: 4
  idle-timeout: "5m"
  max-request-bytes: 16777216
  fallback-http: false
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if !cfg.CodexWebsocketPool.Enabled {
		t.Fatal("CodexWebsocketPool.Enabled = false, want true")
	}
	if got := cfg.CodexWebsocketPool.MaxActivePerAuth; got != 30 {
		t.Fatalf("MaxActivePerAuth = %d, want 30", got)
	}
	if got := cfg.CodexWebsocketPool.MaxIdlePerAuth; got != 4 {
		t.Fatalf("MaxIdlePerAuth = %d, want 4", got)
	}
	if got := cfg.CodexWebsocketPool.IdleTimeout; got != "5m" {
		t.Fatalf("IdleTimeout = %q, want 5m", got)
	}
	if got := cfg.CodexWebsocketPool.MaxRequestBytes; got != 16777216 {
		t.Fatalf("MaxRequestBytes = %d, want 16777216", got)
	}
	if cfg.CodexWebsocketPool.FallbackHTTP == nil || *cfg.CodexWebsocketPool.FallbackHTTP {
		t.Fatalf("FallbackHTTP = %#v, want false", cfg.CodexWebsocketPool.FallbackHTTP)
	}
}
