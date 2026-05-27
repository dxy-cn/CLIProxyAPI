package openai

import (
	"bytes"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const responsesHTTPSessionTTL = 30 * time.Minute

var defaultResponsesHTTPSessionStore = newResponsesHTTPSessionStore(responsesHTTPSessionTTL)

type responsesHTTPSessionStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	sessions map[string]*responsesHTTPSessionState
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
		ttl:      ttl,
		sessions: make(map[string]*responsesHTTPSessionState),
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

	s.cleanupLocked(now)
	session, ok := s.sessions[sessionKey]
	if !ok || session == nil {
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

	s.cleanupLocked(now)
	s.sessions[sessionKey] = &responsesHTTPSessionState{
		lastSeen:         now,
		lastRequest:      bytes.Clone(lastRequest),
		lastResponseBody: bytes.Clone(lastResponseBody),
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
