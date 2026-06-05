package cliproxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestServiceLookupBoundAuthIndexIgnoresBindingsWithoutAccountBindStrategy(t *testing.T) {
	const clientKey = "sk-client"

	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	service.rebuildBindingMap(&config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: config.FlexAPIKeyList{clientKey},
			APIKeyAuthIdentityBindings: map[string]string{
				clientKey: "codex:chatgpt:acct-bound",
			},
		},
		Routing: internalconfig.RoutingConfig{DefaultModelAccount: "codex:chatgpt:acct-default"},
	})

	if got, ok := service.LookupBoundAuthIndex(clientKey); ok || got != "" {
		t.Fatalf("binding must be inactive outside account-bind: got=%q ok=%v", got, ok)
	}
}

func TestServiceLookupBoundAuthIndexAppliesResolvedIdentityBindingsWithAccountBindStrategy(t *testing.T) {
	const clientKey = "sk-client"

	manager := coreauth.NewManager(nil, nil, nil)
	boundAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-bound",
		Provider: "codex",
		FileName: "codex-bound.json",
		Metadata: map[string]any{
			"id_token": testCodexJWT(t, "acct-bound"),
		},
	})
	if err != nil {
		t.Fatalf("register bound auth: %v", err)
	}
	defaultAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-default",
		Provider: "codex",
		FileName: "codex-default.json",
		Metadata: map[string]any{
			"id_token": testCodexJWT(t, "acct-default"),
		},
	})
	if err != nil {
		t.Fatalf("register default auth: %v", err)
	}

	service := &Service{
		cfg:         &config.Config{},
		coreManager: manager,
	}
	service.rebuildBindingMap(&config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: config.FlexAPIKeyList{clientKey},
			APIKeyAuthIdentityBindings: map[string]string{
				clientKey: "codex:chatgpt:acct-bound",
			},
		},
		Routing: internalconfig.RoutingConfig{
			Strategy:            "account-bind",
			DefaultModelAccount: "codex:chatgpt:acct-default",
		},
	})

	if got, ok := service.LookupBoundAuthIndex(clientKey); !ok || got != boundAuth.Index {
		t.Fatalf("binding not active in account-bind: got=%q ok=%v", got, ok)
	}
	if got, ok := service.LookupBoundAuthIndex("sk-other"); !ok || got != defaultAuth.Index {
		t.Fatalf("default binding not active in account-bind: got=%q ok=%v", got, ok)
	}
}

func TestServiceLookupBoundAuthIndexIgnoresLegacyAuthIndexReferencesWithAccountBindStrategy(t *testing.T) {
	const clientKey = "sk-client"

	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	service.rebuildBindingMap(&config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys:            config.FlexAPIKeyList{clientKey},
			APIKeyAuthBindings: map[string]string{clientKey: "idx-legacy"},
		},
		Routing: internalconfig.RoutingConfig{
			Strategy:            "account-bind",
			DefaultModelAccount: "idx-default-legacy",
		},
	})

	if got, ok := service.LookupBoundAuthIndex(clientKey); ok || got != "" {
		t.Fatalf("legacy per-key auth_index binding must be ignored: got=%q ok=%v", got, ok)
	}
	if got, ok := service.LookupBoundAuthIndex("sk-other"); ok || got != "" {
		t.Fatalf("legacy default-model-account auth_index must be ignored: got=%q ok=%v", got, ok)
	}
}

func testCodexJWT(t *testing.T, accountID string) string {
	t.Helper()

	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
