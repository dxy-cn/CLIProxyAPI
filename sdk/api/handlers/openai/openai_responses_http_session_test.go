package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func resetResponsesHTTPSessionStoreForTest(t *testing.T) *responsesHTTPSessionStore {
	t.Helper()

	store := newResponsesHTTPSessionStore(responsesHTTPSessionTTL)
	previous := defaultResponsesHTTPSessionStore
	defaultResponsesHTTPSessionStore = store
	t.Cleanup(func() {
		defaultResponsesHTTPSessionStore = previous
	})
	return store
}

func responsesHTTPSessionStoreLenForTest(store *responsesHTTPSessionStore) int {
	if store == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.sessions)
}

type responsesHTTPFallbackExecutor struct {
	mu              sync.Mutex
	executeCalls    int
	executePayloads [][]byte
	streamCalls     int
	streamPayloads  [][]byte
}

func (e *responsesHTTPFallbackExecutor) Identifier() string { return "test-provider" }

func (e *responsesHTTPFallbackExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	e.mu.Lock()
	e.executeCalls++
	e.executePayloads = append(e.executePayloads, bytes.Clone(req.Payload))
	e.mu.Unlock()

	if responsesRequestUsesPreviousResponseID(req.Payload) {
		return coreexecutor.Response{}, websocketStatusErr{
			code: http.StatusBadRequest,
			msg:  `{"error":{"message":"Previous response with id 'resp-upstream-1' not found.","type":"invalid_request_error","code":"previous_response_not_found","param":"previous_response_id"}}`,
		}
	}

	responseID := "resp-upstream-1"
	assistantID := "assistant-1"
	if gjson.GetBytes(req.Payload, "input.2.id").String() == "msg-2" {
		responseID = "resp-upstream-2"
		assistantID = "assistant-2"
	}
	return coreexecutor.Response{
		Payload: []byte(fmt.Sprintf(`{"id":"%s","output":[{"type":"message","id":"%s"}]}`, responseID, assistantID)),
	}, nil
}

func (e *responsesHTTPFallbackExecutor) ExecuteStream(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.streamCalls++
	e.streamPayloads = append(e.streamPayloads, bytes.Clone(req.Payload))
	e.mu.Unlock()

	if responsesRequestUsesPreviousResponseID(req.Payload) {
		return nil, websocketStatusErr{
			code: http.StatusBadRequest,
			msg:  `{"error":{"message":"Previous response with id 'resp-upstream-1' not found.","type":"invalid_request_error","code":"previous_response_not_found","param":"previous_response_id"}}`,
		}
	}

	responseID := "resp-upstream-1"
	assistantID := "assistant-1"
	if gjson.GetBytes(req.Payload, "input.2.id").String() == "msg-2" {
		responseID = "resp-upstream-2"
		assistantID = "assistant-2"
	}

	chunks := make(chan coreexecutor.StreamChunk, 1)
	chunks <- coreexecutor.StreamChunk{
		Payload: []byte(fmt.Sprintf(
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"%s\",\"output\":[{\"type\":\"message\",\"id\":\"%s\"}]}}\n\n",
			responseID,
			assistantID,
		)),
	}
	close(chunks)
	return &coreexecutor.StreamResult{Chunks: chunks}, nil
}

func (e *responsesHTTPFallbackExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *responsesHTTPFallbackExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not implemented")
}

func (e *responsesHTTPFallbackExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func newResponsesHTTPTestHandler(t *testing.T, executor *responsesHTTPFallbackExecutor) (*OpenAIResponsesAPIHandler, *coreauth.Manager) {
	t.Helper()

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{ID: "auth-http", Provider: executor.Identifier(), Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	return NewOpenAIResponsesAPIHandler(base), manager
}

func TestResponsesHTTPDoesNotStoreSessionForClientRequestIDOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := resetResponsesHTTPSessionStoreForTest(t)

	executor := &responsesHTTPFallbackExecutor{}
	h, _ := newResponsesHTTPTestHandler(t, executor)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","input":[{"type":"message","id":"msg-1"}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Client-Request-Id", fmt.Sprintf("request-%d", i))
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d; body=%s", i, resp.Code, http.StatusOK, resp.Body.String())
		}
	}

	if got := responsesHTTPSessionStoreLenForTest(store); got != 0 {
		t.Fatalf("session store size = %d, want 0 for client request id only", got)
	}
}

