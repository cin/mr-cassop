package jolokia

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Jolokia echoes the request back in every response, including the JMX target's credentials. The
// error returned from Post is logged and published as an event, so it must never carry them, not
// even with debug logging on.
func TestPostErrorDoesNotLeakCredentials(t *testing.T) {
	const secret = "s3cr3t-jmx-password"
	echo := `{"request":{"target":{"user":"admin","password":"` + secret + `"},"type":"exec"},"error":"java.net.SocketTimeoutException: Read timed out","status":500}`
	tests := []struct {
		name       string
		httpStatus int
		body       string
	}{
		{"jmx error in a 200 response", http.StatusOK, echo},
		{"http error status", http.StatusInternalServerError, echo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.httpStatus)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c := NewClient(srv.URL, "user", "pass", zap.NewExample().Sugar()) // debug level

			_, err := c.Post(context.Background(), JMXRequest{Type: "exec"}, "10.0.0.1")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error leaks the JMX password: %v", err)
			}
			if !strings.Contains(err.Error(), "Read timed out") {
				t.Errorf("error lost Jolokia's message: %v", err)
			}
		})
	}
}
