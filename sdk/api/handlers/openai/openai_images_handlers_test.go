package openai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
)

func TestForwardImagesStreamEmitsErrorWhenPendingExceedsCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage)
	close(data)
	close(errs)

	var canceled error
	writeEvent := func(eventName string, dataJSON []byte) {
		if strings.TrimSpace(eventName) != "" {
			_, _ = fmt.Fprintf(c.Writer, "event: %s\n", eventName)
		}
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(dataJSON))
	}

	limit := 10 * 1024 * 1024
	firstChunk := append([]byte(`data: {"type":"response.image_generation_call.partial_image","partial_image_b64":"`), bytes.Repeat([]byte("x"), limit+1)...)
	(&OpenAIAPIHandler{}).forwardImagesStream(
		context.Background(),
		c,
		flusher,
		func(err error) { canceled = err },
		data,
		errs,
		firstChunk,
		"b64_json",
		"image_generation",
		writeEvent,
	)

	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Fatalf("expected SSE error event, got: %q", body)
	}
	if !strings.Contains(body, "responses sse pending buffer exceeded") {
		t.Fatalf("expected pending cap error, got: %q", body)
	}
	if canceled == nil {
		t.Fatal("expected stream cancellation error")
	}
}
