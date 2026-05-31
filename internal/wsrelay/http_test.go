package wsrelay

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/gorilla/websocket"
)

func TestNonStreamRejectsOversizedStreamAggregate(t *testing.T) {
	connected := make(chan string, 1)
	mgr := NewManager(Options{
		Path: "/ws",
		ProviderFactory: func(*http.Request) (string, error) {
			return "test-provider", nil
		},
		OnConnected: func(provider string) {
			connected <- provider
		},
	})
	server := httptest.NewServer(mgr.Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + mgr.Path()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	select {
	case provider := <-connected:
		if provider != "test-provider" {
			t.Fatalf("provider = %q, want test-provider", provider)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket provider")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := mgr.NonStream(ctx, "test-provider", &HTTPRequest{Method: http.MethodPost, URL: "/test"})
		result <- err
	}()

	var req Message
	if err := conn.ReadJSON(&req); err != nil {
		t.Fatalf("read request message: %v", err)
	}

	if err := conn.WriteJSON(Message{
		ID:   req.ID,
		Type: MessageTypeStreamStart,
		Payload: map[string]any{
			"status": 200,
		},
	}); err != nil {
		t.Fatalf("write stream start: %v", err)
	}

	limit := 10 * 1024 * 1024
	for _, chunk := range [][]byte{
		bytes.Repeat([]byte("a"), limit/2),
		bytes.Repeat([]byte("b"), limit/2+1),
	} {
		if err := conn.WriteJSON(Message{
			ID:   req.ID,
			Type: MessageTypeStreamChunk,
			Payload: map[string]any{
				"data": string(chunk),
			},
		}); err != nil {
			t.Fatalf("write stream chunk: %v", err)
		}
	}
	_ = conn.WriteJSON(Message{ID: req.ID, Type: MessageTypeStreamEnd})

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected oversized stream aggregate error")
		}
		if !strings.Contains(err.Error(), "wsrelay: non-stream response body exceeded") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for NonStream result")
	}
}
