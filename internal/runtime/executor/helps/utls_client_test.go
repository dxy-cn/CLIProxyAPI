package helps

import (
	"io"
	"testing"
)

type closeRecorder struct {
	closed bool
}

func (c *closeRecorder) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

func TestUtlsTrackedBodyClosesResponseAndConnection(t *testing.T) {
	body := &closeRecorder{}
	connClosed := false
	wrapped := &utlsTrackedBody{
		ReadCloser: body,
		closeConn: func() {
			connClosed = true
		},
	}

	if err := wrapped.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !body.closed {
		t.Fatalf("expected response body to close")
	}
	if !connClosed {
		t.Fatalf("expected connection cleanup to run")
	}
}
