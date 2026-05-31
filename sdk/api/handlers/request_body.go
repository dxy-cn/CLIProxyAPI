package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	MaxRequestBodyBytes  int64 = 10 * 1024 * 1024
	MaxResponseBodyBytes int64 = 64 * 1024 * 1024
)

var ErrRequestBodyTooLarge = errors.New("request body too large")
var ErrResponseBodyTooLarge = errors.New("response body too large")

func ReadLimitedRawData(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if c.Request.ContentLength > MaxRequestBodyBytes {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrRequestBodyTooLarge, MaxRequestBodyBytes)
	}
	if c.Request.Body != nil && c.Writer != nil {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxRequestBodyBytes)
	}
	data, err := c.GetRawData()
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, fmt.Errorf("%w: limit is %d bytes", ErrRequestBodyTooLarge, MaxRequestBodyBytes)
		}
		return nil, err
	}
	return data, nil
}

func IsRequestBodyTooLarge(err error) bool {
	return errors.Is(err, ErrRequestBodyTooLarge)
}

func RequestBodyErrorStatus(err error) int {
	if IsRequestBodyTooLarge(err) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func ReadLimitedResponseData(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(body, MaxResponseBodyBytes+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) <= MaxResponseBodyBytes {
		return data, nil
	}
	return data[:MaxResponseBodyBytes], fmt.Errorf("%w: limit is %d bytes", ErrResponseBodyTooLarge, MaxResponseBodyBytes)
}
