// Package api provides the HTTP API server implementation for the CLI Proxy API.
// It includes the main server struct, routing setup, middleware for CORS and authentication,
// and integration with various AI API handlers (OpenAI, Claude, Gemini).
// The server supports hot-reloading of clients and configuration.
package api

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/access"
	managementHandlers "github.com/router-for-me/CLIProxyAPI/v7/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/api/middleware"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/home"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementasset"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/redisqueue"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usage"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/gemini"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/openai"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"gopkg.in/yaml.v3"
)

const (
	oauthCallbackSuccessHTML = `<html><head><meta charset="utf-8"><title>Authentication successful</title><script>setTimeout(function(){window.close();},5000);</script></head><body><h1>Authentication successful!</h1><p>You can close this window.</p><p>This window will close automatically in 5 seconds.</p></body></html>`
	maxInboundBodyBytes      = 64 << 20
)

type serverOptionConfig struct {
	extraMiddleware      []gin.HandlerFunc
	engineConfigurator   func(*gin.Engine)
	routerConfigurator   func(*gin.Engine, *handlers.BaseAPIHandler, *config.Config)
	requestLoggerFactory func(*config.Config, string) logging.RequestLogger
	localPassword        string
	keepAliveEnabled     bool
	keepAliveTimeout     time.Duration
	keepAliveOnTimeout   func()
	postAuthHook         auth.PostAuthHook
}

// ServerOption customises HTTP server construction.
type ServerOption func(*serverOptionConfig)

func defaultRequestLoggerFactory(cfg *config.Config, configPath string) logging.RequestLogger {
	configDir := filepath.Dir(configPath)
	logsDir := logging.ResolveLogDirectory(cfg)
	logger := logging.NewFileRequestLogger(cfg.RequestLog, logsDir, configDir, cfg.ErrorLogsMaxFiles)
	logger.SetHomeEnabled(cfg != nil && cfg.Home.Enabled)
	return logger
}

func effectiveSDKConfig(cfg *config.Config) *config.SDKConfig {
	if cfg == nil {
		return nil
	}
	sdkCfg := cfg.SDKConfig
	if cfg.CommercialMode {
		sdkCfg.RequestLog = false
	}
	return &sdkCfg
}

func limitRequestBodySize(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c != nil && c.Request != nil && c.Request.Body != nil && maxBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// WithMiddleware appends additional Gin middleware during server construction.
func WithMiddleware(mw ...gin.HandlerFunc) ServerOption {
	return func(cfg *serverOptionConfig) {
		cfg.extraMiddleware = append(cfg.extraMiddleware, mw...)
	}
}

// WithEngineConfigurator allows callers to mutate the Gin engine prior to middleware setup.
func WithEngineConfigurator(fn func(*gin.Engine)) ServerOption {
	return func(cfg *serverOptionConfig) {
		cfg.engineConfigurator = fn
	}
}

// WithRouterConfigurator appends a callback after default routes are registered.
func WithRouterConfigurator(fn func(*gin.Engine, *handlers.BaseAPIHandler, *config.Config)) ServerOption {
	return func(cfg *serverOptionConfig) {
		cfg.routerConfigurator = fn
	}
}

// WithLocalManagementPassword stores a runtime-only management password accepted for localhost requests.
func WithLocalManagementPassword(password string) ServerOption {
	return func(cfg *serverOptionConfig) {
		cfg.localPassword = password
	}
}

// WithKeepAliveEndpoint enables a keep-alive endpoint with the provided timeout and callback.
func WithKeepAliveEndpoint(timeout time.Duration, onTimeout func()) ServerOption {
	return func(cfg *serverOptionConfig) {
		if timeout <= 0 || onTimeout == nil {
			return
		}
		cfg.keepAliveEnabled = true
		cfg.keepAliveTimeout = timeout
		cfg.keepAliveOnTimeout = onTimeout
	}
}

// WithRequestLoggerFactory customises request logger creation.
func WithRequestLoggerFactory(factory func(*config.Config, string) logging.RequestLogger) ServerOption {
	return func(cfg *serverOptionConfig) {
		cfg.requestLoggerFactory = factory
	}
}

// WithPostAuthHook registers a hook to be called after auth record creation.
func WithPostAuthHook(hook auth.PostAuthHook) ServerOption {
	return func(cfg *serverOptionConfig) {
		cfg.postAuthHook = hook
	}
}

// Server represents the main API server.
// It encapsulates the Gin engine, HTTP server, handlers, and configuration.
type Server struct {
	// engine is the Gin web framework engine instance.
	engine *gin.Engine

	// server is the underlying HTTP server.
	server *http.Server

	// muxBaseListener is the shared TCP listener used to serve both HTTP and Redis protocol traffic.
	muxBaseListener net.Listener

	// muxHTTPListener receives HTTP connections selected by the multiplexer.
	muxHTTPListener *muxListener

	// muxSniffConns tracks accepted connections that are still in protocol detection.
	muxSniffMu    sync.Mutex
	muxSniffConns map[net.Conn]struct{}

	// handlers contains the API handlers for processing requests.
	handlers    *handlers.BaseAPIHandler
	authManager *auth.Manager

	// cfg holds the current server configuration.
	cfg *config.Config

	// oldConfigYaml stores a YAML snapshot of the previous configuration for change detection.
	// This prevents issues when the config object is modified in place by Management API.
	oldConfigYaml []byte

	// accessManager handles request authentication providers.
	accessManager *sdkaccess.Manager

	// requestLogger is the request logger instance for dynamic configuration updates.
	requestLogger logging.RequestLogger
	loggerToggle  func(bool)

	// configFilePath is the absolute path to the YAML config file for persistence.
	configFilePath string

	// currentPath is the absolute path to the current working directory.
	currentPath string

	// wsRoutes tracks registered websocket upgrade paths.
	wsRouteMu     sync.Mutex
	wsRoutes      map[string]struct{}
	wsAuthChanged func(bool, bool)
	wsAuthEnabled atomic.Bool

	// management handler
	mgmt *managementHandlers.Handler

	// managementRoutesRegistered tracks whether the management routes have been attached to the engine.
	managementRoutesRegistered atomic.Bool
	// managementRoutesEnabled controls whether management endpoints serve real handlers.
	managementRoutesEnabled atomic.Bool

	// envManagementSecret indicates whether MANAGEMENT_PASSWORD is configured.
	envManagementSecret bool

	localPassword string

	keepAliveEnabled   bool
	keepAliveTimeout   time.Duration
	keepAliveOnTimeout func()
	keepAliveHeartbeat chan struct{}
	keepAliveStop      chan struct{}

	// bindingMu protects bindingMap, explicitBindingKeys, and defaultBoundAuthIndex.
	bindingMu sync.RWMutex
	// bindingMap maps client API key strings to their resolved bound auth_index.
	// Non-nil only when routing.strategy is "account-bind".
	bindingMap map[string]string
	// explicitBindingKeys records client keys with configured auth_identity, even
	// when the identity cannot be resolved to a current auth_index.
	explicitBindingKeys   map[string]struct{}
	defaultBoundAuthIndex string
	// strictBinding is true when routing.strategy == "account-bind": unbound client keys are rejected.
	strictBinding bool
}

// NewServer creates and initializes a new API server instance.
// It sets up the Gin engine, middleware, routes, and handlers.
//
// Parameters:
//   - cfg: The server configuration
//   - authManager: core runtime auth manager
//   - accessManager: request authentication manager
//
// Returns:
//   - *Server: A new server instance
func NewServer(cfg *config.Config, authManager *auth.Manager, accessManager *sdkaccess.Manager, configFilePath string, opts ...ServerOption) *Server {
	optionState := &serverOptionConfig{
		requestLoggerFactory: defaultRequestLoggerFactory,
	}
	for i := range opts {
		opts[i](optionState)
	}
	// Set gin mode
	if !cfg.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create gin engine
	engine := gin.New()
	if optionState.engineConfigurator != nil {
		optionState.engineConfigurator(engine)
	}

	// Add middleware
	engine.Use(logging.GinLogrusLogger())
	engine.Use(logging.GinLogrusRecovery())
	engine.Use(middleware.FirstChunkCaptureMiddleware())
	for _, mw := range optionState.extraMiddleware {
		engine.Use(mw)
	}

	// Add request logging middleware (positioned after recovery, before auth)
	// Resolve logs directory relative to the configuration file directory.
	var requestLogger logging.RequestLogger
	var toggle func(bool)
	if !cfg.CommercialMode {
		if optionState.requestLoggerFactory != nil {
			requestLogger = optionState.requestLoggerFactory(cfg, configFilePath)
		}
		if requestLogger != nil {
			engine.Use(middleware.RequestLoggingMiddleware(requestLogger))
			if setter, ok := requestLogger.(interface{ SetEnabled(bool) }); ok {
				toggle = setter.SetEnabled
			}
		}
	}

	engine.Use(corsMiddleware())
	wd, err := os.Getwd()
	if err != nil {
		wd = configFilePath
	}

	envAdminPassword, envAdminPasswordSet := os.LookupEnv("MANAGEMENT_PASSWORD")
	envAdminPassword = strings.TrimSpace(envAdminPassword)
	envManagementSecret := envAdminPasswordSet && envAdminPassword != ""

	// Create server instance
	s := &Server{
		engine:              engine,
		handlers:            handlers.NewBaseAPIHandlers(&cfg.SDKConfig, authManager),
		authManager:         authManager,
		cfg:                 cfg,
		accessManager:       accessManager,
		requestLogger:       requestLogger,
		loggerToggle:        toggle,
		configFilePath:      configFilePath,
		currentPath:         wd,
		envManagementSecret: envManagementSecret,
		wsRoutes:            make(map[string]struct{}),
	}
	s.wsAuthEnabled.Store(cfg.WebsocketAuth)
	s.handlers.BoundAuthIndexResolver = s.resolveCurrentBoundAuthIndex
	// Save initial YAML snapshot
	s.oldConfigYaml, _ = yaml.Marshal(cfg)
	s.UpdateBindingConfig(cfg)
	engine.Use(limitRequestBodySize(maxInboundBodyBytes))
	s.applyAccessConfig(nil, cfg)
	if authManager != nil {
		authManager.SetRetryConfig(cfg.RequestRetry, time.Duration(cfg.MaxRetryInterval)*time.Second, cfg.MaxRetryCredentials)
	}
	managementasset.SetCurrentConfig(cfg)
	usage.SetStatisticsEnabled(cfg.UsageStatisticsEnabled)
	auth.SetQuotaCooldownDisabled(cfg.DisableCooling)
	applySignatureCacheConfig(nil, cfg)
	// Initialize management handler
	s.mgmt = managementHandlers.NewHandler(cfg, configFilePath, authManager)
	s.mgmt.SetConfigUpdateHook(func(updated *config.Config) {
		s.applyManagementConfigUpdate(updated)
	})
	if optionState.localPassword != "" {
		s.mgmt.SetLocalPassword(optionState.localPassword)
	}
	logDir := logging.ResolveLogDirectory(cfg)
	s.mgmt.SetLogDirectory(logDir)
	if optionState.postAuthHook != nil {
		s.mgmt.SetPostAuthHook(optionState.postAuthHook)
	}
	s.localPassword = optionState.localPassword

	// Home heartbeat gate: when home is enabled, block all endpoints with 503 until the
	// subscribe-config heartbeat connection is healthy.
	engine.Use(s.homeHeartbeatMiddleware())

	// Setup routes
	s.setupRoutes()
	s.registerPublicMonitorRoutes()

	// Apply additional router configurators from options
	if optionState.routerConfigurator != nil {
		optionState.routerConfigurator(engine, s.handlers, cfg)
	}

	// Register management routes when configuration or environment secrets are available,
	// or when a local management password is provided (e.g. TUI mode).
	hasManagementSecret := cfg.RemoteManagement.SecretKey != "" || envManagementSecret || s.localPassword != ""
	s.managementRoutesEnabled.Store(hasManagementSecret)
	redisqueue.SetEnabled(hasManagementSecret || (cfg != nil && cfg.Home.Enabled))
	if hasManagementSecret {
		s.registerManagementRoutes()
	}

	if optionState.keepAliveEnabled {
		s.enableKeepAlive(optionState.keepAliveTimeout, optionState.keepAliveOnTimeout)
	}

	// Create HTTP server
	s.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler: engine,
	}

	return s
}

