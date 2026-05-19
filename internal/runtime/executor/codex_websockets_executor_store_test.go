package executor

import (
	"testing"

	"github.com/gorilla/websocket"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCodexWebsocketsExecutor_SessionStoreSurvivesExecutorReplacement(t *testing.T) {
	sessionID := "test-session-store-survives-replace"

	globalCodexWebsocketSessionStore.mu.Lock()
	delete(globalCodexWebsocketSessionStore.sessions, sessionID)
	globalCodexWebsocketSessionStore.mu.Unlock()

	exec1 := NewCodexWebsocketsExecutor(nil)
	sess1 := exec1.getOrCreateSession(sessionID)
	if sess1 == nil {
		t.Fatalf("expected session to be created")
	}

	exec2 := NewCodexWebsocketsExecutor(nil)
	sess2 := exec2.getOrCreateSession(sessionID)
	if sess2 == nil {
		t.Fatalf("expected session to be available across executors")
	}
	if sess1 != sess2 {
		t.Fatalf("expected the same session instance across executors")
	}

	exec1.CloseExecutionSession(cliproxyauth.CloseAllExecutionSessionsID)

	globalCodexWebsocketSessionStore.mu.Lock()
	_, stillPresent := globalCodexWebsocketSessionStore.sessions[sessionID]
	globalCodexWebsocketSessionStore.mu.Unlock()
	if !stillPresent {
		t.Fatalf("expected session to remain after executor replacement close marker")
	}

	exec2.CloseExecutionSession(sessionID)

	globalCodexWebsocketSessionStore.mu.Lock()
	_, presentAfterClose := globalCodexWebsocketSessionStore.sessions[sessionID]
	globalCodexWebsocketSessionStore.mu.Unlock()
	if presentAfterClose {
		t.Fatalf("expected session to be removed after explicit close")
	}
}

func TestCanReuseCodexUpstreamConnRequiresSameAuthAndURL(t *testing.T) {
	t.Parallel()

	conn := &websocket.Conn{}
	if !canReuseCodexUpstreamConn(conn, "auth-a", "wss://example.com/a", "auth-a", "wss://example.com/a") {
		t.Fatal("expected websocket session to be reusable when auth and url match")
	}
	if canReuseCodexUpstreamConn(conn, "auth-a", "wss://example.com/a", "auth-b", "wss://example.com/a") {
		t.Fatal("expected websocket session reuse to be rejected when auth changes")
	}
	if canReuseCodexUpstreamConn(conn, "auth-a", "wss://example.com/a", "auth-a", "wss://example.com/b") {
		t.Fatal("expected websocket session reuse to be rejected when upstream url changes")
	}
	if canReuseCodexUpstreamConn(nil, "auth-a", "wss://example.com/a", "auth-a", "wss://example.com/a") {
		t.Fatal("expected nil websocket conn to be non-reusable")
	}
}
