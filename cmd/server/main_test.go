package main

import (
	"os"
	"strings"
	"testing"
)

func TestServerStartupDoesNotLoadGeminiOrAntigravity(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	for _, forbidden := range []string{
		"StartAntigravityVersionUpdater",
		"DoLogin",
		"DoAntigravityLogin",
		"DoKimiLogin",
		"DoXAILogin",
		"antigravity-login",
		"kimi-login",
		"xai-login",
		"Project ID (Gemini only",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("server startup must not load Gemini or Antigravity support; found %q", forbidden)
		}
	}
}