func (s *Server) homeHeartbeatMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || s.cfg == nil || !s.cfg.Home.Enabled {
			c.Next()
			return
		}
		if c != nil && c.Request != nil {
			path := c.Request.URL.Path
			if strings.HasPrefix(path, "/v0/management/") || path == "/v0/management" || path == "/management.html" {
				c.Next()
				return
			}
		}
		client := home.Current()
		if client == nil || !client.HeartbeatOK() {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		c.Next()
	}
}

// setupRoutes configures the API routes for the server.
// It defines the endpoints and associates them with their respective handlers.
func (s *Server) setupRoutes() {
	healthzHandler := func(c *gin.Context) {
		if c.Request.Method == http.MethodHead {
			c.Status(http.StatusOK)
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	s.engine.GET("/healthz", healthzHandler)
	s.engine.HEAD("/healthz", healthzHandler)

	s.engine.GET("/management.html", s.serveManagementControlPanel)
	s.engine.GET("/user/monitor", s.serveManagementControlPanel)
	s.engine.GET("/user/codex-helper", s.serveManagementControlPanel)
	openaiHandlers := openai.NewOpenAIAPIHandler(s.handlers)
	geminiHandlers := gemini.NewGeminiAPIHandler(s.handlers)
	geminiCLIHandlers := gemini.NewGeminiCLIAPIHandler(s.handlers)
	claudeCodeHandlers := claude.NewClaudeCodeAPIHandler(s.handlers)
	openaiResponsesHandlers := openai.NewOpenAIResponsesAPIHandler(s.handlers)

	// OpenAI compatible API routes
	v1 := s.engine.Group("/v1")
	v1.Use(AuthMiddleware(s.accessManager))
	v1.Use(s.accountBindMiddleware())
	{
		v1.GET("/models", s.unifiedModelsHandler(openaiHandlers, claudeCodeHandlers))
		v1.POST("/chat/completions", openaiHandlers.ChatCompletions)
		v1.POST("/completions", openaiHandlers.Completions)
		v1.POST("/images/generations", openaiHandlers.ImagesGenerations)
		v1.POST("/images/edits", openaiHandlers.ImagesEdits)
		v1.POST("/videos", openaiHandlers.VideosCreate)
		v1.POST("/videos/generations", openaiHandlers.XAIVideosGenerations)
		v1.POST("/videos/edits", openaiHandlers.XAIVideosEdits)
		v1.POST("/videos/extensions", openaiHandlers.XAIVideosExtensions)
		v1.GET("/videos/:request_id", openaiHandlers.XAIVideosRetrieve)
		v1.POST("/messages", claudeCodeHandlers.ClaudeMessages)
		v1.POST("/messages/count_tokens", claudeCodeHandlers.ClaudeCountTokens)
		v1.GET("/responses", openaiResponsesHandlers.ResponsesWebsocket)
		v1.POST("/responses", openaiResponsesHandlers.Responses)
		v1.POST("/responses/compact", openaiResponsesHandlers.Compact)
	}

	// Codex CLI direct route aliases (chatgpt_base_url compatible)
	codexDirect := s.engine.Group("/backend-api/codex")
	codexDirect.Use(AuthMiddleware(s.accessManager))
	codexDirect.Use(s.accountBindMiddleware())
	{
		codexDirect.GET("/responses", openaiResponsesHandlers.ResponsesWebsocket)
		codexDirect.POST("/responses", openaiResponsesHandlers.Responses)
		codexDirect.POST("/responses/compact", openaiResponsesHandlers.Compact)
	}

	// Gemini compatible API routes
	v1beta := s.engine.Group("/v1beta")
	v1beta.Use(AuthMiddleware(s.accessManager))
	v1beta.Use(s.accountBindMiddleware())
	{
		v1beta.GET("/models", s.geminiModelsHandler(geminiHandlers))
		v1beta.POST("/models/*action", geminiHandlers.GeminiHandler)
		v1beta.GET("/models/*action", s.geminiGetHandler(geminiHandlers))
	}

	// Root endpoint
	s.engine.GET("/", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusNotFound)
	})
	s.engine.POST("/v1internal:method", geminiCLIHandlers.CLIHandler)

	// OAuth callback endpoints (reuse main server port)
	// These endpoints receive provider redirects and persist
	// the short-lived code/state for the waiting goroutine.
	s.engine.GET("/anthropic/callback", func(c *gin.Context) {
		code := c.Query("code")
		state := c.Query("state")
		errStr := c.Query("error")
		if errStr == "" {
			errStr = c.Query("error_description")
		}
		if state != "" {
			_, _ = managementHandlers.WriteOAuthCallbackFileForPendingSession(s.cfg.AuthDir, "anthropic", state, code, errStr)
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oauthCallbackSuccessHTML)
	})

	s.engine.GET("/codex/callback", func(c *gin.Context) {
		code := c.Query("code")
		state := c.Query("state")
		errStr := c.Query("error")
		if errStr == "" {
			errStr = c.Query("error_description")
		}
		if state != "" {
			_, _ = managementHandlers.WriteOAuthCallbackFileForPendingSession(s.cfg.AuthDir, "codex", state, code, errStr)
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oauthCallbackSuccessHTML)
	})

	// Management routes are registered lazily by registerManagementRoutes when a secret is configured.
}

