package proxy //nolint:testpackage // unit test for unexported removeHopByHopHeaders

import (
	"net/http"
	"testing"
)

func TestRemoveHopByHopHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "X-Custom, keep-alive")
	h.Set("X-Custom", "v")
	h.Set("Transfer-Encoding", "chunked")
	h.Set("Content-Type", "application/grpc")

	removeHopByHopHeaders(h)

	if h.Get("X-Custom") != "" {
		t.Fatal("Connection-named header not stripped")
	}
	if h.Get("Transfer-Encoding") != "" {
		t.Fatal("fixed hop-by-hop not stripped")
	}
	if h.Get("Connection") != "" {
		t.Fatal("Connection header itself not stripped")
	}
	if h.Get("Content-Type") == "" {
		t.Fatal("non-hop-by-hop header stripped")
	}
}
