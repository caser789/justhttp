package fasthttp

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestRequestSuccess(t *testing.T) {
	// empty method, user-agent and body
	testRequestSuccess(t, "", "/foo/bar", "google.com", "", "", "GET")

	// non-empty user-agent
	testRequestSuccess(t, "GET", "/foo/bar", "google.com", "MSIE", "", "GET")

	// non-empty method
	testRequestSuccess(t, "HEAD", "/aaa", "foobar", "", "", "HEAD")

	// POST method with body
	testRequestSuccess(t, "POST", "/bbb", "aaa.com", "Chrome aaa", "post body", "POST")
}

func testRequestSuccess(t *testing.T, method, requestURI, host, userAgent, body, expectedMethod string) {
	var req Request

	req.Header.Method = []byte(method)
	req.Header.RequestURI = []byte(requestURI)
	req.Header.Host = []byte(host)
	req.Header.UserAgent = []byte(userAgent)
	req.Body = []byte(body)

	contentType := []byte("foobar")
	if method == "POST" {
		req.Header.ContentType = contentType
	}

	w := &bytes.Buffer{}
	bw := bufio.NewWriter(w)
	err := req.Write(bw)
	if err != nil {
		t.Fatalf("Unexpected error when calling Request.Write(): %s", err)
	}
	if err = bw.Flush(); err != nil {
		t.Fatalf("Unexpected error when flushing bufio.Writer: %s", err)
	}

	var req1 Request
	br := bufio.NewReader(w)
	if err = req1.Read(br); err != nil {
		t.Fatalf("Unexpected error when calling Request.Read(): %s", err)
	}
	if !bytes.Equal(req1.Header.Method, []byte(expectedMethod)) {
		t.Fatalf("Unexpected method: %q. Expected %q", req1.Header.Method, expectedMethod)
	}
	if !bytes.Equal(req1.Header.RequestURI, []byte(requestURI)) {
		t.Fatalf("Unexpected RequestURI: %q. Expected %q", req1.Header.RequestURI, requestURI)
	}
	if !bytes.Equal(req1.Header.Host, []byte(host)) {
		t.Fatalf("Unexpected host: %q. Expected %q", req1.Header.Host, host)
	}
	if !bytes.Equal(req1.Header.UserAgent, []byte(userAgent)) {
		t.Fatalf("Unexpected user-agent: %q. Expected %q", req1.Header.UserAgent, userAgent)
	}
	if !bytes.Equal(req1.Body, []byte(body)) {
		t.Fatalf("Unexpected body: %q. Expected %q", req1.Body, userAgent)
	}

	if method == "POST" && !bytes.Equal(req1.Header.ContentType, contentType) {
		t.Fatalf("Unexpected content-type: %q. Expected %q", req1.Header.ContentType, contentType)
	}
}

func TestRequestWriteError(t *testing.T) {
	// no requestURI
	testRequestWriteError(t, "", "", "gooble.com", "", "")

	// no host
	testRequestWriteError(t, "", "/foo/bar", "", "", "")

	// get with body
	testRequestWriteError(t, "GET", "/foo/bar", "aaa.com", "", "foobar")
}

func testRequestWriteError(t *testing.T, method, requestURI, host, userAgent, body string) {
	var req Request

	req.Header.Method = []byte(method)
	req.Header.RequestURI = []byte(requestURI)
	req.Header.Host = []byte(host)
	req.Header.UserAgent = []byte(userAgent)
	req.Body = []byte(body)

	w := &bytes.Buffer{}
	bw := bufio.NewWriter(w)
	err := req.Write(bw)
	if err == nil {
		t.Fatalf("Expecting error when writing request=%#v", req)
	}
}

