package fasthttp

import (
	"bufio"
	"bytes"
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