func (s *Server) registerPublicMonitorRoutes() {
	if s == nil || s.engine == nil || s.mgmt == nil {
		return
	}

	monitor := s.engine.Group("/v0/management/public/custom/monitor")
	monitor.Use(s.mgmt.PublicMonitorAPIKeyMiddleware())
	{
		monitor.GET("/request-logs", s.mgmt.GetMonitorRequestLogs)
		monitor.GET("/kpi", s.mgmt.GetMonitorKpi)
		monitor.GET("/quota", s.mgmt.GetPublicMonitorCodexQuota)
		monitor.GET("/model-prices", s.mgmt.GetModelPrices)
		monitor.GET("/model-distribution", s.mgmt.GetMonitorModelDistribution)
		monitor.GET("/daily-trend", s.mgmt.GetMonitorDailyTrend)
		monitor.GET("/daily-model-tokens", s.mgmt.GetMonitorDailyModelTokens)
		monitor.GET("/hourly-model-tokens", s.mgmt.GetMonitorHourlyModelTokens)
		monitor.GET("/hourly-tokens", s.mgmt.GetMonitorHourlyTokens)
		monitor.GET("/key-token-stats", s.mgmt.GetMonitorKeyTokenStats)
	}
}

// AttachWebsocketRoute registers a websocket upgrade handler on the primary Gin engine.
// The handler is served as-is without additional middleware beyond the standard stack already configured.
func (s *Server) AttachWebsocketRoute(path string, handler http.Handler) {
	if s == nil || s.engine == nil || handler == nil {
		return
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		trimmed = "/v1/ws"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	s.wsRouteMu.Lock()
	if _, exists := s.wsRoutes[trimmed]; exists {
		s.wsRouteMu.Unlock()
		return
	}
	s.wsRoutes[trimmed] = struct{}{}
	s.wsRouteMu.Unlock()

	authMiddleware := AuthMiddleware(s.accessManager)
	conditionalAuth := func(c *gin.Context) {
		if !s.wsAuthEnabled.Load() {
			c.Next()
			return
		}
		authMiddleware(c)
	}
	finalHandler := func(c *gin.Context) {
		handler.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}

	s.engine.GET(trimmed, conditionalAuth, finalHandler)
}

func (s *Server) registerManagementRoutes() {
	if s == nil || s.engine == nil || s.mgmt == nil {
		return
	}
	if !s.managementRoutesRegistered.CompareAndSwap(false, true) {
		return
	}

	log.Info("management routes registered after secret key configuration")

	mgmt := s.engine.Group("/v0/management")
	mgmt.Use(s.managementAvailabilityMiddleware(), s.mgmt.Middleware())
	{
		mgmt.GET("/usage", s.mgmt.GetUsageStatistics)
		mgmt.GET("/custom/monitor/request-logs", s.mgmt.GetMonitorRequestLogs)
		mgmt.GET("/custom/monitor/channel-stats", s.mgmt.GetMonitorChannelStats)
		mgmt.GET("/custom/monitor/failure-analysis", s.mgmt.GetMonitorFailureAnalysis)
		mgmt.GET("/custom/monitor/kpi", s.mgmt.GetMonitorKpi)
		mgmt.GET("/custom/monitor/model-distribution", s.mgmt.GetMonitorModelDistribution)
		mgmt.GET("/custom/monitor/daily-trend", s.mgmt.GetMonitorDailyTrend)
		mgmt.GET("/custom/monitor/daily-model-tokens", s.mgmt.GetMonitorDailyModelTokens)
		mgmt.GET("/custom/monitor/hourly-models", s.mgmt.GetMonitorHourlyModels)
		mgmt.GET("/custom/monitor/hourly-model-tokens", s.mgmt.GetMonitorHourlyModelTokens)
		mgmt.GET("/custom/monitor/hourly-tokens", s.mgmt.GetMonitorHourlyTokens)
		mgmt.GET("/custom/monitor/hourly-performance", s.mgmt.GetMonitorHourlyPerformance)
		mgmt.GET("/custom/monitor/service-health", s.mgmt.GetMonitorServiceHealth)
		mgmt.GET("/custom/monitor/key-stats", s.mgmt.GetMonitorKeyStats)
		mgmt.GET("/custom/monitor/key-token-stats", s.mgmt.GetMonitorKeyTokenStats)
		mgmt.GET("/custom/monitor/request-details", s.mgmt.GetMonitorRequestDetails)
		mgmt.GET("/usage/export", s.mgmt.ExportUsageStatistics)
		mgmt.POST("/usage/import", s.mgmt.ImportUsageStatistics)
		mgmt.GET("/config", s.mgmt.GetConfig)
		mgmt.GET("/config.yaml", s.mgmt.GetConfigYAML)
		mgmt.GET("/model-prices", s.mgmt.GetModelPrices)
		mgmt.PUT("/config.yaml", s.mgmt.PutConfigYAML)
		mgmt.GET("/latest-version", s.mgmt.GetLatestVersion)

		mgmt.GET("/debug", s.mgmt.GetDebug)
		mgmt.PUT("/debug", s.mgmt.PutDebug)
		mgmt.PATCH("/debug", s.mgmt.PutDebug)

		mgmt.GET("/logging-to-file", s.mgmt.GetLoggingToFile)
		mgmt.PUT("/logging-to-file", s.mgmt.PutLoggingToFile)
		mgmt.PATCH("/logging-to-file", s.mgmt.PutLoggingToFile)

		mgmt.GET("/logs-max-total-size-mb", s.mgmt.GetLogsMaxTotalSizeMB)
		mgmt.PUT("/logs-max-total-size-mb", s.mgmt.PutLogsMaxTotalSizeMB)
		mgmt.PATCH("/logs-max-total-size-mb", s.mgmt.PutLogsMaxTotalSizeMB)

		mgmt.GET("/error-logs-max-files", s.mgmt.GetErrorLogsMaxFiles)
		mgmt.PUT("/error-logs-max-files", s.mgmt.PutErrorLogsMaxFiles)
		mgmt.PATCH("/error-logs-max-files", s.mgmt.PutErrorLogsMaxFiles)

		mgmt.GET("/usage-statistics-enabled", s.mgmt.GetUsageStatisticsEnabled)
		mgmt.PUT("/usage-statistics-enabled", s.mgmt.PutUsageStatisticsEnabled)
		mgmt.PATCH("/usage-statistics-enabled", s.mgmt.PutUsageStatisticsEnabled)

		mgmt.GET("/proxy-url", s.mgmt.GetProxyURL)
		mgmt.PUT("/proxy-url", s.mgmt.PutProxyURL)
		mgmt.PATCH("/proxy-url", s.mgmt.PutProxyURL)
		mgmt.DELETE("/proxy-url", s.mgmt.DeleteProxyURL)

		mgmt.POST("/api-call", s.mgmt.APICall)

		mgmt.GET("/quota-exceeded/switch-project", s.mgmt.GetSwitchProject)
		mgmt.PUT("/quota-exceeded/switch-project", s.mgmt.PutSwitchProject)
		mgmt.PATCH("/quota-exceeded/switch-project", s.mgmt.PutSwitchProject)

		mgmt.GET("/quota-exceeded/switch-preview-model", s.mgmt.GetSwitchPreviewModel)
		mgmt.PUT("/quota-exceeded/switch-preview-model", s.mgmt.PutSwitchPreviewModel)
		mgmt.PATCH("/quota-exceeded/switch-preview-model", s.mgmt.PutSwitchPreviewModel)

		mgmt.GET("/api-keys", s.mgmt.GetAPIKeys)
		mgmt.PUT("/api-keys", s.mgmt.PutAPIKeys)
		mgmt.PATCH("/api-keys", s.mgmt.PatchAPIKeys)
		mgmt.DELETE("/api-keys", s.mgmt.DeleteAPIKeys)
		mgmt.GET("/api-key-usage", s.mgmt.GetAPIKeyUsage)
		mgmt.GET("/usage-queue", s.mgmt.GetUsageQueue)

		mgmt.GET("/gemini-api-key", s.mgmt.GetGeminiKeys)
		mgmt.PUT("/gemini-api-key", s.mgmt.PutGeminiKeys)
		mgmt.PATCH("/gemini-api-key", s.mgmt.PatchGeminiKey)
		mgmt.DELETE("/gemini-api-key", s.mgmt.DeleteGeminiKey)

		mgmt.GET("/logs", s.mgmt.GetLogs)
		mgmt.DELETE("/logs", s.mgmt.DeleteLogs)
		mgmt.GET("/request-error-logs", s.mgmt.GetRequestErrorLogs)
		mgmt.GET("/request-error-logs/:name", s.mgmt.DownloadRequestErrorLog)
		mgmt.GET("/request-log-by-id/:id", s.mgmt.GetRequestLogByID)
		mgmt.GET("/request-log", s.mgmt.GetRequestLog)
		mgmt.PUT("/request-log", s.mgmt.PutRequestLog)
		mgmt.PATCH("/request-log", s.mgmt.PutRequestLog)
		mgmt.GET("/ws-auth", s.mgmt.GetWebsocketAuth)
		mgmt.PUT("/ws-auth", s.mgmt.PutWebsocketAuth)
		mgmt.PATCH("/ws-auth", s.mgmt.PutWebsocketAuth)

		mgmt.GET("/request-retry", s.mgmt.GetRequestRetry)
		mgmt.PUT("/request-retry", s.mgmt.PutRequestRetry)
		mgmt.PATCH("/request-retry", s.mgmt.PutRequestRetry)
		mgmt.GET("/max-retry-interval", s.mgmt.GetMaxRetryInterval)
		mgmt.PUT("/max-retry-interval", s.mgmt.PutMaxRetryInterval)
		mgmt.PATCH("/max-retry-interval", s.mgmt.PutMaxRetryInterval)

		mgmt.GET("/force-model-prefix", s.mgmt.GetForceModelPrefix)
		mgmt.PUT("/force-model-prefix", s.mgmt.PutForceModelPrefix)
		mgmt.PATCH("/force-model-prefix", s.mgmt.PutForceModelPrefix)

		mgmt.GET("/routing/strategy", s.mgmt.GetRoutingStrategy)
		mgmt.PUT("/routing/strategy", s.mgmt.PutRoutingStrategy)
		mgmt.PATCH("/routing/strategy", s.mgmt.PutRoutingStrategy)

		mgmt.GET("/claude-api-key", s.mgmt.GetClaudeKeys)
		mgmt.PUT("/claude-api-key", s.mgmt.PutClaudeKeys)
		mgmt.PATCH("/claude-api-key", s.mgmt.PatchClaudeKey)
		mgmt.DELETE("/claude-api-key", s.mgmt.DeleteClaudeKey)

		mgmt.GET("/codex-api-key", s.mgmt.GetCodexKeys)
		mgmt.PUT("/codex-api-key", s.mgmt.PutCodexKeys)
		mgmt.PATCH("/codex-api-key", s.mgmt.PatchCodexKey)
		mgmt.DELETE("/codex-api-key", s.mgmt.DeleteCodexKey)

		mgmt.GET("/oauth-excluded-models", s.mgmt.GetOAuthExcludedModels)
		mgmt.PUT("/oauth-excluded-models", s.mgmt.PutOAuthExcludedModels)
		mgmt.PATCH("/oauth-excluded-models", s.mgmt.PatchOAuthExcludedModels)
		mgmt.DELETE("/oauth-excluded-models", s.mgmt.DeleteOAuthExcludedModels)

		mgmt.GET("/oauth-model-alias", s.mgmt.GetOAuthModelAlias)
		mgmt.PUT("/oauth-model-alias", s.mgmt.PutOAuthModelAlias)
		mgmt.PATCH("/oauth-model-alias", s.mgmt.PatchOAuthModelAlias)
		mgmt.DELETE("/oauth-model-alias", s.mgmt.DeleteOAuthModelAlias)

		mgmt.GET("/auth-files", s.mgmt.ListAuthFiles)
		mgmt.GET("/auth-files/models", s.mgmt.GetAuthFileModels)
		mgmt.GET("/model-definitions/:channel", s.mgmt.GetStaticModelDefinitions)
		mgmt.GET("/auth-files/download", s.mgmt.DownloadAuthFile)
		mgmt.POST("/auth-files", s.mgmt.UploadAuthFile)
		mgmt.DELETE("/auth-files", s.mgmt.DeleteAuthFile)
		mgmt.PATCH("/auth-files/status", s.mgmt.PatchAuthFileStatus)
		mgmt.PATCH("/auth-files/fields", s.mgmt.PatchAuthFileFields)
		mgmt.POST("/auth-files/rebalance", s.mgmt.RebalanceAPIKeys)

		mgmt.GET("/anthropic-auth-url", s.mgmt.RequestAnthropicToken)
		mgmt.GET("/codex-auth-url", s.mgmt.RequestCodexToken)
		mgmt.POST("/oauth-callback", s.mgmt.PostOAuthCallback)
		mgmt.GET("/get-auth-status", s.mgmt.GetAuthStatus)
	}
}

func (s *Server) managementAvailabilityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || s.cfg == nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if s.cfg.Home.Enabled {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if !s.managementRoutesEnabled.Load() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

func (s *Server) serveManagementControlPanel(c *gin.Context) {
	cfg := s.cfg
	if cfg == nil || cfg.Home.Enabled || cfg.RemoteManagement.DisableControlPanel {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	filePath := managementasset.FilePath(s.configFilePath)
	if strings.TrimSpace(filePath) == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			// Synchronously ensure management.html is available with a detached context.
			// Control panel bootstrap should not be canceled by client disconnects.
			if !managementasset.EnsureLatestManagementHTML(context.Background(), managementasset.StaticDir(s.configFilePath), cfg.ProxyURL, cfg.RemoteManagement.PanelReleaseURL) {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
		} else {
			log.WithError(err).Error("failed to stat management control panel asset")
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	c.File(filePath)
}

func (s *Server) enableKeepAlive(timeout time.Duration, onTimeout func()) {
	if timeout <= 0 || onTimeout == nil {
		return
	}

	s.keepAliveEnabled = true
	s.keepAliveTimeout = timeout
	s.keepAliveOnTimeout = onTimeout
	s.keepAliveHeartbeat = make(chan struct{}, 1)
	s.keepAliveStop = make(chan struct{}, 1)

	s.engine.GET("/keep-alive", s.handleKeepAlive)

	go s.watchKeepAlive()
}

func (s *Server) handleKeepAlive(c *gin.Context) {
	if s.localPassword != "" {
		provided := strings.TrimSpace(c.GetHeader("Authorization"))
		if provided != "" {
			parts := strings.SplitN(provided, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
				provided = parts[1]
			}
		}
		if provided == "" {
			provided = strings.TrimSpace(c.GetHeader("X-Local-Password"))
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.localPassword)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
			return
		}
	}

	s.signalKeepAlive()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) signalKeepAlive() {
	if !s.keepAliveEnabled {
		return
	}
	select {
	case s.keepAliveHeartbeat <- struct{}{}:
	default:
	}
}

func (s *Server) watchKeepAlive() {
	if !s.keepAliveEnabled {
		return
	}

	timer := time.NewTimer(s.keepAliveTimeout)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			log.Warnf("keep-alive endpoint idle for %s, shutting down", s.keepAliveTimeout)
			if s.keepAliveOnTimeout != nil {
				s.keepAliveOnTimeout()
			}
			return
		case <-s.keepAliveHeartbeat:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(s.keepAliveTimeout)
		case <-s.keepAliveStop:
			return
		}
	}
}

// unifiedModelsHandler creates a unified handler for the /v1/models endpoint
// that routes to different handlers based on the User-Agent header.
// If User-Agent starts with "claude-cli", it routes to Claude handler,
// otherwise it routes to OpenAI handler.
func (s *Server) unifiedModelsHandler(openaiHandler *openai.OpenAIAPIHandler, claudeHandler *claude.ClaudeCodeAPIHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := c.Request.URL.Query()["client_version"]; ok {
			if s != nil && s.cfg != nil && s.cfg.Home.Enabled {
				s.handleHomeCodexClientModels(c)
				return
			}
			openaiHandler.OpenAIModels(c)
			return
		}

		if s != nil && s.cfg != nil && s.cfg.Home.Enabled {
			s.handleHomeModels(c)
			return
		}

		userAgent := c.GetHeader("User-Agent")

		// Route to Claude handler if User-Agent starts with "claude-cli"
		if strings.HasPrefix(userAgent, "claude-cli") {
			// log.Debugf("Routing /v1/models to Claude handler for User-Agent: %s", userAgent)
			claudeHandler.ClaudeModels(c)
		} else {
			// log.Debugf("Routing /v1/models to OpenAI handler for User-Agent: %s", userAgent)
			openaiHandler.OpenAIModels(c)
		}
	}
}

