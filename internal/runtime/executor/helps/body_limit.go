package helps

import (
	"errors"
	"fmt"
	"io"
)

const (
	MaxErrorBodyBytes    int64 = 1 << 20
	MaxResponseBodyBytes int64 = 64 << 20
)

var ErrResponseBodyTooLarge = errors.New("response body too large")

func ReadLimitedErrorBody(body io.Reader) ([]byte, error) {
	return readLimitedBody(body, MaxErrorBodyBytes, false)
}

func ReadLimitedResponseBody(body io.Reader) ([]byte, error) {
	return readLimitedBody(body, MaxResponseBodyBytes, true)
}

func readLimitedBody(body io.Reader, maxBytes int64, failOnLimit bool) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) <= maxBytes {
		return data, nil
	}
	data = data[:maxBytes]
	if failOnLimit {
		return data, fmt.Errorf("%w: limit %d bytes", ErrResponseBodyTooLarge, maxBytes)
	}
	return data, nil
}
