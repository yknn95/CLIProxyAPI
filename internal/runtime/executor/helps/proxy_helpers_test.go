package helps

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestNewProxyAwareHTTPClientDirectBypassesGlobalProxy(t *testing.T) {
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
	client := NewProxyAwareHTTPClient(context.Background(), &config.Config{}, nil, 0)

	if _, ok := client.Transport.(*upstreamMetricsRoundTripper); !ok {
		t.Fatalf("transport type = %T, want *upstreamMetricsRoundTripper", client.Transport)
	}
}
