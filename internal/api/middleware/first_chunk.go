// Package middleware provides HTTP middleware components for the CLI Proxy API server.
// This file installs a lightweight wrapper that records the time-to-first-byte
// (TTFB / first token) for each HTTP response so usage reporting can compute
// streaming throughput without relying on the request-log middleware being enabled.
package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// FirstChunkContextKey is the gin.Context key used to expose the holder.
const FirstChunkContextKey = "USAGE_FIRST_CHUNK"

// FirstChunkHolder records the elapsed time between request start and the first
// byte written to the response. It is safe for concurrent use.
type FirstChunkHolder struct {
	startedAt time.Time
	latency   atomic.Int64 // nanoseconds; 0 means "not marked yet".
	once      sync.Once
}

// NewFirstChunkHolder creates a holder anchored at the given start time.
func NewFirstChunkHolder(start time.Time) *FirstChunkHolder {
	if start.IsZero() {
		start = time.Now()
	}
	return &FirstChunkHolder{startedAt: start}
}

// Mark records the first-chunk latency. Only the first call wins.
func (h *FirstChunkHolder) Mark() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		delta := max(time.Since(h.startedAt), 0)
		h.latency.Store(int64(delta))
	})
}

// Latency returns the recorded first-chunk latency or zero if Mark was never called.
func (h *FirstChunkHolder) Latency() time.Duration {
	if h == nil {
		return 0
	}
	return time.Duration(h.latency.Load())
}

// GetFirstChunkHolder retrieves the holder stored on the gin.Context, if any.
func GetFirstChunkHolder(c *gin.Context) *FirstChunkHolder {
	if c == nil {
		return nil
	}
	v, ok := c.Get(FirstChunkContextKey)
	if !ok {
		return nil
	}
	holder, _ := v.(*FirstChunkHolder)
	return holder
}

// firstChunkWriter wraps gin.ResponseWriter to call holder.Mark on first write.
type firstChunkWriter struct {
	gin.ResponseWriter
	holder *FirstChunkHolder
}

func (w *firstChunkWriter) Write(p []byte) (int, error) {
	w.holder.Mark()
	return w.ResponseWriter.Write(p)
}

func (w *firstChunkWriter) WriteString(s string) (int, error) {
	w.holder.Mark()
	return w.ResponseWriter.WriteString(s)
}

// Hijack forwards to the underlying writer when supported so websocket
// upgrades and other low-level takeovers continue to work.
func (w *firstChunkWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.holder.Mark()
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, errors.New("first_chunk: underlying writer does not support hijacking")
}

// FirstChunkCaptureMiddleware installs a FirstChunkHolder onto every gin.Context
// and wraps the ResponseWriter so the holder is marked on the first response byte.
func FirstChunkCaptureMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		holder := NewFirstChunkHolder(time.Now())
		c.Set(FirstChunkContextKey, holder)
		c.Writer = &firstChunkWriter{ResponseWriter: c.Writer, holder: holder}
		c.Next()
	}
}
