package api

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type testMuxListener struct {
	addr   net.Addr
	connCh chan net.Conn
	done   chan struct{}
	once   sync.Once
}

func newTestMuxListener() *testMuxListener {
	return &testMuxListener{
		addr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8317},
		connCh: make(chan net.Conn, 8),
		done:   make(chan struct{}),
	}
}

func (l *testMuxListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case conn := <-l.connCh:
		return conn, nil
	}
}

func (l *testMuxListener) Close() error {
	l.once.Do(func() {
		close(l.done)
	})
	return nil
}

func (l *testMuxListener) Addr() net.Addr {
	return l.addr
}

func TestAcceptMuxConnections_DoesNotBlockBehindIdleConnection(t *testing.T) {
	server := &Server{}
	listener := newTestMuxListener()
	t.Cleanup(func() { _ = listener.Close() })
	httpListener := newMuxListener(listener.Addr(), 1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.acceptMuxConnections(listener, httpListener)
	}()

	idleClient, idleServer := net.Pipe()
	defer func() { _ = idleClient.Close() }()
	defer func() { _ = idleServer.Close() }()
	listener.connCh <- idleServer

	httpClient, httpServer := net.Pipe()
	defer func() { _ = httpClient.Close() }()
	defer func() { _ = httpServer.Close() }()
	listener.connCh <- httpServer
	go func() {
		_, _ = httpClient.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	}()

	acceptedCh := make(chan net.Conn, 1)
	go func() {
		conn, errAccept := httpListener.Accept()
		if errAccept == nil {
			acceptedCh <- conn
		}
	}()

	select {
	case conn := <-acceptedCh:
		_ = conn.Close()
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("HTTP connection was blocked behind an idle connection")
	}

	_ = listener.Close()
	_ = idleClient.Close()
	select {
	case errAcceptLoop := <-errCh:
		if errAcceptLoop != nil && !errors.Is(errAcceptLoop, net.ErrClosed) {
			t.Fatalf("accept loop returned unexpected error: %v", errAcceptLoop)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("accept loop did not exit")
	}
}
