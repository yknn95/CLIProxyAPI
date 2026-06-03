package executor

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestCodexDebugPayloadForLogKeepsFullPayloadWhenRequestLogEnabled(t *testing.T) {
	payload := []byte(strings.Repeat("a", 600))
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			RequestLog: true,
		},
		Debug: true,
	}

	got := codexDebugPayloadForLog(cfg, payload, 512)
	if got != string(payload) {
		t.Fatalf("codex debug payload was truncated with request-log enabled, len=%d got_suffix=%q", len(got), tailForTest(got, 20))
	}
	if strings.Contains(got, "<truncated>") {
		t.Fatalf("codex debug payload contains truncation marker: %q", tailForTest(got, 40))
	}
}

func TestCodexDebugPayloadForLogTruncatesWhenRequestLogDisabled(t *testing.T) {
	payload := []byte(strings.Repeat("a", 600))

	got := codexDebugPayloadForLog(&config.Config{Debug: true}, payload, 512)
	if len(got) <= 512 {
		t.Fatalf("truncated debug payload length = %d, want marker after limit", len(got))
	}
	if !strings.HasSuffix(got, "...<truncated>") {
		t.Fatalf("debug payload suffix = %q, want truncation marker", tailForTest(got, 20))
	}
}

func tailForTest(value string, n int) string {
	if n <= 0 || len(value) <= n {
		return value
	}
	return value[len(value)-n:]
}
