package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

// TestGetContextWithCancel_PropagatesBoundAuthIndex guards against the regression where
// the bound auth_index injected by the account-bind middleware is dropped when the handler
// rebuilds cliCtx from context.Background(). If this value is lost, requestExecutionMetadata
// will not populate BoundAuthIndexMetadataKey and the conductor will fall back to the global
// routing strategy instead of honoring the per-key binding.
func TestGetContextWithCancel_PropagatesBoundAuthIndex(t *testing.T) {
	const wantIdx = "2a2d14935e2bcab3"

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(WithBoundAuthIndex(req.Context(), wantIdx))
	c.Request = req

	h := &BaseAPIHandler{Cfg: &sdkconfig.SDKConfig{}}
	cliCtx, cancel := h.GetContextWithCancel(nil, c, context.Background())
	defer cancel()

	gotIdx := boundAuthIndexFromContext(cliCtx)
	if gotIdx != wantIdx {
		t.Fatalf("bound auth_index not propagated into cliCtx: got %q, want %q", gotIdx, wantIdx)
	}

	meta := requestExecutionMetadata(cliCtx)
	if v, ok := meta[coreexecutor.BoundAuthIndexMetadataKey].(string); !ok || v != wantIdx {
		t.Fatalf("requestExecutionMetadata did not carry bound auth_index: got %v, want %q", meta[coreexecutor.BoundAuthIndexMetadataKey], wantIdx)
	}
}
