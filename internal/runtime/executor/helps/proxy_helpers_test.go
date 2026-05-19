package helps

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type staticRoundTripper struct{}

func (*staticRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}

func TestNewProxyAwareHTTPClientDirectBypassesGlobalProxy(t *testing.T) {
	t.Parallel()

	client := NewProxyAwareHTTPClient(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "direct"},
		0,
	)

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestNewProxyAwareHTTPClientUsesContextRoundTripperForAuthProxy(t *testing.T) {
	t.Parallel()

	expected := &staticRoundTripper{}
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(expected))

	client := NewProxyAwareHTTPClient(
		ctx,
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "http://auth-proxy.example.com:8080"},
		0,
	)

	if client.Transport != expected {
		t.Fatalf("transport = %T %p, want context roundtripper %p", client.Transport, client.Transport, expected)
	}
}

func TestNewProxyAwareHTTPClientCachesGlobalProxyTransport(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://shared-proxy.example.com:8080"}}
	clientA := NewProxyAwareHTTPClient(context.Background(), cfg, nil, 0)
	clientB := NewProxyAwareHTTPClient(context.Background(), cfg, nil, 0)

	transportA, ok := clientA.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("clientA transport type = %T, want *http.Transport", clientA.Transport)
	}
	transportB, ok := clientB.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("clientB transport type = %T, want *http.Transport", clientB.Transport)
	}
	if transportA != transportB {
		t.Fatalf("expected shared cached transport, got %p and %p", transportA, transportB)
	}
}
