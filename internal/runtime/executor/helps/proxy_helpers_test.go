package helps

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	log "github.com/sirupsen/logrus"
)

func TestNewProxyAwareHTTPClientDirectBypassesGlobalProxy(t *testing.T) {
	previousLevel := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() { log.SetLevel(previousLevel) })

	client := NewProxyAwareHTTPClient(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "direct"},
		0,
	)

	wrapper, ok := client.Transport.(*upstreamMetricsRoundTripper)
	if !ok {
		t.Fatalf("transport type = %T, want *upstreamMetricsRoundTripper", client.Transport)
	}
	transport, ok := wrapper.base.(*http.Transport)
	if !ok {
		t.Fatalf("inner transport type = %T, want *http.Transport", wrapper.base)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestNewProxyAwareHTTPClientWrapsTransportWithUpstreamMetrics(t *testing.T) {
	previousLevel := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() { log.SetLevel(previousLevel) })

	client := NewProxyAwareHTTPClient(context.Background(), &config.Config{}, nil, 0)

	if _, ok := client.Transport.(*upstreamMetricsRoundTripper); !ok {
		t.Fatalf("transport type = %T, want *upstreamMetricsRoundTripper", client.Transport)
	}
}

func TestNewProxyAwareHTTPClientDoesNotWrapUpstreamMetricsWhenDebugDisabled(t *testing.T) {
	previousLevel := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	t.Cleanup(func() { log.SetLevel(previousLevel) })

	client := NewProxyAwareHTTPClient(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		nil,
		0,
	)

	if _, ok := client.Transport.(*upstreamMetricsRoundTripper); ok {
		t.Fatalf("transport type = %T, want unwrapped transport when debug is disabled", client.Transport)
	}
}
