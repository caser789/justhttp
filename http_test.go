package fasthttp

import (
	"bufio"
	"bytes"
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
