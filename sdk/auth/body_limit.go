package auth

import (
	"errors"
	"fmt"
	"io"
)

const maxAuthResponseBodyBytes int64 = 1 << 20

var errAuthResponseBodyTooLarge = errors.New("auth response body too large")

func readLimitedAuthResponseBody(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(body, maxAuthResponseBodyBytes+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) <= maxAuthResponseBodyBytes {
		return data, nil
	}
	return data[:maxAuthResponseBodyBytes], fmt.Errorf("%w: limit is %d bytes", errAuthResponseBodyTooLarge, maxAuthResponseBodyBytes)
}