func (s *Server) handleHomeCodexClientModels(c *gin.Context) {
	entries, ok := s.loadHomeModelEntries(c)
	if !ok {
		return
	}

	models := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		model := map[string]any{
			"id":     entry.id,
			"object": "model",
		}
		if entry.created > 0 {
			model["created"] = entry.created
		}
		if entry.ownedBy != "" {
			model["owned_by"] = entry.ownedBy
		}
		if entry.displayName != "" {
			model["display_name"] = entry.displayName
			model["description"] = entry.displayName
		}
		models = append(models, model)
	}

	c.JSON(http.StatusOK, openai.CodexClientModelsResponse(models))
}

func (s *Server) geminiModelsHandler(geminiHandler *gemini.GeminiAPIHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s != nil && s.cfg != nil && s.cfg.Home.Enabled {
			s.handleHomeGeminiModels(c)
			return
		}

		geminiHandler.GeminiModels(c)
	}
}

func (s *Server) geminiGetHandler(geminiHandler *gemini.GeminiAPIHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s != nil && s.cfg != nil && s.cfg.Home.Enabled {
			s.handleHomeGeminiModel(c)
			return
		}

		geminiHandler.GeminiGetHandler(c)
	}
}

type homeModelEntry struct {
	id          string
	created     int64
	ownedBy     string
	displayName string
}

