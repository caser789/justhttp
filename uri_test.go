package fasthttp

import (
	"bytes"
	"testing"
)

func TestURIParse(t *testing.T) {
	var u URI

	// no args
	testURIParse(t, &u, "aaa", "sdfdsf",
		"http://aaa/sdfdsf", "aaa", "sdfdsf", "sdfdsf", "", "")

	// args
	testURIParse(t, &u, "xx", "/aa?ss",
		"http://xx/aa?ss", "xx", "/aa", "/aa", "ss", "")

	// args and hash
	testURIParse(t, &u, "foobar.com", "/a.b.c?def=gkl#mnop",
		"http://foobar.com/a.b.c?def=gkl#mnop", "foobar.com", "/a.b.c", "/a.b.c", "def=gkl", "mnop")

	// encoded path
	testURIParse(t, &u, "aa.com", "/Test%20+%20%D0%BF%D1%80%D0%B8?asdf=%20%20&s=12#sdf",
		"http://aa.com/Test%20+%20%D0%BF%D1%80%D0%B8?asdf=%20%20&s=12#sdf", "aa.com", "/Test + при", "/Test%20+%20%D0%BF%D1%80%D0%B8", "asdf=%20%20&s=12", "sdf")

	// host in uppercase
	testURIParse(t, &u, "FOObar.com", "/bC?De=F#Gh",
		"http://foobar.com/bC?De=F#Gh", "foobar.com", "/bC", "/bC", "De=F", "Gh")

	// uri with hostname
	testURIParse(t, &u, "xxx.com", "http://aaa.com/foo/bar?baz=aaa#ddd",
		"http://aaa.com/foo/bar?baz=aaa#ddd", "aaa.com", "/foo/bar", "/foo/bar", "baz=aaa", "ddd")
	testURIParse(t, &u, "xxx.com", "https://ab.com/f/b%20r?baz=aaa#ddd",
		"https://ab.com/f/b%20r?baz=aaa#ddd", "ab.com", "/f/b r", "/f/b%20r", "baz=aaa", "ddd")

	// no slash after hostname in uri
	testURIParse(t, &u, "aaa.com", "http://google.com",
		"http://google.com/", "google.com", "/", "/", "", "")

	// uppercase hostname in uri
	testURIParse(t, &u, "abc.com", "http://GoGLE.com/aaa",
		"http://gogle.com/aaa", "gogle.com", "/aaa", "/aaa", "", "")

	// http:// in query params
	testURIParse(t, &u, "aaa.com", "/foo?bar=http://google.com",
		"http://aaa.com/foo?bar=http://google.com", "aaa.com", "/foo", "/foo", "bar=http://google.com", "")
}

func testURIParse(t *testing.T, u *URI, host, uri,
	expectedURI, expectedHost, expectedPath, expectedPathOriginal, expectedArgs, expectedHash string) {
	u.Parse([]byte(host), []byte(uri))

	if !bytes.Equal(u.URI, []byte(expectedURI)) {
		t.Fatalf("Unexpected uri %q. Expected %q. host=%q, uri=%q", u.URI, expectedURI, host, uri)
	}
	if !bytes.Equal(u.Host, []byte(expectedHost)) {
		t.Fatalf("Unexpected host %q. Expected %q. host=%q, uri=%q", u.Host, expectedHost, host, uri)
	}
	if !bytes.Equal(u.PathOriginal, []byte(expectedPathOriginal)) {
		t.Fatalf("Unexpected original path %q. Expected %q. host=%q, uri=%q", u.PathOriginal, expectedPathOriginal, host, uri)
	}
	if !bytes.Equal(u.Path, []byte(expectedPath)) {
		t.Fatalf("Unexpected path %q. Expected %q. host=%q, uri=%q", u.Path, expectedPath, host, uri)
	}
	if !bytes.Equal(u.QueryString, []byte(expectedArgs)) {
		t.Fatalf("Unexpected args %q. Expected %q. host=%q, uri=%q", u.QueryString, expectedArgs, host, uri)
	}
	if !bytes.Equal(u.Hash, []byte(expectedHash)) {
		t.Fatalf("Unexpected hash %q. Expected %q. host=%q, uri=%q", u.Hash, expectedHash, host, uri)
	}
}
