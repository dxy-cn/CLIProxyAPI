package helps

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestRecordAPIRequestCapsStoredBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := context.WithValue(context.Background(), "gin", c)

	RecordAPIRequest(ctx, &config.Config{SDKConfig: config.SDKConfig{RequestLog: true}}, UpstreamRequestLog{
		URL:  "https://example.test/v1/responses",
		Body: bytes.Repeat([]byte("x"), maxAPIRequestLogBytes+1024),
	})

	value, exists := c.Get(apiRequestKey)
	if !exists {
		t.Fatal("expected API request log to be stored")
	}
	apiRequest, ok := value.([]byte)
	if !ok {
		t.Fatalf("API request type = %T, want []byte", value)
	}
	if len(apiRequest) > maxAPIRequestLogBytes {
		t.Fatalf("API request log bytes = %d, want <= %d", len(apiRequest), maxAPIRequestLogBytes)
	}
}

func TestAppendAPIResponseChunkCapsStoredBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := context.WithValue(context.Background(), "gin", c)
	cfg := &config.Config{SDKConfig: config.SDKConfig{RequestLog: true}}

	chunk := bytes.Repeat([]byte("x"), maxAPIResponseLogBytes)
	AppendAPIResponseChunk(ctx, cfg, chunk)
	AppendAPIResponseChunk(ctx, cfg, chunk)

	value, exists := c.Get(apiResponseKey)
	if !exists {
		t.Fatal("expected API response log to be stored")
	}
	apiResponse, ok := value.([]byte)
	if !ok {
		t.Fatalf("API response type = %T, want []byte", value)
	}
	if len(apiResponse) > maxAPIResponseLogBytes {
		t.Fatalf("API response log bytes = %d, want <= %d", len(apiResponse), maxAPIResponseLogBytes)
	}
}

func TestAppendAPIWebsocketTimelineCapsChunkSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	appendAPIWebsocketTimeline(c, bytes.Repeat([]byte("x"), maxAPIWebsocketTimelineChunk+1024))

	value, exists := c.Get(apiWebsocketTimelineKey)
	if !exists {
		t.Fatal("expected websocket timeline to be stored")
	}
	timeline, ok := value.([]byte)
	if !ok {
		t.Fatalf("timeline type = %T, want []byte", value)
	}
	if len(timeline) != maxAPIWebsocketTimelineChunk {
		t.Fatalf("timeline bytes = %d, want %d", len(timeline), maxAPIWebsocketTimelineChunk)
	}
}

func TestAppendAPIWebsocketTimelineCapsTotalSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	chunk := bytes.Repeat([]byte("x"), maxAPIWebsocketTimelineChunk)
	for i := 0; i < 100; i++ {
		appendAPIWebsocketTimeline(c, chunk)
	}

	value, exists := c.Get(apiWebsocketTimelineKey)
	if !exists {
		t.Fatal("expected websocket timeline to be stored")
	}
	timeline, ok := value.([]byte)
	if !ok {
		t.Fatalf("timeline type = %T, want []byte", value)
	}
	if len(timeline) > maxAPIWebsocketTimelineBytes {
		t.Fatalf("timeline bytes = %d, want <= %d", len(timeline), maxAPIWebsocketTimelineBytes)
	}
}
