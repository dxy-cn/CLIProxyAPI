package cliproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestServiceShutdownStopsServerWithCanceledParentContext(t *testing.T) {
	port := freeTCPPort(t)
	handlerStarted := make(chan struct{})
	cfg := &config.Config{
		Host:    "127.0.0.1",
		Port:    port,
		AuthDir: t.TempDir(),
	}
	server := api.NewServer(cfg, coreauth.NewManager(nil, nil, nil), nil, "", api.WithRouterConfigurator(
		func(engine *gin.Engine, _ *handlers.BaseAPIHandler, _ *config.Config) {
			engine.GET("/slow-shutdown", func(c *gin.Context) {
				select {
				case <-handlerStarted:
				default:
					close(handlerStarted)
				}
				time.Sleep(150 * time.Millisecond)
				c.Status(http.StatusNoContent)
			})
		},
	))
	service := &Service{server: server}

	startErr := make(chan error, 1)
	go func() {
		startErr <- server.Start()
	}()
	waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/healthz", port))

	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/slow-shutdown", port))
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("slow shutdown handler did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := service.Shutdown(ctx)
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		t.Fatalf("Shutdown must not derive server stop timeout from a canceled caller context: %v", err)
	}
	if err != nil {
		t.Fatalf("Shutdown returned unexpected error: %v", err)
	}

	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("slow request did not finish after Shutdown")
	}

	select {
	case errStart := <-startErr:
		if errStart != nil && !errors.Is(errStart, http.ErrServerClosed) {
			t.Fatalf("server.Start returned unexpected error: %v", errStart)
		}
	case <-time.After(time.Second):
		t.Fatal("server.Start did not return after Shutdown")
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener addr type = %T, want *net.TCPAddr", listener.Addr())
	}
	return addr.Port
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not become ready at %s", url)
}
