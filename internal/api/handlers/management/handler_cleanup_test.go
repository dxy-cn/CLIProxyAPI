package management

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestHandlerCloseStopsAttemptCleanup(t *testing.T) {
	h := NewHandler(&config.Config{}, "", nil)
	done := h.attemptCleanupDone
	if done == nil {
		t.Fatal("expected attempt cleanup goroutine to expose a done channel")
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("attempt cleanup goroutine did not stop after Close")
	}

	if err := h.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}
