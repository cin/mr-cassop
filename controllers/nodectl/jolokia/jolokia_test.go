package jolokia

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

// hangingServer accepts the request and never answers until the test ends,
// like a Jolokia proxy stuck connecting to a node that drops packets.
func hangingServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	// cleanups run last-in first-out: unblock the handler before closing the server
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv
}

func TestPostTimesOutOnUnresponsiveNode(t *testing.T) {
	srv := hangingServer(t)
	c := NewClient(srv.URL, "user", "pass", zap.NewNop().Sugar())
	c.timeout = 50 * time.Millisecond

	start := time.Now()
	_, err := c.Post(context.Background(), JMXRequest{Type: "read"}, "10.0.0.1")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Post returned after %s, want it to give up at the timeout", elapsed)
	}
}

func TestPostRequestTimeoutOverridesDefault(t *testing.T) {
	srv := hangingServer(t)
	c := NewClient(srv.URL, "user", "pass", zap.NewNop().Sugar())
	c.timeout = time.Hour // would hang the test if the per-request timeout were ignored

	start := time.Now()
	_, err := c.Post(context.Background(), JMXRequest{Type: "exec", Timeout: 50 * time.Millisecond}, "10.0.0.1")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Post returned after %s, want it to honor JMXRequest.Timeout", elapsed)
	}
}

func TestPostLongTimeoutDoesNotCutOffSlowResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"value":"ok","status":200}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "user", "pass", zap.NewNop().Sugar())
	c.timeout = 50 * time.Millisecond // the default would cut this call off

	resp, err := c.Post(context.Background(), JMXRequest{Type: "exec", Timeout: 5 * time.Second}, "10.0.0.1")
	if err != nil {
		t.Fatalf("Post with a longer request timeout failed: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("status = %d, want 200", resp.Status)
	}
}
