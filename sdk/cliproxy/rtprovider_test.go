package cliproxy

import (
	"net/http"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRoundTripperForDirectBypassesProxy(t *testing.T) {
	t.Parallel()

	provider := newDefaultRoundTripperProvider()
	rt := provider.RoundTripperFor(&coreauth.Auth{ProxyURL: "direct"})
	transport, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", rt)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestRoundTripperForEmptyProxyUsesSharedPooledTransport(t *testing.T) {
	t.Parallel()

	provider := newDefaultRoundTripperProvider()
	first := provider.RoundTripperFor(&coreauth.Auth{})
	second := provider.RoundTripperFor(&coreauth.Auth{})
	if first == nil {
		t.Fatal("expected shared transport, got nil")
	}
	if first != second {
		t.Fatal("expected empty proxy auths to share one transport")
	}
	transport, ok := first.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", first)
	}
	if transport.MaxIdleConnsPerHost < 500 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want at least 500", transport.MaxIdleConnsPerHost)
	}
	if transport.Proxy == nil {
		t.Fatal("expected empty proxy transport to inherit default proxy behavior")
	}
}