func TestRequestParseURI(t *testing.T) {
	host := "foobar.com"
	requestURI := "/aaa/bb+b%20d?ccc=ddd&qqq#1334dfds&=d"
	expectedPathOriginal := "/aaa/bb+b%20d"
	expectedPath := "/aaa/bb+b d"
	expectedQueryString := "ccc=ddd&qqq"
	expectedHash := "1334dfds&=d"

	var req Request
	req.Header.Host = []byte(host)
	req.Header.RequestURI = []byte(requestURI)

	req.ParseURI()

	if string(req.URI.Host) != host {
		t.Fatalf("Unexpected host %q. Expected %q", req.URI.Host, host)
	}
	if string(req.URI.PathOriginal) != expectedPathOriginal {
		t.Fatalf("Unexpected source path %q. Expected %q", req.URI.PathOriginal, expectedPathOriginal)
	}
	if string(req.URI.Path) != expectedPath {
		t.Fatalf("Unexpected path %q. Expected %q", req.URI.Path, expectedPath)
	}
	if string(req.URI.QueryString) != expectedQueryString {
		t.Fatalf("Unexpected query string %q. Expected %q", req.URI.QueryString, expectedQueryString)
	}
	if string(req.URI.Hash) != expectedHash {
		t.Fatalf("Unexpected hash %q. Expected %q", req.URI.Hash, expectedHash)
	}
}

func TestRequestParsePostArgsSuccess(t *testing.T) {
	var req Request

	testRequestParsePostArgsSuccess(t, &req, "POST / HTTP/1.1\r\nHost: aaa.com\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 0\r\n\r\n", 0, "foo=", "=")

	testRequestParsePostArgsSuccess(t, &req, "POST / HTTP/1.1\r\nHost: aaa.com\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 18\r\n\r\nfoo&b%20r=b+z=&qwe", 3, "foo=", "b r=b z=", "qwe=")
}

func testRequestParsePostArgsSuccess(t *testing.T, req *Request, s string, expectedArgsLen int, expectedArgs ...string) {
	r := bytes.NewBufferString(s)
	br := bufio.NewReader(r)
	err := req.Read(br)
	if err != nil {
		t.Fatalf("Unexpected error when reading %q: %s", s, err)
	}
	if err = req.ParsePostArgs(); err != nil {
		t.Fatalf("Unexpected error when parsing POST args for %q: %s", s, err)
	}
	if req.PostArgs.Len() != expectedArgsLen {
		t.Fatalf("Unexpected args len %d. Expected %d for %q", req.PostArgs.Len(), expectedArgsLen, s)
	}
	for _, x := range expectedArgs {
		tmp := strings.SplitN(x, "=", 2)
		k := tmp[0]
		v := tmp[1]
		vv := req.PostArgs.Get(k)
		if vv != v {
			t.Fatalf("Unexpected value for key %q: %q. Expected %q for %q", k, vv, v, s)
		}
	}
}

func TestRequestParsePostArgsError(t *testing.T) {
	var req Request

	// non-post
	testRequestParsePostArgsError(t, &req, "GET /aa HTTP/1.1\r\nHost: aaa\r\n\r\n")

	// invalid content-type
	testRequestParsePostArgsError(t, &req, "POST /aa HTTP/1.1\r\nHost: aaa\r\nContent-Type: text/html\r\nContent-Length: 5\r\n\r\nabcde")
}

func testRequestParsePostArgsError(t *testing.T, req *Request, s string) {
	r := bytes.NewBufferString(s)
	br := bufio.NewReader(r)
	err := req.Read(br)
	if err != nil {
		t.Fatalf("Unexpected error when reading %q: %s", s, err)
	}
	if err = req.ParsePostArgs(); err == nil {
		t.Fatalf("Expecting error when parsing POST args for %q", s)
	}
}

func TestRequestReadChunked(t *testing.T) {
	var req Request

	s := "POST /foo HTTP/1.1\r\nHost: google.com\r\nTransfer-Encoding: chunked\r\nContent-Type: aa/bb\r\n\r\n3\r\nabc\r\n5\r\n12345\r\n0\r\n\r\ntrail"
	r := bytes.NewBufferString(s)
	rb := bufio.NewReader(r)
	err := req.Read(rb)
	if err != nil {
		t.Fatalf("Unexpected error when reading chunked request: %s", err)
	}
	expectedBody := "abc12345"
	if string(req.Body) != expectedBody {
		t.Fatalf("Unexpected body %q. Expected %q", req.Body, expectedBody)
	}
	verifyRequestHeader(t, &req.Header, -1, "/foo", "google.com", "", "aa/bb")
	verifyTrailer(t, rb, "trail")
}
