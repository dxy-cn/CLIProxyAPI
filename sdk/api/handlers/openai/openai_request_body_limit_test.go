package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

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

func TestOpenAIImagesRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = newOversizedJSONRequest("/v1/images/generations")

	h := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	h.ImagesGenerations(c)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}