func TestResponsesHTTPUsesStableTurnMetadataSessionAcrossRequestIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetResponsesHTTPSessionStoreForTest(t)

	executor := &responsesHTTPFallbackExecutor{}
	h, _ := newResponsesHTTPTestHandler(t, executor)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	firstReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","input":[{"type":"message","id":"msg-1"}]}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.Header.Set("X-Client-Request-Id", "request-1")
	firstReq.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"stable-session"}`)
	firstResp := httptest.NewRecorder()
	router.ServeHTTP(firstResp, firstReq)

	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d; body=%s", firstResp.Code, http.StatusOK, firstResp.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","previous_response_id":"resp-upstream-1","input":[{"type":"message","id":"msg-2"}]}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-Client-Request-Id", "request-2")
	secondReq.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"stable-session"}`)
	secondResp := httptest.NewRecorder()
	router.ServeHTTP(secondResp, secondReq)

	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d; body=%s", secondResp.Code, http.StatusOK, secondResp.Body.String())
	}
	if gjson.Get(secondResp.Body.String(), "error.code").Exists() {
		t.Fatalf("unexpected fallback error body: %s", secondResp.Body.String())
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.executeCalls != 3 {
		t.Fatalf("execute calls = %d, want 3", executor.executeCalls)
	}
}

func TestResponsesHTTPRetriesWithMergedTranscriptWhenPreviousResponseIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetResponsesHTTPSessionStoreForTest(t)

	executor := &responsesHTTPFallbackExecutor{}
	h, _ := newResponsesHTTPTestHandler(t, executor)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	sessionID := "session-http-fallback"
	firstReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","input":[{"type":"message","id":"msg-1"}]}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.Header.Set("X-Codex-Turn-Metadata", fmt.Sprintf(`{"session_id":%q}`, sessionID))
	firstResp := httptest.NewRecorder()
	router.ServeHTTP(firstResp, firstReq)

	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d; body=%s", firstResp.Code, http.StatusOK, firstResp.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","previous_response_id":"resp-upstream-1","input":[{"type":"message","id":"msg-2"}]}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-Codex-Turn-Metadata", fmt.Sprintf(`{"session_id":%q}`, sessionID))
	secondResp := httptest.NewRecorder()
	router.ServeHTTP(secondResp, secondReq)

	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d; body=%s", secondResp.Code, http.StatusOK, secondResp.Body.String())
	}
	if gjson.Get(secondResp.Body.String(), "error.code").Exists() {
		t.Fatalf("unexpected fallback error body: %s", secondResp.Body.String())
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.executeCalls != 3 {
		t.Fatalf("execute calls = %d, want 3", executor.executeCalls)
	}
	if len(executor.executePayloads) != 3 {
		t.Fatalf("captured execute payloads = %d, want 3", len(executor.executePayloads))
	}
	if gjson.GetBytes(executor.executePayloads[1], "previous_response_id").String() != "resp-upstream-1" {
		t.Fatalf("first retry should preserve previous_response_id: %s", executor.executePayloads[1])
	}
	fallback := executor.executePayloads[2]
	if gjson.GetBytes(fallback, "previous_response_id").Exists() {
		t.Fatalf("fallback request must not include previous_response_id: %s", fallback)
	}
	input := gjson.GetBytes(fallback, "input").Array()
	if len(input) != 3 {
		t.Fatalf("fallback input len = %d, want 3: %s", len(input), fallback)
	}
	if input[0].Get("id").String() != "msg-1" ||
		input[1].Get("id").String() != "assistant-1" ||
		input[2].Get("id").String() != "msg-2" {
		t.Fatalf("unexpected fallback transcript: %s", fallback)
	}
}

