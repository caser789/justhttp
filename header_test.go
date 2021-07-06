package fasthttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"
)

func TestResponseConnectionClose(t *testing.T) {
	testResponseConnectionClose(t, true)
	testResponseConnectionClose(t, false)
}

func testResponseConnectionClose(t *testing.T, connectionClose bool) {
	h := &ResponseHeader{
		ConnectionClose: connectionClose,
	}
	w := &bytes.Buffer{}
	bw := bufio.NewWriter(w)
	err := h.Write(bw)
	if err != nil {
		t.Fatalf("Unexpected error when writing response header: %s", err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("Unexpected error when flushing response header: %s", err)
	}

	var h1 ResponseHeader
	br := bufio.NewReader(w)
	err = h1.Read(br)
	if err != nil {
		t.Fatalf("Unexpected error when reading response header: %s", err)
	}
	if h1.ConnectionClose != h.ConnectionClose {
		t.Fatalf("Unexpected value for ConnectionClose: %v. Expected %v", h1.ConnectionClose, h.ConnectionClose)
	}
}

func _TestResponseHeaderTooBig(t *testing.T) {
	s := "HTTP:/1.1 200 OK\r\nContent-Type: sss\r\nContent-Length: 0\r\n" + getHeaders(100500) + "\r\n"
	r := bytes.NewBufferString(s)
	br := bufio.NewReaderSize(r, 4096)
	h := &ResponseHeader{}
	err := h.Read(br)
	if err == nil {
		t.Fatalf("Expecting error when reading too big header")
	}
}

func getHeaders(n int) string {
	var h []string
	for i := 0; i < n; i++ {
		h = append(h, fmt.Sprintf("Header_%d: Value_%d\r\n", i, i))
	}
	return strings.Join(h, "")
}

func TestResponseHeaderBufioPeek(t *testing.T) {
	r := &bufioPeekReader{
		s: "HTTP/1.1 200 OK\r\nContent-Length: 10\r\nContent-Type: aaa\r\n" + getHeaders(10) + "\r\n0123456789",
	}
	br := bufio.NewReaderSize(r, 4096)
	h := &ResponseHeader{}
	if err := h.Read(br); err != nil {
		t.Fatalf("Unexpected error when reading response: %s", err)
	}
	verifyResponseHeader(t, h, 200, 10, "aaa")
	verifyTrailer(t, br, "0123456789")
}

type bufioPeekReader struct {
	s string
	n int
}

func (r *bufioPeekReader) Read(b []byte) (int, error) {
	if len(r.s) == 0 {
		return 0, io.EOF
	}

	r.n++
	n := r.n
	if len(r.s) < n {
		n = len(r.s)
	}
	src := []byte(r.s[:n])
	r.s = r.s[n:]
	n = copy(b, src)
	return n, nil
}

func verifyResponseHeader(t *testing.T, h *ResponseHeader, expectedStatusCode, expectedContentLength int, expectedContentType string) {
	if h.StatusCode != expectedStatusCode {
		t.Fatalf("Unexpected status code %d. Expected %d", h.StatusCode, expectedStatusCode)
	}
	if h.ContentLength != expectedContentLength {
		t.Fatalf("Unexpected content length %d. Expected %d", h.ContentLength, expectedContentLength)
	}
	if !bytes.Equal(h.ContentType, []byte(expectedContentType)) {
		t.Fatalf("Unexpected content type %q. Expected %q", h.ContentType, expectedContentType)
	}
}

func verifyTrailer(t *testing.T, r *bufio.Reader, expectedTrailer string) {
	trailer, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatalf("Cannot read trailer: %s", err)
	}
	if !bytes.Equal(trailer, []byte(expectedTrailer)) {
		t.Fatalf("Unexpected trailer %q. Expected %q", trailer, expectedTrailer)
	}
}
