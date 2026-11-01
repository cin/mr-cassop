package controllers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	testPreviousIP       = "10.9.9.9"
	replaceArgFound      = "replace_address=[-Dcassandra.replace_address_first_boot=" + testPreviousIP + "]"
	replaceArgEmpty      = "replace_address=[]"
	fakeSSTableDumpError = "sstabledump: cannot read the sstable"
	fakeSSTableDump      = `#!/bin/bash
fixture="${1%-Data.db}.json"
[[ -f "${fixture}" ]] || { echo "` + fakeSSTableDumpError + `" >&2; exit 1; }
cat "${fixture}"
`
)

// testSSTable is a fake system.local sstable: sstabledump prints a bootstrapped cell with the given
// state and timestamp, no such cell when state is empty, or fails when fails is set.
type testSSTable struct {
	state string
	ts    string
	fails bool
}

func (s testSSTable) dump() string {
	cells := `          { "name" : "broadcast_address", "value" : "127.0.0.1", "tstamp" : "2026-10-31T20:57:38.651Z" },` + "\n"
	if s.state != "" {
		cells += fmt.Sprintf(`          { "name" : "bootstrapped", "value" : "%s", "tstamp" : "%s" },`, s.state, s.ts) + "\n"
	}
	return "[\n  {\n    \"rows\" : [\n      {\n        \"cells\" : [\n" + cells + "        ]\n      }\n    ]\n  }\n]\n"
}

// runReplaceAddressScript runs replaceAddressScript with its data path and CASSANDRA_HOME pointed at a
// temp dir holding the given sstables, and returns what it printed, including the final replace_address.
func runReplaceAddressScript(t *testing.T, previousIP string, sstables []testSSTable) string {
	t.Helper()
	asserts := NewGomegaWithT(t)

	_, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}

	root := t.TempDir()
	toolsDir := filepath.Join(root, "home", "tools", "bin")
	asserts.Expect(os.MkdirAll(toolsDir, 0o755)).To(Succeed())
	asserts.Expect(os.WriteFile(filepath.Join(toolsDir, "sstabledump"), []byte(fakeSSTableDump), 0o755)).To(Succeed())

	dataDir := filepath.Join(root, "data", "system", "local-abc")
	asserts.Expect(os.MkdirAll(dataDir, 0o755)).To(Succeed())
	for i, sstable := range sstables {
		base := filepath.Join(dataDir, fmt.Sprintf("nb-%d-big", i+1))
		asserts.Expect(os.WriteFile(base+"-Data.db", nil, 0o644)).To(Succeed())
		if !sstable.fails {
			asserts.Expect(os.WriteFile(base+".json", []byte(sstable.dump()), 0o644)).To(Succeed())
		}
	}

	script := strings.ReplaceAll(replaceAddressScript, "/var/lib/cassandra", root) +
		"\necho \"replace_address=[${replace_address}]\"\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(),
		"CASSANDRA_HOME="+filepath.Join(root, "home"),
		"CASSANDRA_NODE_PREVIOUS_IP="+previousIP,
	)
	out, err := cmd.CombinedOutput()
	asserts.Expect(err).NotTo(HaveOccurred(), string(out))

	return string(out)
}