func (s *Server) handleHomeModels(c *gin.Context) {
	entries, ok := s.loadHomeModelEntries(c)
	if !ok {
		return
	}

	userAgent := c.GetHeader("User-Agent")
	isClaude := strings.HasPrefix(userAgent, "claude-cli")

	if isClaude {
		out := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			model := map[string]any{
				"id":       entry.id,
				"object":   "model",
				"owned_by": entry.ownedBy,
			}
			if entry.created > 0 {
				model["created_at"] = entry.created
			}
			if entry.displayName != "" {
				model["display_name"] = entry.displayName
			}
			out = append(out, model)
		}
		firstID := ""
		lastID := ""
		if len(out) > 0 {
			if id, okID := out[0]["id"].(string); okID {
				firstID = id
			}
			if id, okID := out[len(out)-1]["id"].(string); okID {
				lastID = id
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"data":     out,
			"has_more": false,
			"first_id": firstID,
			"last_id":  lastID,
		})
		return
	}

	filtered := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		model := map[string]any{
			"id":     entry.id,
			"object": "model",
		}
		if entry.created > 0 {
			model["created"] = entry.created
		}
		if entry.ownedBy != "" {
			model["owned_by"] = entry.ownedBy
		}
		filtered = append(filtered, model)
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   filtered,
	})
}

func (s *Server) handleHomeGeminiModels(c *gin.Context) {
	entries, ok := s.loadHomeModelEntries(c)
	if !ok {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"models": formatHomeGeminiModels(entries),
	})
}

func (s *Server) handleHomeGeminiModel(c *gin.Context) {
	entries, ok := s.loadHomeModelEntries(c)
	if !ok {
		return
	}

	action := strings.TrimPrefix(c.Param("action"), "/")
	action = strings.TrimSpace(action)
	for _, entry := range entries {
		if homeGeminiModelMatches(entry, action) {
			c.JSON(http.StatusOK, formatHomeGeminiModel(entry))
			return
		}
	}

	c.JSON(http.StatusNotFound, handlers.ErrorResponse{
		Error: handlers.ErrorDetail{
			Message: "Not Found",
			Type:    "not_found",
		},
	})
}

func (s *Server) loadHomeModelEntries(c *gin.Context) ([]homeModelEntry, bool) {
	if s == nil || c == nil || c.Request == nil {
		return nil, false
	}
	client := home.Current()
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "home control center unavailable",
				Type:    "server_error",
			},
		})
		return nil, false
	}

	raw, errGet := client.GetModels(c.Request.Context())
	if errGet != nil {
		c.JSON(http.StatusBadGateway, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: errGet.Error(),
				Type:    "server_error",
			},
		})
		return nil, false
	}

	entries, errDecode := decodeHomeModels(raw)
	if errDecode != nil {
		c.JSON(http.StatusBadGateway, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: errDecode.Error(),
				Type:    "server_error",
			},
		})
		return nil, false
	}

	return entries, true
}

