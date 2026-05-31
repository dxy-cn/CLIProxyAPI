package helps

import (
	"errors"
	"strings"
	"testing"
)

func TestReadLimitedBodyReturnsLimitError(t *testing.T) {
	data, err := readLimitedBody(strings.NewReader("abcd"), 3, true)
	if !errors.Is(err, ErrResponseBodyTooLarge) {
		t.Fatalf("expected ErrResponseBodyTooLarge, got %v", err)
	}
	if got, want := string(data), "abc"; got != want {
		t.Fatalf("limited body = %q, want %q", got, want)
	}
}

func TestReadLimitedErrorBodyTruncatesWithoutLimitError(t *testing.T) {
	data, err := readLimitedBody(strings.NewReader("abcd"), 3, false)
	if err != nil {
		t.Fatalf("readLimitedBody returned error: %v", err)
	}
	if got, want := string(data), "abc"; got != want {
		t.Fatalf("limited body = %q, want %q", got, want)
	}
}