func TestReplaceAddressScript(t *testing.T) {
	cases := []struct {
		name       string
		previousIP string
		sstables   []testSSTable
		wantArg    string
		wantLog    string // expected somewhere in the script's output, e.g. an sstabledump error
	}{
		{
			name:       "no previous IP",
			previousIP: "",
			sstables:   []testSSTable{{state: "COMPLETED", ts: "2026-10-31T20:58:11.008001Z"}},
			wantArg:    replaceArgEmpty,
		},
		{
			name:       "empty data volume",
			previousIP: testPreviousIP,
			wantArg:    replaceArgFound,
		},
		{
			name:       "bootstrap completed",
			previousIP: testPreviousIP,
			sstables:   []testSSTable{{state: "COMPLETED", ts: "2026-10-31T20:58:11.008001Z"}},
			wantArg:    replaceArgEmpty,
		},
		{
			// system.local sstables exist before a bootstrap completes
			name:       "sstable without a bootstrapped cell",
			previousIP: testPreviousIP,
			sstables:   []testSSTable{{}},
			wantArg:    replaceArgFound,
		},
		{
			name:       "bootstrap in progress",
			previousIP: testPreviousIP,
			sstables:   []testSSTable{{state: "IN_PROGRESS", ts: "2026-10-31T20:58:11.008001Z"}},
			wantArg:    replaceArgFound,
		},
		{
			name:       "older COMPLETED, newer DECOMMISSIONED",
			previousIP: testPreviousIP,
			sstables: []testSSTable{
				{state: "COMPLETED", ts: "2026-10-31T20:58:11.008001Z"},
				{state: "DECOMMISSIONED", ts: "2026-10-31T21:10:00.500000Z"},
			},
			wantArg: replaceArgFound,
		},
		{
			name:       "older IN_PROGRESS, newer COMPLETED, in the other file order",
			previousIP: testPreviousIP,
			sstables: []testSSTable{
				{state: "COMPLETED", ts: "2026-10-31T20:58:11.008001Z"},
				{state: "IN_PROGRESS", ts: "2026-10-31T20:50:00.100000Z"},
			},
			wantArg: replaceArgEmpty,
		},
		{
			// compared as text, "...:11Z" would sort after "...:11.5Z"
			name:       "timestamps with and without fractional seconds",
			previousIP: testPreviousIP,
			sstables: []testSSTable{
				{state: "DECOMMISSIONED", ts: "2026-10-31T20:58:11Z"},
				{state: "COMPLETED", ts: "2026-10-31T20:58:11.500000Z"},
			},
			wantArg: replaceArgEmpty,
		},
		{
			// a later flush can hold other system.local columns but no bootstrapped cell
			name:       "newer sstable without a bootstrapped cell, older COMPLETED",
			previousIP: testPreviousIP,
			sstables: []testSSTable{
				{state: "COMPLETED", ts: "2026-10-31T20:58:11.008001Z"},
				{},
			},
			wantArg: replaceArgEmpty,
		},
		{
			name:       "sstabledump fails",
			previousIP: testPreviousIP,
			sstables:   []testSSTable{{fails: true}},
			wantArg:    replaceArgFound,
			wantLog:    fakeSSTableDumpError, // not hidden, so the pod log shows why
		},
		{
			name:       "sstabledump fails on one sstable, the other is COMPLETED",
			previousIP: testPreviousIP,
			sstables: []testSSTable{
				{fails: true},
				{state: "COMPLETED", ts: "2026-10-31T20:58:11.008001Z"},
			},
			wantArg: replaceArgEmpty,
			wantLog: fakeSSTableDumpError,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asserts := NewGomegaWithT(t)
			out := runReplaceAddressScript(t, c.previousIP, c.sstables)
			asserts.Expect(out).To(ContainSubstring(c.wantArg))
			asserts.Expect(out).To(ContainSubstring(c.wantLog))
		})
	}
}

// A node that is joining the ring does not open CQL for minutes, so the liveness probe on the CQL port
// must not start until a startup probe on the same port has succeeded (#229).
func TestCassandraContainerStartupProbe(t *testing.T) {
	cases := []struct {
		name              string
		timeoutSeconds    *int32
		wantFailureCounts int32
	}{
		{name: "default is one hour", timeoutSeconds: nil, wantFailureCounts: 360},
		{name: "custom timeout", timeoutSeconds: ptr.To[int32](7200), wantFailureCounts: 720},
		{name: "minimum timeout", timeoutSeconds: ptr.To[int32](60), wantFailureCounts: 6},
		{name: "rounds up to a whole probe period", timeoutSeconds: ptr.To[int32](95), wantFailureCounts: 10},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asserts := NewGomegaWithT(t)

			cc := baseCC.DeepCopy()
			initializeReconciler(cc) // applies the CR defaults
			if c.timeoutSeconds != nil {
				cc.Spec.Cassandra.StartupTimeoutSeconds = c.timeoutSeconds
			}
			container := cassandraContainer(cc, cc.Spec.DCs[0], checksumContainer{}, &v1.Secret{})

			startup := container.StartupProbe
			asserts.Expect(startup).NotTo(BeNil())
			asserts.Expect(startup.TCPSocket).NotTo(BeNil())
			asserts.Expect(startup.TCPSocket.Port.String()).To(Equal("cql"))
			asserts.Expect(startup.SuccessThreshold).To(BeEquivalentTo(1))
			asserts.Expect(startup.FailureThreshold).To(Equal(c.wantFailureCounts))
			asserts.Expect(container.LivenessProbe.TCPSocket.Port).To(Equal(startup.TCPSocket.Port))
		})
	}
}

// A container built before the CR was defaulted must not panic and gets the default timeout.
func TestCassandraStartupProbeWithoutDefaulting(t *testing.T) {
	asserts := NewGomegaWithT(t)

	cc := &dbv1alpha1.CassandraCluster{Spec: dbv1alpha1.CassandraClusterSpec{Cassandra: &dbv1alpha1.Cassandra{}}}
	asserts.Expect(cassandraStartupProbe(cc).FailureThreshold).To(BeEquivalentTo(defaultStartupTimeoutSeconds / startupProbePeriodSeconds))
}