func formatHomeGeminiModels(entries []homeModelEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, formatHomeGeminiModel(entry))
	}
	return out
}

func formatHomeGeminiModel(entry homeModelEntry) map[string]any {
	name := entry.id
	if !strings.HasPrefix(name, "models/") {
		name = "models/" + name
	}
	displayName := entry.displayName
	if displayName == "" {
		displayName = entry.id
	}
	return map[string]any{
		"name":                       name,
		"displayName":                displayName,
		"description":                displayName,
		"supportedGenerationMethods": []string{"generateContent"},
	}
}

func homeGeminiModelMatches(entry homeModelEntry, action string) bool {
	id := strings.TrimSpace(entry.id)
	if id == "" || action == "" {
		return false
	}
	normalizedAction := strings.TrimPrefix(action, "models/")
	normalizedID := strings.TrimPrefix(id, "models/")
	return action == id || action == "models/"+id || normalizedAction == normalizedID
}

func decodeHomeModels(raw []byte) ([]homeModelEntry, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("home models payload is empty")
	}

	var bySection map[string][]map[string]any
	if err := json.Unmarshal(raw, &bySection); err != nil {
		return nil, fmt.Errorf("parse home models payload: %w", err)
	}
	if len(bySection) == 0 {
		return nil, fmt.Errorf("home models payload has no sections")
	}

	seen := make(map[string]struct{})
	out := make([]homeModelEntry, 0, 256)
	for _, models := range bySection {
		for _, model := range models {
			id, _ := model["id"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				name, _ := model["name"].(string)
				name = strings.TrimSpace(name)
				id = strings.TrimPrefix(name, "models/")
			}
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}

			created := int64(0)
			switch v := model["created"].(type) {
			case float64:
				created = int64(v)
			case int64:
				created = v
			case int:
				created = int64(v)
			case json.Number:
				if n, err := v.Int64(); err == nil {
					created = n
				}
			}

			ownedBy, _ := model["owned_by"].(string)
			ownedBy = strings.TrimSpace(ownedBy)
			displayName, _ := model["display_name"].(string)
			displayName = strings.TrimSpace(displayName)
			if displayName == "" {
				displayName, _ = model["displayName"].(string)
				displayName = strings.TrimSpace(displayName)
			}

			out = append(out, homeModelEntry{
				id:          id,
				created:     created,
				ownedBy:     ownedBy,
				displayName: displayName,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	if len(out) == 0 {
		return nil, fmt.Errorf("home models payload contains no models")
	}
	return out, nil
}

// Start begins listening for and serving HTTP or HTTPS requests.
// It's a blocking call and will only return on an unrecoverable error.
//
// Returns:
//   - error: An error if the server fails to start
func (s *Server) Start() error {
	if s == nil || s.server == nil {
		return fmt.Errorf("failed to start HTTP server: server not initialized")
	}

	addr := s.server.Addr
	listener, errListen := net.Listen("tcp", addr)
	if errListen != nil {
		return fmt.Errorf("failed to start HTTP server: %v", errListen)
	}

	useTLS := s.cfg != nil && s.cfg.TLS.Enable
	if useTLS {
		certPath := strings.TrimSpace(s.cfg.TLS.Cert)
		keyPath := strings.TrimSpace(s.cfg.TLS.Key)
		if certPath == "" || keyPath == "" {
			if errClose := listener.Close(); errClose != nil {
				log.Errorf("failed to close listener after TLS validation failure: %v", errClose)
			}
			return fmt.Errorf("failed to start HTTPS server: tls.cert or tls.key is empty")
		}
		certPair, errLoad := tls.LoadX509KeyPair(certPath, keyPath)
		if errLoad != nil {
			if errClose := listener.Close(); errClose != nil {
				log.Errorf("failed to close listener after TLS key pair load failure: %v", errClose)
			}
			return fmt.Errorf("failed to start HTTPS server: %v", errLoad)
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{certPair},
			NextProtos:   []string{"h2", "http/1.1"},
		}
		s.server.TLSConfig = tlsConfig
		if errHTTP2 := http2.ConfigureServer(s.server, &http2.Server{}); errHTTP2 != nil {
			log.Warnf("failed to configure HTTP/2: %v", errHTTP2)
		}
		listener = tls.NewListener(listener, tlsConfig)
		log.Debugf("Starting API server on %s with TLS", addr)
	} else {
		log.Debugf("Starting API server on %s", addr)
	}

	httpListener := newMuxListener(listener.Addr(), 1024)
	s.muxBaseListener = listener
	s.muxHTTPListener = httpListener

	httpErrCh := make(chan error, 1)
	acceptErrCh := make(chan error, 1)

	go func() {
		httpErrCh <- s.server.Serve(httpListener)
	}()
	go func() {
		acceptErrCh <- s.acceptMuxConnections(listener, httpListener)
	}()

	select {
	case errServe := <-httpErrCh:
		if s.muxBaseListener != nil {
			if errClose := s.muxBaseListener.Close(); errClose != nil && !errors.Is(errClose, net.ErrClosed) {
				log.Debugf("failed to close shared listener after HTTP serve exit: %v", errClose)
			}
		}
		if s.muxHTTPListener != nil {
			_ = s.muxHTTPListener.Close()
		}
		errAccept := <-acceptErrCh
		errServe = normalizeHTTPServeError(errServe)
		errAccept = normalizeListenerError(errAccept)
		if errServe != nil {
			return fmt.Errorf("failed to start HTTP server: %v", errServe)
		}
		if errAccept != nil {
			return fmt.Errorf("failed to start HTTP server: %v", errAccept)
		}
		return nil
	case errAccept := <-acceptErrCh:
		if s.muxHTTPListener != nil {
			_ = s.muxHTTPListener.Close()
		}
		if s.muxBaseListener != nil {
			if errClose := s.muxBaseListener.Close(); errClose != nil && !errors.Is(errClose, net.ErrClosed) {
				log.Debugf("failed to close shared listener after accept loop exit: %v", errClose)
			}
		}
		errServe := <-httpErrCh
		errServe = normalizeHTTPServeError(errServe)
		errAccept = normalizeListenerError(errAccept)
		if errAccept != nil {
			return fmt.Errorf("failed to start HTTP server: %v", errAccept)
		}
		if errServe != nil {
			return fmt.Errorf("failed to start HTTP server: %v", errServe)
		}
		return nil
	}
}

// Stop gracefully shuts down the API server without interrupting any
// active connections.
//
// Parameters:
//   - ctx: The context for graceful shutdown
//
// Returns:
//   - error: An error if the server fails to stop
func (s *Server) Stop(ctx context.Context) error {
	log.Debug("Stopping API server...")

	if s.keepAliveEnabled {
		select {
		case s.keepAliveStop <- struct{}{}:
		default:
		}
	}
	if s.mgmt != nil {
		if err := s.mgmt.Close(); err != nil {
			log.Debugf("failed to close management handler: %v", err)
		}
	}

	if s.muxHTTPListener != nil {
		_ = s.muxHTTPListener.Close()
	}
	if s.muxBaseListener != nil {
		if errClose := s.muxBaseListener.Close(); errClose != nil && !errors.Is(errClose, net.ErrClosed) {
			log.Debugf("failed to close shared listener: %v", errClose)
		}
	}
	s.closeMuxSniffConns()

	// Shutdown the HTTP server.
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %v", err)
	}

	log.Debug("API server stopped")
	return nil
}

// corsMiddleware returns a Gin middleware handler that adds CORS headers
// to every response, allowing cross-origin requests.
//
// Returns:
//   - gin.HandlerFunc: The CORS middleware handler
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "*")
		c.Header("Access-Control-Expose-Headers", "X-CPA-VERSION, X-CPA-COMMIT, X-CPA-BUILD-DATE, X-Config-Hash, ETag")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func (s *Server) applyAccessConfig(oldCfg, newCfg *config.Config) {
	if s == nil || s.accessManager == nil || newCfg == nil {
		return
	}
	if _, err := access.ApplyAccessProviders(s.accessManager, oldCfg, newCfg); err != nil {
		return
	}
}

