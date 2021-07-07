package fasthttp

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"testing"
	"time"
)

func TestServerSteal(t *testing.T) {
	s := &Server{
		Handler: func(ctx *ServerCtx) {
			ctx.Steal()
			ctx.Success("text/plain", []byte("Stolen ctx"))
		},
	}

	rw := &readWriter{}
	rw.r.WriteString("GET /foo HTTP/1.1\r\nHost: google.com\r\n\r\n")

	ch := make(chan error)
	go func() {
		ch <- s.ServeConn(rw)
	}()

	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("Unexpected error from serveConn: %s", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("timeout")
	}

	br := bufio.NewReader(&rw.w)
	verifyResponse(t, br, 200, "text/plain", "Stolen ctx")

	data, err := ioutil.ReadAll(br)
	if err != nil {
		t.Fatalf("Unexpected error when reading remaining data: %s", err)
	}
	if len(data) != 0 {
		t.Fatalf("Unexpected data read after the first response %q. Expecting %q", data, "")
	}
}

type readWriter struct {
	r bytes.Buffer
	w bytes.Buffer
}

func (rw *readWriter) Read(b []byte) (int, error) {
	return rw.r.Read(b)
}

func (rw *readWriter) Write(b []byte) (int, error) {
	return rw.w.Write(b)
}

func verifyResponse(t *testing.T, r *bufio.Reader, expectedStatusCode int, expectedContentType, expectedBody string) {
	var resp Response
	if err := resp.Read(r); err != nil {
		t.Fatalf("Unexpected error when parsing response: %s", err)
	}

	if !bytes.Equal(resp.Body, []byte(expectedBody)) {
		t.Fatalf("Unexpected body %q. Expected %q", resp.Body, []byte(expectedBody))
	}
	verifyResponseHeader(t, &resp.Header, expectedStatusCode, len(resp.Body), expectedContentType)
}
