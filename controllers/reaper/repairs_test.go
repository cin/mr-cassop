package reaper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	. "github.com/onsi/gomega"
)

func newTestReaperClient(t *testing.T, handler http.HandlerFunc) ReaperClient {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	reaperURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	return NewReaperClient(reaperURL, "test-cluster", "", "", http.DefaultClient, 1)
}

func TestRepairRuns(t *testing.T) {
	asserts := NewWithT(t)

	var requested *http.Request
	rc := newTestReaperClient(t, func(w http.ResponseWriter, r *http.Request) {
		requested = r
		_, _ = w.Write([]byte(`[
			{"id":"run-1","state":"RUNNING","keyspace_name":"ks1","owner":"mr-cassop"},
			{"id":"run-2","state":"PAUSED","keyspace_name":"ks2","owner":"someone"}]`))
	})

	runs, err := rc.RepairRuns(context.Background())
	asserts.Expect(err).NotTo(HaveOccurred())
	asserts.Expect(requested.Method).To(Equal(http.MethodGet))
	asserts.Expect(requested.URL.Path).To(Equal("/repair_run"))
	asserts.Expect(requested.URL.Query().Get("cluster_name")).To(Equal("test-cluster"))
	asserts.Expect(runs).To(Equal([]RepairRun{
		{ID: "run-1", State: RepairStateRunning, KeyspaceName: "ks1", Owner: "mr-cassop"},
		{ID: "run-2", State: RepairStatePaused, KeyspaceName: "ks2", Owner: "someone"},
	}))
}

func TestRepairRunsFailure(t *testing.T) {
	asserts := NewWithT(t)

	rc := newTestReaperClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	_, err := rc.RepairRuns(context.Background())
	asserts.Expect(err).To(BeEquivalentTo(
		&requestFailedWithStatus{code: http.StatusInternalServerError, message: "boom\n"}))
}

func TestPauseAndResumeRepairRun(t *testing.T) {
	tests := []struct {
		name         string
		call         func(rc ReaperClient) error
		expectedPath string
	}{
		{
			name:         "pause",
			call:         func(rc ReaperClient) error { return rc.PauseRepairRun(context.Background(), "run-1") },
			expectedPath: "/repair_run/run-1/state/PAUSED",
		},
		{
			name:         "resume",
			call:         func(rc ReaperClient) error { return rc.ResumeRepairRun(context.Background(), "run-1") },
			expectedPath: "/repair_run/run-1/state/RUNNING",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			asserts := NewWithT(t)

			var requested *http.Request
			rc := newTestReaperClient(t, func(w http.ResponseWriter, r *http.Request) {
				requested = r
			})

			asserts.Expect(tc.call(rc)).To(Succeed())
			asserts.Expect(requested.Method).To(Equal(http.MethodPut))
			asserts.Expect(requested.URL.Path).To(Equal(tc.expectedPath))
		})
	}
}

func TestPauseRepairRunFailure(t *testing.T) {
	asserts := NewWithT(t)

	rc := newTestReaperClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such run", http.StatusNotFound)
	})

	err := rc.PauseRepairRun(context.Background(), "run-1")
	asserts.Expect(err).To(BeEquivalentTo(&requestFailedWithStatus{code: http.StatusNotFound, message: "no such run\n"}))
}