// UpdateClients updates the server's client list and configuration.
// This method is called when the configuration or authentication tokens change.
//
// Parameters:
//   - clients: The new slice of AI service clients
//   - cfg: The new application configuration
func (s *Server) UpdateClients(cfg *config.Config) {
	// Reconstruct old config from YAML snapshot to avoid reference sharing issues
	var oldCfg *config.Config
	if len(s.oldConfigYaml) > 0 {
		_ = yaml.Unmarshal(s.oldConfigYaml, &oldCfg)
	}

	// Update request logger enabled state if it has changed
	previousRequestLog := false
	if oldCfg != nil {
		previousRequestLog = oldCfg.RequestLog
	}
	if s.requestLogger != nil && (oldCfg == nil || previousRequestLog != cfg.RequestLog) {
		if s.loggerToggle != nil {
			s.loggerToggle(cfg.RequestLog)
		} else if toggler, ok := s.requestLogger.(interface{ SetEnabled(bool) }); ok {
			toggler.SetEnabled(cfg.RequestLog)
		}
	}

	if oldCfg == nil || oldCfg.Home.Enabled != cfg.Home.Enabled {
		if setter, ok := s.requestLogger.(interface{ SetHomeEnabled(bool) }); ok {
			setter.SetHomeEnabled(cfg.Home.Enabled)
		}
	}

	if oldCfg == nil || oldCfg.LoggingToFile != cfg.LoggingToFile || oldCfg.LogsMaxTotalSizeMB != cfg.LogsMaxTotalSizeMB {
		if err := logging.ConfigureLogOutput(cfg); err != nil {
			log.Errorf("failed to reconfigure log output: %v", err)
		}
	}

	if oldCfg == nil || oldCfg.UsageStatisticsEnabled != cfg.UsageStatisticsEnabled {
		redisqueue.SetUsageStatisticsEnabled(cfg.UsageStatisticsEnabled)
	}

	if oldCfg == nil || oldCfg.RedisUsageQueueRetentionSeconds != cfg.RedisUsageQueueRetentionSeconds {
		redisqueue.SetRetentionSeconds(cfg.RedisUsageQueueRetentionSeconds)
	}

	usagePersistenceChanged := oldCfg == nil || oldCfg.UsagePersistenceEnabled != cfg.UsagePersistenceEnabled
	usageAuthDirChanged := cfg.UsagePersistenceEnabled && oldCfg != nil && oldCfg.AuthDir != cfg.AuthDir
	// AuthDir changes only affect the SQLite backend.
	if usagePersistenceChanged || (usageAuthDirChanged && usage.UsingSQLiteBackend()) {
		if err := usage.UpdatePersistence(context.Background(), cfg.UsagePersistenceEnabled, cfg.AuthDir); err != nil {
			log.Warnf("usage database init failed, using memory only: %v", err)
		} else if oldCfg != nil {
			log.Debugf("usage_persistence_enabled updated from %t to %t", oldCfg.UsagePersistenceEnabled, cfg.UsagePersistenceEnabled)
		} else {
			log.Debugf("usage_persistence_enabled toggled to %t", cfg.UsagePersistenceEnabled)
		}
	}

	if s.requestLogger != nil && (oldCfg == nil || oldCfg.ErrorLogsMaxFiles != cfg.ErrorLogsMaxFiles) {
		if setter, ok := s.requestLogger.(interface{ SetErrorLogsMaxFiles(int) }); ok {
			setter.SetErrorLogsMaxFiles(cfg.ErrorLogsMaxFiles)
		}
	}

	if oldCfg == nil || oldCfg.DisableCooling != cfg.DisableCooling {
		auth.SetQuotaCooldownDisabled(cfg.DisableCooling)
	}

	if oldCfg != nil && oldCfg.DisableImageGeneration != cfg.DisableImageGeneration {
		log.Infof("disable-image-generation updated: %v -> %v", oldCfg.DisableImageGeneration, cfg.DisableImageGeneration)
	}

	applySignatureCacheConfig(oldCfg, cfg)

	if s.handlers != nil && s.handlers.AuthManager != nil {
		s.handlers.AuthManager.SetRetryConfig(cfg.RequestRetry, time.Duration(cfg.MaxRetryInterval)*time.Second, cfg.MaxRetryCredentials)
	}

	// Update log level dynamically when debug flag changes
	if oldCfg == nil || oldCfg.Debug != cfg.Debug {
		util.SetLogLevel(cfg)
	}

	prevSecretEmpty := true
	if oldCfg != nil {
		prevSecretEmpty = oldCfg.RemoteManagement.SecretKey == ""
	}
	newSecretEmpty := cfg.RemoteManagement.SecretKey == ""
	if s.envManagementSecret {
		s.registerManagementRoutes()
		if s.managementRoutesEnabled.CompareAndSwap(false, true) {
			log.Info("management routes enabled via MANAGEMENT_PASSWORD")
		} else {
			s.managementRoutesEnabled.Store(true)
		}
	} else {
		switch {
		case prevSecretEmpty && !newSecretEmpty:
			s.registerManagementRoutes()
			if s.managementRoutesEnabled.CompareAndSwap(false, true) {
				log.Info("management routes enabled after secret key update")
			} else {
				s.managementRoutesEnabled.Store(true)
			}
		case !prevSecretEmpty && newSecretEmpty:
			if s.managementRoutesEnabled.CompareAndSwap(true, false) {
				log.Info("management routes disabled after secret key removal")
			} else {
				s.managementRoutesEnabled.Store(false)
			}
		default:
			s.managementRoutesEnabled.Store(!newSecretEmpty)
		}
	}
	redisqueue.SetEnabled(s.managementRoutesEnabled.Load() || (cfg != nil && cfg.Home.Enabled))

	s.applyAccessConfig(oldCfg, cfg)
	s.cfg = cfg
	s.wsAuthEnabled.Store(cfg.WebsocketAuth)
	if oldCfg != nil && s.wsAuthChanged != nil && oldCfg.WebsocketAuth != cfg.WebsocketAuth {
		s.wsAuthChanged(oldCfg.WebsocketAuth, cfg.WebsocketAuth)
	}
	managementasset.SetCurrentConfig(cfg)
	// Save YAML snapshot for next comparison
	s.oldConfigYaml, _ = yaml.Marshal(cfg)

	s.handlers.UpdateClients(effectiveSDKConfig(cfg))

	if s.mgmt != nil {
		s.mgmt.SetConfig(cfg)
		s.mgmt.SetAuthManager(s.handlers.AuthManager)
	}

	// Count client sources from configuration and auth store.
	authEntries := 0
	if cfg != nil && !cfg.Home.Enabled {
		tokenStore := sdkAuth.GetTokenStore()
		if dirSetter, ok := tokenStore.(interface{ SetBaseDir(string) }); ok {
			dirSetter.SetBaseDir(cfg.AuthDir)
		}
		authEntries = util.CountAuthFiles(context.Background(), tokenStore)
	}
	geminiAPIKeyCount := len(cfg.GeminiKey)
	claudeAPIKeyCount := len(cfg.ClaudeKey)
	codexAPIKeyCount := len(cfg.CodexKey)

	total := authEntries + geminiAPIKeyCount + claudeAPIKeyCount + codexAPIKeyCount
	fmt.Printf("server clients and configuration updated: %d clients (%d auth entries + %d Gemini API keys + %d Claude API keys + %d Codex keys)\n",
		total,
		authEntries,
		geminiAPIKeyCount,
		claudeAPIKeyCount,
		codexAPIKeyCount,
	)
}

