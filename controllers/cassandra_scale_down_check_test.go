package controllers

import (
	"context"
	"strconv"
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/cql"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/jobs"
	"github.com/cin/mr-cassop/controllers/mocks"
	"github.com/cin/mr-cassop/controllers/nodectl"
)

func ntsKeyspace(name string, rfs map[string]string) cql.Keyspace {
	replication := map[string]string{"class": cql.ReplicationClassNetworkTopologyStrategy}
	for dc, rf := range rfs {
		replication[dc] = rf
	}
	return cql.Keyspace{Name: name, Replication: replication}
}

func TestKeyspacesBlockingScaleDown(t *testing.T) {
	keyspaces := []cql.Keyspace{
		ntsKeyspace("system_auth", map[string]string{"dc1": "3"}),
		ntsKeyspace("reaper_db", map[string]string{"dc1": "3", "dc2": "3"}),
		ntsKeyspace("user_ks", map[string]string{"dc1": "2"}),
		ntsKeyspace("transient_ks", map[string]string{"dc1": "3/1"}),
		ntsKeyspace("other_dc_only", map[string]string{"dc2": "3"}),
		{Name: "simple_ks", Replication: map[string]string{"class": "org.apache.cassandra.locator.SimpleStrategy", "replication_factor": "3"}},
		{Name: "system", Replication: map[string]string{"class": "org.apache.cassandra.locator.LocalStrategy"}},
	}

	tests := []struct {
		name  string
		dc    string
		nodes int
		want  []string
	}{
		{"3 nodes cannot drop below RF 3", "dc1", 3, []string{"reaper_db (RF 3)", "simple_ks (RF 3)", "system_auth (RF 3)", "transient_ks (RF 3)"}},
		{"4 nodes can go to 3", "dc1", 4, nil},
		{"2 nodes are blocked by RF 2 as well", "dc1", 2, []string{"reaper_db (RF 3)", "simple_ks (RF 3)", "system_auth (RF 3)", "transient_ks (RF 3)", "user_ks (RF 2)"}},
		// SimpleStrategy replicates across the whole cluster, so it counts for any DC
		{"only keyspaces replicated to the DC count", "dc2", 3, []string{"other_dc_only (RF 3)", "reaper_db (RF 3)", "simple_ks (RF 3)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			NewGomegaWithT(t).Expect(keyspacesBlockingScaleDown(keyspaces, tt.dc, tt.nodes)).To(Equal(tt.want))
		})
	}
}

type scaleDownFixture struct {
	reconciler *CassandraClusterReconciler
	recorder   *record.FakeRecorder
	nctl       *mocks.MockNodectl
	cql        *mocks.MockCqlClient
	cc         *v1alpha1.CassandraCluster
	sts        appsv1.StatefulSet
	pods       *v1.PodList
}

// newScaleDownFixture sets up a dc1 scale-down from 3 to 2 nodes where dc1-2 is a NORMAL node.
// dc1 is listed in the CR, so it stays.
func newScaleDownFixture(t *testing.T, keyspaces []cql.Keyspace) *scaleDownFixture {
	t.Helper()
	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: v1alpha1.CassandraClusterSpec{
			DCs:                 []v1alpha1.DC{{Name: "dc1", Replicas: proto.Int(2)}},
			AdminRoleSecretName: "admin-secret",
		},
	}
	adminSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin-secret", Namespace: "default"},
		Data:       map[string][]byte{v1alpha1.CassandraOperatorAdminRole: []byte("admin"), v1alpha1.CassandraOperatorAdminPassword: []byte("pass")},
	}
	sts := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-cassandra-dc1",
			Namespace: "default",
			Labels:    map[string]string{v1alpha1.CassandraClusterDC: "dc1"},
		},
		Spec: appsv1.StatefulSetSpec{Replicas: proto.Int32(3)},
	}
	pods := &v1.PodList{}
	for i, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		pods.Items = append(pods.Items, v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-cassandra-dc1-" + strconv.Itoa(i),
				Namespace: "default",
				Labels:    map[string]string{v1alpha1.CassandraClusterDC: "dc1"},
			},
			Status: v1.PodStatus{PodIP: ip},
		})
	}

	ctrl := gomock.NewController(t)
	nctl := mocks.NewMockNodectl(ctrl)
	nctl.EXPECT().OperationMode(gomock.Any(), "10.0.0.3").Return(nodectl.NodeOperationModeNormal, nil).AnyTimes()
	cqlClient := mocks.NewMockCqlClient(ctrl)
	cqlClient.EXPECT().GetKeyspacesInfo().Return(keyspaces, nil).AnyTimes()
	cqlClient.EXPECT().CloseSession().AnyTimes()
	recorder := record.NewFakeRecorder(10)

	return &scaleDownFixture{
		reconciler: &CassandraClusterReconciler{
			Client:        fake.NewClientBuilder().WithScheme(baseScheme).WithObjects(&sts, adminSecret).Build(),
			Scheme:        baseScheme,
			Log:           zap.NewNop().Sugar(),
			Jobs:          jobs.NewJobManager(make(chan event.GenericEvent, 10), zap.NewNop().Sugar()),
			Events:        events.NewEventRecorder(recorder),
			NodectlClient: func(string, string, string, *zap.SugaredLogger) nodectl.Nodectl { return nctl },
			CqlClient:     func(*gocql.ClusterConfig) (cql.CqlClient, error) { return cqlClient, nil },
		},
		recorder: recorder, nctl: nctl, cql: cqlClient, cc: cc, sts: sts, pods: pods,
	}
}

