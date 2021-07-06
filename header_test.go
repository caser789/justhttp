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

func TestResponseHeaderTooBig(t *testing.T) {
	s := "HTTP:/1.1 200 OK\r\nContent-Type: sss\r\nContent-Length: 0\r\n" + getHeaders(100500) + "\r\n"
	r := bytes.NewBufferString(s)
	br := bufio.NewReaderSize(r, 4096)
	h := &ResponseHeader{}
	err := h.Read(br)
	if err == nil {
		t.Fatalf("Expecting error when reading too big header")
	}
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

func TestResponseHeaderReadSuccess(t *testing.T) {
	h := &ResponseHeader{}

	// straight order of content-length and content-type
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\r\nContent-Length: 123\r\nContent-Type: text/html\r\n\r\n",
		200, 123, "text/html", "")

	// reverse order of content-length and content-type
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 202 OK\r\nContent-Type: text/plain; encoding=utf-8\r\nContent-length: 543\r\n\r\n",
		202, 543, "text/plain; encoding=utf-8", "")

	// transfer-encoding: chunked
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 505 Internal error\r\nContent-Type: text/html\r\nTransfer-Encoding: chunked\r\n\r\n",
		505, -1, "text/html", "")

	// reverse order of content-type and transfer-encoding
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 343 foobar\r\nTransfer-Encoding: chunked\r\nContent-Type: text/json\r\n\r\n",
		343, -1, "text/json", "")

	// additional headers
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 100 Continue\r\nFooBar: baz\r\nContent-Type: aaa/bbb\r\nUser-Agent: x\r\nContent-Length: 123\r\nZZZ: werer\r\n\r\n",
		100, 123, "aaa/bbb", "")

	// trailer (aka body)
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 32245\r\n\r\nqwert aaa",
		200, 32245, "text/plain", "qwert aaa")

	// ancient http protocol
	testResponseHeaderReadSuccess(t, h, "HTTP/0.9 300 OK\r\nContent-Length: 123\r\nContent-Type: text/html\r\n\r\nqqqq",
		300, 123, "text/html", "qqqq")

	// lf instead of crlf
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\nContent-Length: 123\nContent-Type: text/html\n\n",
		200, 123, "text/html", "")

	// Zero-length headers with mixed crlf and lf
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 400 OK\nContent-Length: 345\nZero-Value: \r\nContent-Type: aaa\n: zero-key\r\n\r\nooa",
		400, 345, "aaa", "ooa")

	// No space after colon
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\nContent-Length:34\nContent-Type: sss\n\naaaa",
		200, 34, "sss", "aaaa")

	// invalid case
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 400 OK\nconTEnt-leNGTH: 123\nConTENT-TYPE: ass\n\n",
		400, 123, "ass", "")

	// duplicate content-length
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\r\nContent-Length: 456\r\nContent-Type: foo/bar\r\nContent-Length: 321\r\n\r\n",
		200, 321, "foo/bar", "")

	// duplicate content-type
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\r\nContent-Length: 234\r\nContent-Type: foo/bar\r\nContent-Type: baz/bar\r\n\r\n",
		200, 234, "baz/bar", "")

	// both transfer-encoding: chunked and content-length
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\r\nContent-Type: foo/bar\r\nContent-Length: 123\r\nTransfer-Encoding: chunked\r\n\r\n",
		200, -1, "foo/bar", "")

	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 300 OK\r\nContent-Type: foo/barr\r\nTransfer-Encoding: chunked\r\nContent-Length: 354\r\n\r\n",
		300, -1, "foo/barr", "")

	// duplicate transfer-encoding: chunked
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n",
		200, -1, "text/html", "")

	// no reason string in the first line
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 456 OK\r\nContent-Type: xxx/yyy\r\nContent-Length: 134\r\n\r\naaaxxx",
		456, 134, "xxx/yyy", "aaaxxx")

	// blank lines before the first line
	testResponseHeaderReadSuccess(t, h, "\r\nHTTP/1.1 200 OK\r\nContent-Type: aa\r\nContent-Length: 0\r\n\r\nsss",
		200, 0, "aa", "sss")
}

func TestResponseHeaderReadError(t *testing.T) {
	h := &ResponseHeader{}

	// incorrect first line
	testResponseHeaderReadError(t, h, "fo")
	testResponseHeaderReadError(t, h, "foobarbaz")
	testResponseHeaderReadError(t, h, "HTTP/1.1")
	testResponseHeaderReadError(t, h, "HTTP/1.1 ")
	testResponseHeaderReadError(t, h, "HTTP/1.1 s")

	// non-numeric status code
	testResponseHeaderReadError(t, h, "HTTP/1.1 foobar OK\r\nContent-Length: 123\r\nContent-Type: text/html\r\n\r\n")
	testResponseHeaderReadError(t, h, "HTTP/1.1 123foobar OK\r\nContent-Length: 123\r\nContent-Type: text/html\r\n\r\n")
	testResponseHeaderReadError(t, h, "HTTP/1.1 foobar344 OK\r\nContent-Length: 123\r\nContent-Type: text/html\r\n\r\n")

	// no headers
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\n")
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\n\r\n")

	// no trailing crlf
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\nContent-Length: 123\r\nContent-Type: text/html\r\n")

	// non-numeric content-length
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\nContent-Length: faaa\r\nContent-Type: text/html\r\n\r\n")
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\nContent-Length: 123aa\r\nContent-Type: text/html\r\n\r\n")
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\nContent-Length: aa124\r\nContent-Type: text/html\r\n\r\n")

	// no content-type
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\nContent-Length: 123\r\n\r\n")

	// no content-length
	testResponseHeaderReadError(t, h, "HTTP/1.1 200 OK\r\nContent-Type: foo/bar\r\n\r\n")
}

func TestRequestHeaderTooBig(t *testing.T) {
	s := "GET / HTTP/1.1\r\nHost: aaa.com\r\n" + getHeaders(100500) + "\r\n"
	r := bytes.NewBufferString(s)
	br := bufio.NewReaderSize(r, 4096)
	h := &RequestHeader{}
	err := h.Read(br)
	if err == nil {
		t.Fatalf("Expecting error when reading too big header")
	}
}

func testResponseHeaderReadSuccess(t *testing.T, h *ResponseHeader, headers string, expectedStatusCode, expectedContentLength int,
	expectedContentType, expectedTrailer string) {
	r := bytes.NewBufferString(headers)
	br := bufio.NewReader(r)
	err := h.Read(br)
	if err != nil {
		t.Fatalf("Unexpected error when parsing response headers: %s. headers=%q", err, headers)
	}
	verifyResponseHeader(t, h, expectedStatusCode, expectedContentLength, expectedContentType)
	verifyTrailer(t, br, expectedTrailer)
}

func testResponseHeaderReadError(t *testing.T, h *ResponseHeader, headers string) {
	r := bytes.NewBufferString(headers)
	br := bufio.NewReader(r)
	err := h.Read(br)
	if err == nil {
		t.Fatalf("Expecting error when reading response header %q", headers)
	}

	// make sure response header works after error
	testResponseHeaderReadSuccess(t, h, "HTTP/1.1 200 OK\r\nContent-Type: foo/bar\r\nContent-Length: 12345\r\n\r\nsss",
		200, 12345, "foo/bar", "sss")
}

func getHeaders(n int) string {
	var h []string
	for i := 0; i < n; i++ {
		h = append(h, fmt.Sprintf("Header_%d: Value_%d\r\n", i, i))
	}
	return strings.Join(h, "")
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