func (s *Server) applyManagementConfigUpdate(cfg *config.Config) {
	if s == nil || cfg == nil {
		return
	}
	s.UpdateClients(cfg)
	s.UpdateBindingConfig(cfg)
	if s.authManager != nil {
		s.authManager.SetConfig(cfg)
		s.authManager.ResetSelectorState()
	}
}

func (s *Server) SetWebsocketAuthChangeHandler(fn func(bool, bool)) {
	if s == nil {
		return
	}
	s.wsAuthChanged = fn
}

// UpdateBindingConfig atomically replaces the account-bind routing table in the server.
// Called on every config reload alongside UpdateClients.
//
// Bindings are active only when routing.strategy is "account-bind"; other strategies
// must not interpret api-keys auth_index/auth_identity metadata.
func (s *Server) UpdateBindingConfig(cfg *config.Config) {
	if s == nil || cfg == nil {
		return
	}
	strategy, _ := auth.NormalizeRoutingStrategy(cfg.Routing.Strategy)
	if strategy != auth.RoutingStrategyAccountBind {
		s.bindingMu.Lock()
		defer s.bindingMu.Unlock()
		s.bindingMap = nil
		s.explicitBindingKeys = nil
		s.defaultBoundAuthIndex = ""
		s.strictBinding = false
		return
	}

	var auths []*auth.Auth
	if s.authManager != nil {
		auths = s.authManager.List()
	}
	bindingMap, defaultBoundAuthIndex, explicitBindingKeys := auth.ResolveBindingIndexes(
		auths,
		cfg.APIKeyAuthBindings,
		cfg.APIKeyAuthIdentityBindings,
		cfg.Routing.DefaultModelAccount,
	)
	s.bindingMu.Lock()
	defer s.bindingMu.Unlock()
	s.bindingMap = bindingMap // may be nil
	s.explicitBindingKeys = explicitBindingKeys
	s.defaultBoundAuthIndex = defaultBoundAuthIndex
	s.strictBinding = true
}

const (
	accountBindSourceExplicit = "api-key-auth-identity"
	accountBindSourceDefault  = "default-model-account"
)

func (s *Server) lookupBoundAuthBinding(clientKey string) (string, string, bool, bool) {
	if s == nil {
		return "", "", false, false
	}
	s.bindingMu.RLock()
	bindingMap := s.bindingMap
	explicitBindingKeys := s.explicitBindingKeys
	defaultIdx := s.defaultBoundAuthIndex
	strict := s.strictBinding
	s.bindingMu.RUnlock()

	active := len(bindingMap) > 0 || len(explicitBindingKeys) > 0 || defaultIdx != "" || strict
	if !active {
		return "", "", false, false
	}

	authIdx := ""
	clientKey = strings.TrimSpace(clientKey)
	if clientKey != "" {
		if len(bindingMap) > 0 {
			authIdx = bindingMap[clientKey]
		}
		if authIdx != "" {
			return authIdx, accountBindSourceExplicit, strict, true
		}
		if _, hasExplicitBinding := explicitBindingKeys[clientKey]; hasExplicitBinding {
			return "", accountBindSourceExplicit, strict, true
		}
	}
	if defaultIdx != "" {
		authIdx = defaultIdx
		return authIdx, accountBindSourceDefault, strict, true
	}
	return "", "", strict, true
}

const accountBindUnboundErrorMessage = "account-bind: no auth_index bound for this API key"

func (s *Server) resolveCurrentBoundAuthIndex(c *gin.Context) (string, bool, error) {
	clientKey := clientAPIKeyFromGinContext(c)
	authIdx, _, strict, active := s.lookupBoundAuthBinding(clientKey)
	if !active {
		return "", true, nil
	}
	if authIdx == "" && strict {
		return "", true, accountBindUnboundError(c)
	}
	return authIdx, true, nil
}

// accountBindMiddleware resolves the bound auth_index for the client API key and
// injects it into the request context when account-bind routing is active.
func (s *Server) accountBindMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientKey := clientAPIKeyFromGinContext(c)
		authIdx, source, strict, active := s.lookupBoundAuthBinding(clientKey)
		if !active {
			c.Next()
			return
		}
		if authIdx == "" {
			if strict {
				c.AbortWithStatusJSON(http.StatusBadRequest, accountBindUnboundErrorBody(c))
				return
			}
			c.Next()
			return
		}

		ctx := handlers.WithBoundAuthIndex(c.Request.Context(), authIdx)
		ctx = handlers.WithBoundAuthSource(ctx, source)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func clientAPIKeyFromGinContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	for _, key := range []string{"userApiKey", "apiKey"} {
		if raw, ok := c.Get(key); ok {
			if value, _ := raw.(string); strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func accountBindUnboundError(c *gin.Context) error {
	return errors.New(accountBindUnboundErrorMessageWithRequestID(c))
}

func accountBindUnboundErrorBody(c *gin.Context) gin.H {
	return gin.H{
		"error": accountBindUnboundErrorMessageWithRequestID(c),
	}
}

func accountBindUnboundErrorMessageWithRequestID(c *gin.Context) string {
	requestID := accountBindRequestID(c)
	if requestID == "" {
		return accountBindUnboundErrorMessage
	}
	return fmt.Sprintf("%s (request id: %s)", accountBindUnboundErrorMessage, requestID)
}

func accountBindRequestID(c *gin.Context) string {
	requestID := strings.TrimSpace(logging.GetGinRequestID(c))
	if requestID != "" {
		return requestID
	}
	if c != nil && c.Request != nil {
		return strings.TrimSpace(logging.GetRequestID(c.Request.Context()))
	}
	return ""
}

// (management handlers moved to internal/api/handlers/management)

// AuthMiddleware returns a Gin middleware handler that authenticates requests
// using the configured authentication providers.
func AuthMiddleware(manager *sdkaccess.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.Next()
			return
		}

		result, err := manager.Authenticate(c.Request.Context(), c.Request)
		if err == nil {
			if result != nil {
				c.Set("userApiKey", result.Principal)
				c.Set("accessProvider", result.Provider)
				if len(result.Metadata) > 0 {
					c.Set("accessMetadata", result.Metadata)
				}
			}
			c.Next()
			return
		}

		statusCode := err.HTTPStatusCode()
		if statusCode >= http.StatusInternalServerError {
			log.Errorf("authentication middleware error: %v", err)
		}
		c.AbortWithStatusJSON(statusCode, gin.H{"error": err.Message})
	}
}

func configuredSignatureCacheEnabled(cfg *config.Config) bool {
	if cfg != nil && cfg.AntigravitySignatureCacheEnabled != nil {
		return *cfg.AntigravitySignatureCacheEnabled
	}
	return true
}

func applySignatureCacheConfig(oldCfg, cfg *config.Config) {
	newVal := configuredSignatureCacheEnabled(cfg)
	newStrict := configuredSignatureBypassStrict(cfg)
	if oldCfg == nil {
		cache.SetSignatureCacheEnabled(newVal)
		cache.SetSignatureBypassStrictMode(newStrict)
		return
	}

	oldVal := configuredSignatureCacheEnabled(oldCfg)
	if oldVal != newVal {
		cache.SetSignatureCacheEnabled(newVal)
	}

	oldStrict := configuredSignatureBypassStrict(oldCfg)
	if oldStrict != newStrict {
		cache.SetSignatureBypassStrictMode(newStrict)
	}
}

func configuredSignatureBypassStrict(cfg *config.Config) bool {
	if cfg != nil && cfg.AntigravitySignatureBypassStrict != nil {
		return *cfg.AntigravitySignatureBypassStrict
	}
	return false
}
