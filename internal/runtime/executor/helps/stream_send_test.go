package helps

import (
	"context"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestSendStreamChunkReturnsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := make(chan cliproxyexecutor.StreamChunk)
	if SendStreamChunk(ctx, out, cliproxyexecutor.StreamChunk{Payload: []byte("blocked")}) {
		t.Fatalf("expected canceled send to return false")
	}
}