func (f *scaleDownFixture) handle() error {
	broadcast := map[string]string{
		"test-cluster-cassandra-dc1-0": "10.0.0.1",
		"test-cluster-cassandra-dc1-1": "10.0.0.2",
		"test-cluster-cassandra-dc1-2": "10.0.0.3",
	}
	return f.reconciler.handlePodDecommission(context.Background(), f.cc, f.sts, broadcast, "test-cluster-cassandra-dc1-2", f.pods)
}

// https://github.com/cin/mr-cassop/issues/178: Cassandra refuses to decommission a node of a 3 node DC
// while a keyspace has RF 3 there. The operator used to start the decommission anyway.
func TestHandlePodDecommission_BlockedByKeyspaceReplication(t *testing.T) {
	asserts := NewGomegaWithT(t)
	f := newScaleDownFixture(t, []cql.Keyspace{
		ntsKeyspace("system_auth", map[string]string{"dc1": "3"}),
		ntsKeyspace("user_ks", map[string]string{"dc1": "3"}),
		ntsKeyspace("system_traces", map[string]string{"dc1": "2"}),
	})

	err := f.handle() // no Decommission call is expected on the mock
	asserts.Expect(err).To(MatchError(errScaleDownBlocked))
	asserts.Expect(f.recorder.Events).To(Receive(And(
		ContainSubstring(events.EventScaleDownBlocked),
		ContainSubstring("system_auth (RF 3)"),
		ContainSubstring("user_ks (RF 3)"),
		Not(ContainSubstring("system_traces")))))
	asserts.Expect(f.reconciler.Jobs.Exists("pod-decommission-test-cluster-cassandra-dc1-2")).To(BeFalse())
}

func TestHandlePodDecommission_StartsOnceReplicationAllows(t *testing.T) {
	asserts := NewGomegaWithT(t)
	f := newScaleDownFixture(t, []cql.Keyspace{
		ntsKeyspace("system_auth", map[string]string{"dc1": "2"}),
		ntsKeyspace("user_ks", map[string]string{"dc1": "1"}),
	})
	started := make(chan struct{})
	f.nctl.EXPECT().Decommission(gomock.Any(), "10.0.0.3").DoAndReturn(func(context.Context, string) error {
		close(started)
		return nil
	})

	asserts.Expect(f.handle()).To(Succeed())
	asserts.Eventually(started).Should(BeClosed())
}

// The keyspaces of a DC that's being removed are handled by handleDCsDecommission, so the check must
// leave that path alone.
func TestCheckScaleDownNotBlockedByReplication_SkipsDCBeingRemoved(t *testing.T) {
	asserts := NewGomegaWithT(t)
	f := newScaleDownFixture(t, []cql.Keyspace{ntsKeyspace("system_auth", map[string]string{"dc1": "3"})})
	f.cc.Spec.DCs = []v1alpha1.DC{{Name: "dc2", Replicas: proto.Int(3)}}
	f.reconciler.CqlClient = func(*gocql.ClusterConfig) (cql.CqlClient, error) {
		t.Error("the keyspaces of a DC being removed must not be checked")
		return nil, nil
	}

	asserts.Expect(f.reconciler.checkScaleDownNotBlockedByReplication(f.cc, f.sts, "admin", "pass")).To(Succeed())
}
