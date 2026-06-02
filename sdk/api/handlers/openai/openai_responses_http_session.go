package openai

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	responsesHTTPSessionTTL             = 30 * time.Minute
	responsesHTTPSessionCleanupInterval = time.Minute
	responsesHTTPSessionMaxSessions     = 1 << 13
	responsesHTTPSessionMaxBytes        = 1 << 10
)

var defaultResponsesHTTPSessionStore = newResponsesHTTPSessionStore(responsesHTTPSessionTTL)

type responsesHTTPSessionStore struct {
	mu              sync.Mutex
	ttl             time.Duration
	cleanupInterval time.Duration
	maxSessions     int
	nextCleanupAt   time.Time
	sessions        map[string]*responsesHTTPSessionState
}

type responsesHTTPSessionState struct {
	lastSeen         time.Time
	lastRequest      []byte
	lastResponseBody []byte
}

func newResponsesHTTPSessionStore(ttl time.Duration) *responsesHTTPSessionStore {
	if ttl <= 0 {
		ttl = responsesHTTPSessionTTL
	}
	return &responsesHTTPSessionStore{
		ttl:             ttl,
		cleanupInterval: responsesHTTPSessionCleanupInterval,
		maxSessions:     responsesHTTPSessionMaxSessions,
		sessions:        make(map[string]*responsesHTTPSessionState),
	}
}

func (s *responsesHTTPSessionStore) get(sessionKey string) ([]byte, []byte, bool) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || s == nil {
		return nil, nil, false
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.maybeCleanupLocked(now)
	session, ok := s.sessions[sessionKey]
	if !ok || session == nil {
		return nil, nil, false
	}
	if s.ttl > 0 && now.Sub(session.lastSeen) > s.ttl {
		delete(s.sessions, sessionKey)
		return nil, nil, false
	}
	session.lastSeen = now
	return bytes.Clone(session.lastRequest), bytes.Clone(session.lastResponseBody), true
}

func (s *responsesHTTPSessionStore) put(sessionKey string, lastRequest []byte, lastResponseBody []byte) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || s == nil || len(lastRequest) == 0 {
		return
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.maybeCleanupLocked(now)
	if len(lastRequest)+len(lastResponseBody) > responsesHTTPSessionMaxBytes {
		delete(s.sessions, sessionKey)
		return
	}
	s.sessions[sessionKey] = &responsesHTTPSessionState{
		lastSeen:         now,
		lastRequest:      bytes.Clone(lastRequest),
		lastResponseBody: bytes.Clone(lastResponseBody),
	}
	s.enforceMaxSessionsLocked()
}

func (s *responsesHTTPSessionStore) maybeCleanupLocked(now time.Time) {
	if s == nil || s.ttl <= 0 {
		return
	}
	if s.cleanupInterval <= 0 || s.nextCleanupAt.IsZero() || !now.Before(s.nextCleanupAt) {
		s.cleanupLocked(now)
		if s.cleanupInterval > 0 {
			s.nextCleanupAt = now.Add(s.cleanupInterval)
		}
	}
}

func (s *responsesHTTPSessionStore) cleanupLocked(now time.Time) {
	if s == nil || s.ttl <= 0 {
		return
	}
	for key, session := range s.sessions {
		if session == nil || now.Sub(session.lastSeen) > s.ttl {
			delete(s.sessions, key)
		}
	}
}

func (s *responsesHTTPSessionStore) enforceMaxSessionsLocked() {
	if s == nil || s.maxSessions <= 0 || len(s.sessions) <= s.maxSessions {
		return
	}

	type sessionEntry struct {
		key      string
		lastSeen time.Time
	}
	entries := make([]sessionEntry, 0, len(s.sessions))
	for key, session := range s.sessions {
		if session == nil {
			delete(s.sessions, key)
			continue
		}
		entries = append(entries, sessionEntry{key: key, lastSeen: session.lastSeen})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastSeen.Before(entries[j].lastSeen)
	})
	for len(s.sessions) > s.maxSessions && len(entries) > 0 {
		oldest := entries[0]
		entries = entries[1:]
		delete(s.sessions, oldest.key)
	}
}

func responsesHTTPSessionKey(req *http.Request) string {
	if req == nil {
		return ""
	}
	if raw := strings.TrimSpace(req.Header.Get("X-Codex-Turn-Metadata")); raw != "" {
		if sessionID := strings.TrimSpace(gjson.Get(raw, "session_id").String()); sessionID != "" {
			return sessionID
		}
	}
	if sessionID := strings.TrimSpace(req.Header.Get("Session_id")); sessionID != "" {
		return sessionID
	}
	return ""
}

func responsesRequestUsesPreviousResponseID(rawJSON []byte) bool {
	return strings.TrimSpace(gjson.GetBytes(rawJSON, "previous_response_id").String()) != ""
}

func isResponsesPreviousResponseNotFoundError(errMsg *interfaces.ErrorMessage) bool {
	return isPreviousResponseNotFoundWebsocketError(errMsg)
}

func responsesOutputFromBody(payload []byte) []byte {
	output := gjson.GetBytes(payload, "output")
	if output.Exists() && output.IsArray() {
		return bytes.Clone([]byte(output.Raw))
	}
	return responseCompletedOutputFromPayload(payload)
}

func normalizeResponsesHTTPRequestWithSession(
	rawJSON []byte,
	lastRequest []byte,
	lastResponseBody []byte,
) ([]byte, *interfaces.ErrorMessage) {
	if !responsesRequestUsesPreviousResponseID(rawJSON) || len(lastRequest) == 0 {
		return bytes.Clone(rawJSON), nil
	}

	normalized, _, errMsg := normalizeResponseSubsequentRequest(rawJSON, lastRequest, lastResponseBody, false)
	if errMsg != nil {
		return nil, errMsg
	}

	streamResult := gjson.GetBytes(rawJSON, "stream")
	if streamResult.Exists() {
		updated, errSet := sjson.SetRawBytes(normalized, "stream", []byte(streamResult.Raw))
		if errSet == nil {
			normalized = updated
		}
	} else {
		updated, errDelete := sjson.DeleteBytes(normalized, "stream")
		if errDelete == nil {
			normalized = updated
		}
	}

	return normalized, nil
}
