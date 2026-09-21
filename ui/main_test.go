package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCredentials(t *testing.T, dir, user, password string) {
	t.Helper()
	for name, v := range map[string]string{credentialsUserFile: user, credentialsPasswordFile: password} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A rotated admin password must reach prober on the next request, with no restart.
func TestProxyPicksUpRotatedCredentials(t *testing.T) {
	var gotPassword string
	prober := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotPassword, _ = r.BasicAuth()
		w.Write([]byte(`[]`))
	}))
	defer prober.Close()

	dir := t.TempDir()
	environments = []Environment{{Name: "test", ProberURL: prober.URL, CredentialsDir: dir}}

	for _, password := range []string{"before-rotation", "after-rotation"} {
		writeCredentials(t, dir, "admin", password+"\n")
		rec := httptest.NewRecorder()
		handleProxy(rec, httptest.NewRequest(http.MethodGet, "/api/environments/test/nodes", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if gotPassword != password {
			t.Fatalf("prober got password %q, want %q", gotPassword, password)
		}
	}
}

func TestProxyFailsClosedWhenCredentialsMissing(t *testing.T) {
	environments = []Environment{{Name: "test", ProberURL: "http://unused", CredentialsDir: t.TempDir()}}

	rec := httptest.NewRecorder()
	handleProxy(rec, httptest.NewRequest(http.MethodGet, "/api/environments/test/nodes", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServeShutsDownOnContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, &http.Server{Addr: addr, Handler: http.NotFoundHandler()}) }()

	waitForListener(t, addr)
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("serve did not return after context cancel")
	}
}

func waitForListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never listened on %s", addr)
}
