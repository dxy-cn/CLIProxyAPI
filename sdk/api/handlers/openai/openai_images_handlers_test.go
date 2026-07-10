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
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/tidwall/gjson"
)

type imagesResponsesCaptureExecutor struct {
	calls        int
	model        string
	sourceFormat string
	payload      []byte
}

func (e *imagesResponsesCaptureExecutor) Identifier() string { return "codex" }

func (e *imagesResponsesCaptureExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.calls++
	e.model = req.Model
	e.sourceFormat = opts.SourceFormat.String()
	e.payload = bytes.Clone(req.Payload)
	return coreexecutor.Response{Payload: []byte(`{"created":1704067200,"data":[{"b64_json":"aW1hZ2U="}]}`)}, nil
}

func (e *imagesResponsesCaptureExecutor) ExecuteStream(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.calls++
	e.model = req.Model
	e.sourceFormat = opts.SourceFormat.String()
	e.payload = bytes.Clone(req.Payload)

	chunks := make(chan coreexecutor.StreamChunk, 1)
	chunks <- coreexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"created_at":1704067200,"output":[{"type":"image_generation_call","result":"aW1hZ2U=","revised_prompt":"draw a cat","output_format":"png"}],"tool_usage":{"image_gen":{"input_tokens":1,"output_tokens":2}}}}` + "\n\n")}
	close(chunks)
	return &coreexecutor.StreamResult{Chunks: chunks}, nil
}

func (e *imagesResponsesCaptureExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *imagesResponsesCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, fmt.Errorf("not implemented")
}

func (e *imagesResponsesCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestImagesGenerationsDefaultGPTImage2UsesImageExecutor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor := &imagesResponsesCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth := &coreauth.Auth{
		ID:         "test-image-codex-auth",
		Provider:   executor.Identifier(),
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"plan_type": "plus"},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register auth: %v", err)
	}

	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "gpt-image-2"}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{GPTImage2BaseModel: "gpt-5.4-mini"}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/images/generations", h.ImagesGenerations)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"draw a cat"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if executor.model != "gpt-image-2" {
		t.Fatalf("model = %q, want %q", executor.model, "gpt-image-2")
	}
	if executor.sourceFormat != "openai-image" {
		t.Fatalf("source format = %q, want %q", executor.sourceFormat, "openai-image")
	}
	if got := gjson.GetBytes(executor.payload, "prompt").String(); got != "draw a cat" {
		t.Fatalf("prompt = %q, want original image prompt; payload=%s", got, string(executor.payload))
	}
	if got := gjson.GetBytes([]byte(resp.Body.String()), "data.0.b64_json").String(); got != "aW1hZ2U=" {
		t.Fatalf("b64_json = %q, want image payload; body=%s", got, resp.Body.String())
	}
}

func TestCodexImagesToolModelOnlyMatchesGPTImage2(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-image-2", want: true},
		{model: "codex/gpt-image-2", want: true},
		{model: "gpt-image-1.5", want: false},
		{model: "codex/gpt-image-1.5", want: false},
		{model: "grok-imagine-image", want: false},
	}

	for _, tt := range tests {
		if got := isCodexImagesToolModel(tt.model); got != tt.want {
			t.Fatalf("isCodexImagesToolModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestCollectImagesFromResponsesStreamReportsImageCallFailure(t *testing.T) {
	data := make(chan []byte, 1)
	errs := make(chan *interfaces.ErrorMessage)
	data <- []byte(`data: {"type":"response.completed","response":{"created_at":1704067200,"output":[{"type":"image_generation_call","status":"failed","error":{"message":"safety filter blocked the image"}}]}}` + "\n\n")
	close(data)
	close(errs)

	out, errMsg := collectImagesFromResponsesStream(context.Background(), data, errs, "b64_json")

	if out != nil {
		t.Fatalf("output = %s, want nil", out)
	}
	if errMsg == nil || errMsg.Error == nil {
		t.Fatal("expected error message")
	}
	if errMsg.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadGateway)
	}
	if got := errMsg.Error.Error(); !strings.Contains(got, "safety filter blocked the image") {
		t.Fatalf("error = %q, want upstream image failure detail", got)
	}
}

func TestCollectImagesFromResponsesStreamReportsIncompleteReason(t *testing.T) {
	data := make(chan []byte, 1)
	errs := make(chan *interfaces.ErrorMessage)
	data <- []byte(`data: {"type":"response.incomplete","response":{"created_at":1704067200,"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}}` + "\n\n")
	close(data)
	close(errs)

	out, errMsg := collectImagesFromResponsesStream(context.Background(), data, errs, "b64_json")

	if out != nil {
		t.Fatalf("output = %s, want nil", out)
	}
	if errMsg == nil || errMsg.Error == nil {
		t.Fatal("expected error message")
	}
	if errMsg.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadGateway)
	}
	if got := errMsg.Error.Error(); !strings.Contains(got, "content_filter") {
		t.Fatalf("error = %q, want incomplete reason", got)
	}
}

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
