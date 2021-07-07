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

func TestResponseReadWithoutBody(t *testing.T) {
	var resp Response

	testResponseReadWithoutBody(t, &resp, "HTTP/1.1 304 Not Modified\r\nContent-Type: aa\r\nContent-Length: 1235\r\n\r\nfoobar", false,
		304, 1235, "aa", "foobar")

	testResponseReadWithoutBody(t, &resp, "HTTP/1.1 204 Foo Bar\r\nContent-Type: aab\r\nTransfer-Encoding: chunked\r\n\r\n123\r\nss", false,
		204, -1, "aab", "123\r\nss")

	testResponseReadWithoutBody(t, &resp, "HTTP/1.1 100 AAA\r\nContent-Type: xxx\r\nContent-Length: 3434\r\n\r\naaaa", false,
		100, 3434, "xxx", "aaaa")

	testResponseReadWithoutBody(t, &resp, "HTTP 200 OK\r\nContent-Type: text/xml\r\nContent-Length: 123\r\n\r\nxxxx", true,
		200, 123, "text/xml", "xxxx")
}

func testResponseReadWithoutBody(t *testing.T, resp *Response, s string, skipBody bool,
	expectedStatusCode, expectedContentLength int, expectedContentType, expectedTrailer string) {
	r := bytes.NewBufferString(s)
	rb := bufio.NewReader(r)
	resp.SkipBody = skipBody
	err := resp.Read(rb)
	if err != nil {
		t.Fatalf("Unexpected error when reading response without body: %s. response=%q", err, s)
	}
	if len(resp.Body) != 0 {
		t.Fatalf("Unexpected response body %q. Expected %q. response=%q", resp.Body, "", s)
	}
	verifyResponseHeader(t, &resp.Header, expectedStatusCode, expectedContentLength, expectedContentType)
	verifyTrailer(t, rb, expectedTrailer)

	// verify that ordinal response is read after null-body response
	testResponseReadSuccess(t, resp, "HTTP/1.1 300 OK\r\nContent-Length: 5\r\nContent-Type: bar\r\n\r\n56789aaa",
		300, 5, "bar", "56789", "aaa")
}

func TestResponseSuccess(t *testing.T) {
	// 200 response
	testResponseSuccess(t, 200, "test/plain", "server", "foobar",
		200, "test/plain", "server")

	// response with missing statusCode
	testResponseSuccess(t, 0, "text/plain", "server", "foobar",
		200, "text/plain", "server")

	// response with missing server
	testResponseSuccess(t, 500, "aaa", "", "aaadfsd",
		500, "aaa", string(defaultServerName))

	// empty body
	testResponseSuccess(t, 200, "bbb", "qwer", "",
		200, "bbb", "qwer")

	// missing content-type
	testResponseSuccess(t, 200, "", "asdfsd", "asdf",
		200, string(defaultContentType), "asdfsd")
}

func testResponseSuccess(t *testing.T, statusCode int, contentType, serverName, body string,
	expectedStatusCode int, expectedContentType, expectedServerName string) {
	var resp Response
	resp.Header.StatusCode = statusCode
	resp.Header.ContentType = []byte(contentType)
	resp.Header.Server = []byte(serverName)
	resp.Body = []byte(body)

	w := &bytes.Buffer{}
	bw := bufio.NewWriter(w)
	err := resp.Write(bw)
	if err != nil {
		t.Fatalf("Unexpected error when calling Response.Write(): %s", err)
	}
	if err = bw.Flush(); err != nil {
		t.Fatalf("Unexpected error when flushing bufio.Writer: %s", err)
	}

	var resp1 Response
	br := bufio.NewReader(w)
	if err = resp1.Read(br); err != nil {
		t.Fatalf("Unexpected error when calling Response.Read(): %s", err)
	}
	if resp1.Header.StatusCode != expectedStatusCode {
		t.Fatalf("Unexpected status code: %d. Expected %d", resp1.Header.StatusCode, expectedStatusCode)
	}
	if resp1.Header.ContentLength != len(body) {
		t.Fatalf("Unexpected content-length: %d. Expected %d", resp1.Header.ContentLength, len(body))
	}
	if !bytes.Equal(resp1.Header.ContentType, []byte(expectedContentType)) {
		t.Fatalf("Unexpected content-type: %q. Expected %q", resp1.Header.ContentType, expectedContentType)
	}
	if !bytes.Equal(resp1.Header.Server, []byte(expectedServerName)) {
		t.Fatalf("Unexpected server: %q. Expected %q", resp1.Header.Server, expectedServerName)
	}
	if !bytes.Equal(resp1.Body, []byte(body)) {
		t.Fatalf("Unexpected body: %q. Expected %q", resp1.Body, body)
	}
}

