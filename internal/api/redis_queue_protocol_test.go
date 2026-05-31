package api

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadRESPArrayRejectsOversizedArrayBeforeAllocation(t *testing.T) {
	_, err := readRESPArray(bufio.NewReader(strings.NewReader("*513\r\n")))
	if err == nil {
		t.Fatalf("expected oversized array error")
	}
	if !strings.Contains(err.Error(), "array too large") {
		t.Fatalf("expected array too large error, got %v", err)
	}
}

func TestReadRESPArrayRejectsOversizedBulkStringBeforeAllocation(t *testing.T) {
	_, err := readRESPArray(bufio.NewReader(strings.NewReader("*1\r\n$1048577\r\n")))
	if err == nil {
		t.Fatalf("expected oversized bulk string error")
	}
	if !strings.Contains(err.Error(), "bulk string too large") {
		t.Fatalf("expected bulk string too large error, got %v", err)
	}
}

func TestReadRESPArrayRejectsOversizedSimpleLine(t *testing.T) {
	payload := "*1\r\n+" + strings.Repeat("a", 4097) + "\r\n"
	_, err := readRESPArray(bufio.NewReader(strings.NewReader(payload)))
	if err == nil {
		t.Fatalf("expected oversized line error")
	}
	if !strings.Contains(err.Error(), "line too large") {
		t.Fatalf("expected line too large error, got %v", err)
	}
}
