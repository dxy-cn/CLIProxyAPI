package access

import (
	"context"
	"net/http"
	"testing"
)

func TestManagerAuthenticateRejectsWhenNoProvidersConfigured(t *testing.T) {
	manager := NewManager()
	req, err := http.NewRequest(http.MethodGet, "/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	result, authErr := manager.Authenticate(context.Background(), req)
	if result != nil {
		t.Fatalf("unexpected auth result: %#v", result)
	}
	if authErr == nil {
		t.Fatal("expected auth error")
	}
	if authErr.Code != AuthErrorCodeNoCredentials {
		t.Fatalf("auth error code = %q, want %q", authErr.Code, AuthErrorCodeNoCredentials)
	}
	if authErr.HTTPStatusCode() != http.StatusUnauthorized {
		t.Fatalf("auth status = %d, want %d", authErr.HTTPStatusCode(), http.StatusUnauthorized)
	}
}