func TestResponseWriteError(t *testing.T) {
	var resp Response

	// negative statusCode
	resp.Header.StatusCode = -1234
	w := &bytes.Buffer{}
	bw := bufio.NewWriter(w)
	err := resp.Write(bw)
	if err == nil {
		t.Fatalf("Expecting error when writing response=%#v", resp)
	}
}

func TestResponseReadSuccess(t *testing.T) {
	resp := &Response{}

	// usual response
	testResponseReadSuccess(t, resp, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\nContent-Type: foo/bar\r\n\r\n0123456789",
		200, 10, "foo/bar", "0123456789", "")

	// zero response
	testResponseReadSuccess(t, resp, "HTTP/1.1 500 OK\r\nContent-Length: 0\r\nContent-Type: foo/bar\r\n\r\n",
		500, 0, "foo/bar", "", "")

	// response with trailer
	testResponseReadSuccess(t, resp, "HTTP/1.1 300 OK\r\nContent-Length: 5\r\nContent-Type: bar\r\n\r\n56789aaa",
		300, 5, "bar", "56789", "aaa")

	// chunked response
	testResponseReadSuccess(t, resp, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nTransfer-Encoding: chunked\r\n\r\n4\r\nqwer\r\n2\r\nty\r\n0\r\n\r\nzzzzz",
		200, -1, "text/html", "qwerty", "zzzzz")

	// zero chunked response
	testResponseReadSuccess(t, resp, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\nzzz",
		200, -1, "text/html", "", "zzz")
}

func TestResponseReadError(t *testing.T) {
	resp := &Response{}

	// empty response
	testResponseReadError(t, resp, "")

	// invalid header
	testResponseReadError(t, resp, "foobar")

	// empty body
	testResponseReadError(t, resp, "HTTP/1.1 200 OK\r\nContent-Type: aaa\r\nContent-Length: 1234\r\n\r\n")

	// short body
	testResponseReadError(t, resp, "HTTP/1.1 200 OK\r\nContent-Type: aaa\r\nContent-Length: 1234\r\n\r\nshort")
}

func testResponseReadError(t *testing.T, resp *Response, response string) {
	r := bytes.NewBufferString(response)
	rb := bufio.NewReader(r)
	err := resp.Read(rb)
	if err == nil {
		t.Fatalf("Expecting error for response=%q", response)
	}

	testResponseReadSuccess(t, resp, "HTTP/1.1 303 Redisred sedfs sdf\r\nContent-Type: aaa\r\nContent-Length: 5\r\n\r\nHELLOaaa",
		303, 5, "aaa", "HELLO", "aaa")
}

func testResponseReadSuccess(t *testing.T, resp *Response, response string, expectedStatusCode, expectedContentLength int,
	expectedContenType, expectedBody, expectedTrailer string) {

	r := bytes.NewBufferString(response)
	rb := bufio.NewReader(r)
	err := resp.Read(rb)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}

	verifyResponseHeader(t, &resp.Header, expectedStatusCode, expectedContentLength, expectedContenType)
	if !bytes.Equal(resp.Body, []byte(expectedBody)) {
		t.Fatalf("Unexpected body %q. Expected %q", resp.Body, []byte(expectedBody))
	}
	verifyTrailer(t, rb, expectedTrailer)
}