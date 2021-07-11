package fasthttp

import (
	"testing"
)

func TODOTestClientDo(t *testing.T) {
	var req Request
	var resp Response

	req.Header.Set("HOST", "google.com")
	if err := Do(&req, &resp); err != nil {
		t.Fatalf("unexpected error when doing http request: %s", err)
	}
	t.Fatalf("statusCode=%d, body=%q", resp.Header.StatusCode, resp.Body)
}
