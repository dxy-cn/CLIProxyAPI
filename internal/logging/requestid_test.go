package logging

import (
	"encoding/hex"
	"testing"
)

func TestGenerateRequestIDReturns32HexCharacters(t *testing.T) {
	requestID := GenerateRequestID()
	if len(requestID) != 32 {
		t.Fatalf("request ID length = %d, want 32: %q", len(requestID), requestID)
	}
	if _, err := hex.DecodeString(requestID); err != nil {
		t.Fatalf("request ID must be hex: %q: %v", requestID, err)
	}
}
