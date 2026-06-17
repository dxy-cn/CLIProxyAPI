package openai

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type repeatByteReader byte

func (r repeatByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func newOversizedJSONRequest(path string) *http.Request {
	return newJSONRequestWithContentLength(path, handlers.MaxRequestBodyBytes+1)
}

func newJSONRequestWithContentLength(path string, contentLength int64) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = contentLength
	return req
}

func TestOpenAIChatCompletionsRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = newOversizedJSONRequest("/v1/chat/completions")

	h := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	h.ChatCompletions(c)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestOpenAIResponsesAllowsCodexBodyAboveGenericLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = newOversizedJSONRequest("/v1/responses")

	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	h.Responses(c)

	if recorder.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body=%s; responses requests above the generic JSON limit should be accepted", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "request body too large") {
		t.Fatalf("body=%s; responses request should not fail at the generic JSON body limit", recorder.Body.String())
	}
}

func TestOpenAIResponsesCompactAllowsCodexBodyAboveGenericLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = newOversizedJSONRequest("/v1/responses/compact")

	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	h.Compact(c)

	if recorder.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body=%s; compact responses requests above the generic JSON limit should be accepted", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "request body too large") {
		t.Fatalf("body=%s; compact responses request should not fail at the generic JSON body limit", recorder.Body.String())
	}
}

func TestOpenAIImagesGenerationsAllowsBodyAboveGenericLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = newOversizedJSONRequest("/v1/images/generations")

	h := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	h.ImagesGenerations(c)

	if recorder.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body=%s; image generation requests above the generic JSON limit should be accepted", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "request body too large") {
		t.Fatalf("body=%s; image generation request should not fail at the generic JSON body limit", recorder.Body.String())
	}
}

func TestOpenAIImagesEditsAllowsBodyAboveGenericLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = newOversizedJSONRequest("/v1/images/edits")

	h := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	h.ImagesEdits(c)

	if recorder.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body=%s; image edit requests above the generic JSON limit should be accepted", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "request body too large") {
		t.Fatalf("body=%s; image edit request should not fail at the generic JSON body limit", recorder.Body.String())
	}
}

func TestMultipartFileToDataURLAllowsFileAboveGenericLimit(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "image.png")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := io.CopyN(part, repeatByteReader('a'), handlers.MaxRequestBodyBytes+1); err != nil {
		t.Fatalf("write multipart file failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if err := req.ParseMultipartForm(32 << 20); err != nil {
		t.Fatalf("ParseMultipartForm failed: %v", err)
	}
	files := req.MultipartForm.File["image"]
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}

	dataURL, err := multipartFileToDataURL(files[0])
	if err != nil {
		t.Fatalf("multipartFileToDataURL rejected a file above the generic limit: %v", err)
	}
	if !strings.HasPrefix(dataURL, "data:") {
		t.Fatalf("dataURL prefix = %q, want data URL", dataURL[:min(len(dataURL), 16)])
	}
}
