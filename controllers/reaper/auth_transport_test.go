package reaper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"
)

func TestAuthTransportLoginsOnUnauthorized(t *testing.T) {
	asserts := NewWithT(t)

	var loginCount, clusterCallCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			atomic.AddInt32(&loginCount, 1)
			asserts.Expect(r.FormValue("username")).To(Equal("reaper-user"))
			asserts.Expect(r.FormValue("password")).To(Equal("reaper-pass"))
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"token":"test-token"}`)
		case "/cluster/test_cluster":
			atomic.AddInt32(&clusterCallCount, 1)
			if r.Header.Get("Authorization") != "Bearer test-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	reaperUrl, err := url.Parse(ts.URL)
	asserts.Expect(err).To(BeNil())

	rc := NewReaperClient(reaperUrl, "test_cluster", "reaper-user", "reaper-pass", http.DefaultClient, 1)

	exists, err := rc.ClusterExists(context.Background())
	asserts.Expect(err).To(BeNil())
	asserts.Expect(exists).To(BeTrue())
	asserts.Expect(atomic.LoadInt32(&loginCount)).To(Equal(int32(1)))
	asserts.Expect(atomic.LoadInt32(&clusterCallCount)).To(Equal(int32(2))) // unauthenticated attempt, then authenticated retry

	// A second call should reuse the cached token instead of logging in again.
	exists, err = rc.ClusterExists(context.Background())
	asserts.Expect(err).To(BeNil())
	asserts.Expect(exists).To(BeTrue())
	asserts.Expect(atomic.LoadInt32(&loginCount)).To(Equal(int32(1)))
	asserts.Expect(atomic.LoadInt32(&clusterCallCount)).To(Equal(int32(3)))
}

func TestAuthTransportLoginFailure(t *testing.T) {
	asserts := NewWithT(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "Credentials are required to access this resource.")
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer ts.Close()

	reaperUrl, err := url.Parse(ts.URL)
	asserts.Expect(err).To(BeNil())

	rc := NewReaperClient(reaperUrl, "test_cluster", "wrong-user", "wrong-pass", http.DefaultClient, 1)
	_, err = rc.ClusterExists(context.Background())
	asserts.Expect(err).ToNot(BeNil())
	asserts.Expect(err.Error()).To(ContainSubstring("reaper authentication failed"))
}
