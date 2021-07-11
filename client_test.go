package fasthttp

import (
	"testing"
)

func TODOTestClientDo(t *testing.T) {
	statusCode, body, err := Get(nil, "http://google.com")
	if err != nil {
		t.Fatalf("unexpected error when doing http request: %s", err)
	}
	t.Fatalf("statusCode=%d, body=%q", statusCode, body)
}
