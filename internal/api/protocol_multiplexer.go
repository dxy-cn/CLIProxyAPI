package api

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const muxProtocolSniffTimeout = 5 * time.Second

func normalizeHTTPServeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func normalizeListenerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) acceptMuxConnections(listener net.Listener, httpListener *muxListener) error {
	if s == nil || listener == nil {
		return net.ErrClosed
	}

	for {
		conn, errAccept := listener.Accept()
		if errAccept != nil {
			return errAccept
		}
		if conn == nil {
			continue
		}

		go s.routeMuxConnection(conn, httpListener)
	}
}

func (s *Server) routeMuxConnection(conn net.Conn, httpListener *muxListener) {
	if s == nil || conn == nil {
		return
	}
	s.trackMuxSniffConn(conn)
	defer s.untrackMuxSniffConn(conn)

	if errDeadline := conn.SetReadDeadline(time.Now().Add(muxProtocolSniffTimeout)); errDeadline != nil {
		log.Debugf("failed to set protocol sniff deadline: %v", errDeadline)
	}

	tlsConn, ok := conn.(*tls.Conn)
	if ok {
		if errHandshake := tlsConn.Handshake(); errHandshake != nil {
			if errClose := conn.Close(); errClose != nil {
				log.Errorf("failed to close connection after TLS handshake error: %v", errClose)
			}
			return
		}
		proto := strings.TrimSpace(tlsConn.ConnectionState().NegotiatedProtocol)
		if proto == "h2" || proto == "http/1.1" {
			clearMuxReadDeadline(conn)
			if httpListener == nil {
				if errClose := conn.Close(); errClose != nil {
					log.Errorf("failed to close connection: %v", errClose)
				}
				return
			}
			if errPut := httpListener.Put(tlsConn); errPut != nil {
				if errClose := conn.Close(); errClose != nil {
					log.Errorf("failed to close connection after HTTP routing failure: %v", errClose)
				}
			}
			return
		}
	}

	reader := bufio.NewReader(conn)
	prefix, errPeek := reader.Peek(1)
	if errPeek != nil {
		if errClose := conn.Close(); errClose != nil {
			log.Errorf("failed to close connection after protocol peek failure: %v", errClose)
		}
		return
	}

	clearMuxReadDeadline(conn)
	if isRedisRESPPrefix(prefix[0]) {
		if !s.managementRoutesEnabled.Load() {
			if errClose := conn.Close(); errClose != nil {
				log.Errorf("failed to close redis connection while management is disabled: %v", errClose)
			}
			return
		}
		go s.handleRedisConnection(conn, reader)
		return
	}

	if httpListener == nil {
		if errClose := conn.Close(); errClose != nil {
			log.Errorf("failed to close connection without HTTP listener: %v", errClose)
		}
		return
	}

	if errPut := httpListener.Put(&bufferedConn{Conn: conn, reader: reader}); errPut != nil {
		if errClose := conn.Close(); errClose != nil {
			log.Errorf("failed to close connection after HTTP routing failure: %v", errClose)
		}
	}
}

func clearMuxReadDeadline(conn net.Conn) {
	if conn == nil {
		return
	}
	if errDeadline := conn.SetReadDeadline(time.Time{}); errDeadline != nil {
		log.Debugf("failed to clear protocol sniff deadline: %v", errDeadline)
	}
}

func (s *Server) trackMuxSniffConn(conn net.Conn) {
	if s == nil || conn == nil {
		return
	}
	s.muxSniffMu.Lock()
	if s.muxSniffConns == nil {
		s.muxSniffConns = make(map[net.Conn]struct{})
	}
	s.muxSniffConns[conn] = struct{}{}
	s.muxSniffMu.Unlock()
}

func (s *Server) untrackMuxSniffConn(conn net.Conn) {
	if s == nil || conn == nil {
		return
	}
	s.muxSniffMu.Lock()
	delete(s.muxSniffConns, conn)
	s.muxSniffMu.Unlock()
}

func (s *Server) closeMuxSniffConns() {
	if s == nil {
		return
	}
	s.muxSniffMu.Lock()
	conns := make([]net.Conn, 0, len(s.muxSniffConns))
	for conn := range s.muxSniffConns {
		conns = append(conns, conn)
	}
	s.muxSniffMu.Unlock()

	for _, conn := range conns {
		if errClose := conn.Close(); errClose != nil {
			log.Debugf("failed to close protocol sniff connection: %v", errClose)
		}
	}
}