func TestResponsesHTTPStreamRetriesWithMergedTranscriptWhenPreviousResponseIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetResponsesHTTPSessionStoreForTest(t)

	executor := &responsesHTTPFallbackExecutor{}
	h, _ := newResponsesHTTPTestHandler(t, executor)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	sessionID := "session-http-stream-fallback"
	firstReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.Header.Set("X-Codex-Turn-Metadata", fmt.Sprintf(`{"session_id":%q}`, sessionID))
	firstResp := httptest.NewRecorder()
	router.ServeHTTP(firstResp, firstReq)

	if firstResp.Code != http.StatusOK {
		t.Fatalf("first stream status = %d, want %d; body=%s", firstResp.Code, http.StatusOK, firstResp.Body.String())
	}
	if !strings.Contains(firstResp.Body.String(), `"type":"response.completed"`) {
		t.Fatalf("first stream missing completed event: %s", firstResp.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"test-model","stream":true,"previous_response_id":"resp-upstream-1","input":[{"type":"message","id":"msg-2"}]}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("X-Codex-Turn-Metadata", fmt.Sprintf(`{"session_id":%q}`, sessionID))
	secondResp := httptest.NewRecorder()
	router.ServeHTTP(secondResp, secondReq)

	if secondResp.Code != http.StatusOK {
		t.Fatalf("second stream status = %d, want %d; body=%s", secondResp.Code, http.StatusOK, secondResp.Body.String())
	}
	if strings.Contains(secondResp.Body.String(), `"previous_response_not_found"`) {
		t.Fatalf("unexpected stream fallback error: %s", secondResp.Body.String())
	}
	if !strings.Contains(secondResp.Body.String(), `"type":"response.completed"`) {
		t.Fatalf("second stream missing completed event: %s", secondResp.Body.String())
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.streamCalls != 3 {
		t.Fatalf("stream calls = %d, want 3", executor.streamCalls)
	}
	if len(executor.streamPayloads) != 3 {
		t.Fatalf("captured stream payloads = %d, want 3", len(executor.streamPayloads))
	}
	if gjson.GetBytes(executor.streamPayloads[1], "previous_response_id").String() != "resp-upstream-1" {
		t.Fatalf("first stream retry should preserve previous_response_id: %s", executor.streamPayloads[1])
	}
	fallback := executor.streamPayloads[2]
	if gjson.GetBytes(fallback, "previous_response_id").Exists() {
		t.Fatalf("stream fallback request must not include previous_response_id: %s", fallback)
	}
	input := gjson.GetBytes(fallback, "input").Array()
	if len(input) != 3 {
		t.Fatalf("stream fallback input len = %d, want 3: %s", len(input), fallback)
	}
	if input[0].Get("id").String() != "msg-1" ||
		input[1].Get("id").String() != "assistant-1" ||
		input[2].Get("id").String() != "msg-2" {
		t.Fatalf("unexpected stream fallback transcript: %s", fallback)
	}
}

func TestResponsesHTTPSessionStoreSkipsOversizedPayloads(t *testing.T) {
	store := newResponsesHTTPSessionStore(responsesHTTPSessionTTL)
	store.put("session-1", []byte(`{"model":"test-model"}`), []byte(`[]`))
	if _, _, ok := store.get("session-1"); !ok {
		t.Fatal("expected small session payload to be cached")
	}

	store.put("session-1", bytes.Repeat([]byte("x"), responsesHTTPSessionMaxBytes+1), nil)
	if _, _, ok := store.get("session-1"); ok {
		t.Fatal("expected oversized session payload to evict cached session")
	}
}

func prefillResponsesHTTPSessionStoreForBenchmark(size int) *responsesHTTPSessionStore {
	store := newResponsesHTTPSessionStore(responsesHTTPSessionTTL)
	now := time.Now()
	store.nextCleanupAt = now.Add(time.Hour)
	for i := 0; i < size; i++ {
		store.sessions[fmt.Sprintf("session-%d", i)] = &responsesHTTPSessionState{
			lastSeen:         now,
			lastRequest:      []byte(`{"model":"test-model","input":[{"type":"message","id":"msg-1"}]}`),
			lastResponseBody: []byte(`[{"type":"message","id":"assistant-1"}]`),
		}
	}
	return store
}

func BenchmarkResponsesHTTPSessionStoreGetPrefilled(b *testing.B) {
	for _, size := range []int{1, 100, 1000, 5000} {
		b.Run(fmt.Sprintf("sessions_%d", size), func(b *testing.B) {
			store := prefillResponsesHTTPSessionStoreForBenchmark(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				store.get("session-0")
			}
		})
	}
}

func BenchmarkResponsesHTTPSessionStorePutPrefilled(b *testing.B) {
	for _, size := range []int{1, 100, 1000, 5000} {
		b.Run(fmt.Sprintf("sessions_%d", size), func(b *testing.B) {
			store := prefillResponsesHTTPSessionStoreForBenchmark(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				store.put("session-0", []byte(`{"model":"test-model"}`), []byte(`[]`))
			}
		})
	}
}
