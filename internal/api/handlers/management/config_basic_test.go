package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestPutRoutingStrategyAcceptsSequentialFillAlias(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: round-robin\n"), 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := &config.Config{
		Routing: config.RoutingConfig{Strategy: "round-robin"},
	}
	h := NewHandler(cfg, configPath, coreauth.NewManager(nil, nil, nil))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/routing/strategy", strings.NewReader(`{"value":"sf"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PutRoutingStrategy(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PutRoutingStrategy status = %d, want %d with body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := h.cfg.Routing.Strategy; got != "sequential-fill" {
		t.Fatalf("handler config strategy = %q, want %q", got, "sequential-fill")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}
	if !strings.Contains(string(data), "sequential-fill") {
		t.Fatalf("saved config = %q, want it to contain %q", string(data), "sequential-fill")
	}
}

func TestGetConfigYAMLIncludesConfigHash(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	content := []byte("routing:\n  strategy: round-robin\n")
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, coreauth.NewManager(nil, nil, nil))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/config.yaml", nil)

	h.GetConfigYAML(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("GetConfigYAML status = %d, want %d with body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	wantHash := configContentHash(content)
	if got := rec.Header().Get(configHashHeader); got != wantHash {
		t.Fatalf("%s = %q, want %q", configHashHeader, got, wantHash)
	}
	if got := rec.Header().Get("ETag"); got != `"`+wantHash+`"` {
		t.Fatalf("ETag = %q, want quoted hash %q", got, wantHash)
	}
}

func TestPutConfigYAMLRequiresConfigHash(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	original := []byte("routing:\n  strategy: round-robin\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, coreauth.NewManager(nil, nil, nil))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/config.yaml", strings.NewReader("routing:\n  strategy: sequential-fill\n"))
	ctx.Request.Header.Set("Content-Type", "application/yaml")

	h.PutConfigYAML(ctx)

	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("PutConfigYAML status = %d, want %d with body %s", rec.Code, http.StatusPreconditionRequired, rec.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("config file changed on missing hash: %q", string(data))
	}
}

func TestPutConfigYAMLRejectsStaleConfigHash(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	loadedByEditor := []byte("routing:\n  strategy: round-robin\n")
	current := []byte("routing:\n  strategy: fill-first\n")
	if err := os.WriteFile(configPath, current, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, coreauth.NewManager(nil, nil, nil))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/config.yaml", strings.NewReader("routing:\n  strategy: sequential-fill\n"))
	ctx.Request.Header.Set("Content-Type", "application/yaml")
	ctx.Request.Header.Set(configHashHeader, configContentHash(loadedByEditor))

	h.PutConfigYAML(ctx)

	if rec.Code != http.StatusConflict {
		t.Fatalf("PutConfigYAML status = %d, want %d with body %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	if string(data) != string(current) {
		t.Fatalf("config file changed on stale hash: %q", string(data))
	}
}

func TestPutConfigYAMLRejectsWildcardIfMatch(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	original := []byte("routing:\n  strategy: round-robin\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, coreauth.NewManager(nil, nil, nil))

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/config.yaml", strings.NewReader("routing:\n  strategy: sequential-fill\n"))
	ctx.Request.Header.Set("Content-Type", "application/yaml")
	ctx.Request.Header.Set("If-Match", "*")

	h.PutConfigYAML(ctx)

	if rec.Code != http.StatusConflict {
		t.Fatalf("PutConfigYAML status = %d, want %d with body %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("config file changed on wildcard If-Match: %q", string(data))
	}
}

func TestPutConfigYAMLAcceptsMatchingConfigHash(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	original := []byte("routing:\n  strategy: round-robin\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, coreauth.NewManager(nil, nil, nil))
	next := "routing:\n  strategy: sequential-fill\n"

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/config.yaml", strings.NewReader(next))
	ctx.Request.Header.Set("Content-Type", "application/yaml")
	ctx.Request.Header.Set("If-Match", `"`+configContentHash(original)+`"`)

	h.PutConfigYAML(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PutConfigYAML status = %d, want %d with body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	if string(data) != next {
		t.Fatalf("config file = %q, want %q", string(data), next)
	}
	if got := rec.Header().Get(configHashHeader); got != configContentHash(data) {
		t.Fatalf("%s = %q, want %q", configHashHeader, got, configContentHash(data))
	}
}

func TestPutConfigYAMLDispatchesQuotaWarningsOnThresholdChange(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	original := []byte("quota-warning:\n  webhook-url: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test\n  threshold: 10\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		Label:    "codex-note",
		Metadata: map[string]any{"email": "codex-1@example.com"},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := NewHandler(&config.Config{
		QuotaWarning: config.QuotaWarning{
			WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
			Threshold:  10,
		},
	}, configPath, manager)
	h.quotaWarningQuotaFetcher = func(_ context.Context, auth *coreauth.Auth) (int, gin.H, error) {
		if auth.ID != "codex-auth" {
			t.Errorf("unexpected quota fetch auth: %s", auth.ID)
		}
		return http.StatusOK, gin.H{"rate_limit": gin.H{"primary_window": gin.H{
			"used_percent":         85,
			"limit_window_seconds": 18000,
			"reset_at":             1777777777,
		}}}, nil
	}

	sent := make(chan string, 1)
	h.quotaWarningSender = func(_ context.Context, _ string, content string) error {
		sent <- content
		return nil
	}

	next := "quota-warning:\n  webhook-url: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test\n  threshold: 20\n"
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/config.yaml", strings.NewReader(next))
	ctx.Request.Header.Set("Content-Type", "application/yaml")
	ctx.Request.Header.Set(configHashHeader, configContentHash(original))

	h.PutConfigYAML(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PutConfigYAML status = %d, want %d with body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	select {
	case content := <-sent:
		expectedReset := time.Unix(1777777777, 0).Local().Format("2006-01-02 15:04")
		if strings.Contains(content, "codex-1@example.com") {
			t.Fatalf("quota warning content must not use email as credential name: %s", content)
		}
		if !strings.Contains(content, "凭证: codex-note") ||
			!strings.Contains(content, "5小时限额: 15%") ||
			!strings.Contains(content, "重置时间: "+expectedReset) {
			t.Fatalf("unexpected quota warning content: %s", content)
		}
	case <-time.After(time.Second):
		t.Fatal("expected quota warning after threshold change")
	}
}

func TestPutConfigYAMLConcurrentSameHashAllowsOneWriter(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	original := []byte("routing:\n  strategy: round-robin\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, coreauth.NewManager(nil, nil, nil))
	hash := configContentHash(original)
	payloads := []string{
		"routing:\n  strategy: fill-first\n",
		"routing:\n  strategy: sequential-fill\n",
	}
	statuses := make(chan int, len(payloads))

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, payload := range payloads {
		payload := payload
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodPut, "/config.yaml", strings.NewReader(payload))
			ctx.Request.Header.Set("Content-Type", "application/yaml")
			ctx.Request.Header.Set(configHashHeader, hash)

			h.PutConfigYAML(ctx)
			statuses <- rec.Code
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)

	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("status counts = %#v, want one 200 and one 409", counts)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	final := string(data)
	if final != payloads[0] && final != payloads[1] {
		t.Fatalf("final config = %q, want one submitted payload", final)
	}
}
