package responses

import (
	"bytes"
	"context"
	"strings"

	"github.com/tidwall/gjson"
)

// ConvertCodexResponseToOpenAIResponses converts OpenAI Chat Completions streaming chunks
// to OpenAI Responses SSE events (response.*).

func ConvertCodexResponseToOpenAIResponses(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) [][]byte {
	if bytes.HasPrefix(rawJSON, []byte("data:")) {
		rawJSON = bytes.TrimSpace(rawJSON[5:])
		out := make([]byte, 0, len(rawJSON)+len("data: "))
		out = append(out, []byte("data: ")...)
		out = append(out, rawJSON...)
		return [][]byte{out}
	}
	return [][]byte{rawJSON}
}

// ConvertCodexResponseToOpenAIResponsesNonStream builds a single Responses JSON
// from a terminal Codex Responses event.
func ConvertCodexResponseToOpenAIResponsesNonStream(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) []byte {
	rootResult := gjson.ParseBytes(rawJSON)
	if !isCodexResponsesTerminalEvent(rootResult.Get("type").String()) {
		return []byte{}
	}
	responseResult := rootResult.Get("response")
	return []byte(responseResult.Raw)
}

func isCodexResponsesTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.incomplete", "response.failed", "response.cancelled":
		return true
	default:
		return false
	}
}
