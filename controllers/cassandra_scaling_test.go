package controllers

import (
	"context"
	"strconv"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/cql"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/jobs"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/mocks"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/nodectl"
	"github.com/cin/mr-cassop/controllers/util"
)

// TestHandlePodDecommission_UnscheduledPod covers the scale-down deadlock from
// https://github.com/cin/mr-cassop/issues/119: a pod that never got a PodIP (e.g. left
// over from a scale-up that exceeded cluster capacity) never joined the ring, so it
// should be scaled away directly instead of going through nodectl decommission.
func TestHandlePodDecommission_UnscheduledPod(t *testing.T) {
	asserts := NewGomegaWithT(t)

	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: v1alpha1.CassandraClusterSpec{
			DCs: []v1alpha1.DC{
				{Name: "dc1", Replicas: proto.Int(4)},
			},
		},
	}

	sts := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-cassandra-dc1",
			Namespace: "default",
			Labels:    map[string]string{v1alpha1.CassandraClusterDC: "dc1"},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: proto.Int32(5), // still at the old, higher count
		},
	}

	stuckPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-cassandra-dc1-4",
			Namespace: "default",
			UID:       types.UID("uid5"),
			Labels:    map[string]string{v1alpha1.CassandraClusterDC: "dc1"},
		},
		Status: v1.PodStatus{
			// no PodIP - pod is Pending and never joined the cluster
		},
	}

	podList := &v1.PodList{Items: []v1.Pod{stuckPod}}

	tClient := fake.NewClientBuilder().WithScheme(baseScheme).WithObjects(&sts).Build()

	reconciler := &CassandraClusterReconciler{
		Client: tClient,
		Scheme: baseScheme,
		Log:    zap.NewNop().Sugar(),
		Jobs:   jobs.NewJobManager(nil, zap.NewNop().Sugar()),
	}

	err := reconciler.handlePodDecommission(context.Background(), cc, sts, map[string]string{}, stuckPod.Name, podList)
	asserts.Expect(err).To(BeNil())

	updatedSts := &appsv1.StatefulSet{}
	asserts.Expect(tClient.Get(context.Background(), client.ObjectKeyFromObject(&sts), updatedSts)).To(Succeed())
	asserts.Expect(*updatedSts.Spec.Replicas).To(BeEquivalentTo(4))
}

type decommissionModeFixture struct {
	reconciler *CassandraClusterReconciler
	client     client.Client
	recorder   *record.FakeRecorder
	nctl       *mocks.MockNodectl
	cc         *v1alpha1.CassandraCluster
	sts        appsv1.StatefulSet
	pods       *v1.PodList
	podName    string
}

// newDecommissionModeFixture sets up a dc1 scale-down from 3 to 2 nodes, where dc1-2 reports opMode.
func newDecommissionModeFixture(t *testing.T, opMode nodectl.OperationMode) *decommissionModeFixture {
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

	nctl := mocks.NewMockNodectl(gomock.NewController(t))
	nctl.EXPECT().OperationMode(gomock.Any(), "10.0.0.3").Return(opMode, nil)
	// no keyspace blocks the scale-down here; that check has its own tests
	cqlClient := mocks.NewMockCqlClient(gomock.NewController(t))
	cqlClient.EXPECT().GetKeyspacesInfo().Return(nil, nil).AnyTimes()
	cqlClient.EXPECT().CloseSession().AnyTimes()
	tClient := fake.NewClientBuilder().WithScheme(baseScheme).WithObjects(&sts, adminSecret).Build()
	recorder := record.NewFakeRecorder(10)

	return &decommissionModeFixture{
		reconciler: &CassandraClusterReconciler{
			Client:        tClient,
			Scheme:        baseScheme,
			Log:           zap.NewNop().Sugar(),
			Jobs:          jobs.NewJobManager(make(chan event.GenericEvent, 10), zap.NewNop().Sugar()),
			Events:        events.NewEventRecorder(recorder),
			NodectlClient: func(string, string, string, *zap.SugaredLogger) nodectl.Nodectl { return nctl },
			CqlClient:     func(*gocql.ClusterConfig) (cql.CqlClient, error) { return cqlClient, nil },
		},
		client: tClient, recorder: recorder, nctl: nctl, cc: cc, sts: sts, pods: pods,
		podName: "test-cluster-cassandra-dc1-2",
	}
}

func (f *decommissionModeFixture) handle() error {
	broadcast := map[string]string{
		"test-cluster-cassandra-dc1-0": "10.0.0.1",
		"test-cluster-cassandra-dc1-1": "10.0.0.2",
		"test-cluster-cassandra-dc1-2": "10.0.0.3",
	}
	return f.reconciler.handlePodDecommission(context.Background(), f.cc, f.sts, broadcast, f.podName, f.pods)
}

func (f *decommissionModeFixture) replicas(asserts *GomegaWithT) int32 {
	sts := &appsv1.StatefulSet{}
	asserts.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(&f.sts), sts)).To(Succeed())
	return *sts.Spec.Replicas
}

// https://github.com/cin/mr-cassop/issues/197: a node in DECOMMISSION_FAILED or JOINING_FAILED used
// to fail every reconcile with "unknown operation mode", with nothing telling the user why.
func TestHandlePodDecommission_FailedModesAreReported(t *testing.T) {
	tests := []struct {
		mode  nodectl.OperationMode
		event string
	}{
		{nodectl.NodeOperationModeDecommissionFailed, events.EventDecommissionFailed},
		{nodectl.NodeOperationModeJoiningFailed, events.EventNodeJoiningFailed},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			asserts := NewGomegaWithT(t)
			f := newDecommissionModeFixture(t, tt.mode)

			err := f.handle() // no Decommission call is expected on the mock
			asserts.Expect(err).To(MatchError(ContainSubstring(string(tt.mode))))
			asserts.Expect(err).ToNot(MatchError(ContainSubstring("unknown operation mode")))
			asserts.Expect(f.recorder.Events).To(Receive(ContainSubstring(tt.event)))
			asserts.Expect(f.replicas(asserts)).To(BeEquivalentTo(3))
		})
	}
}

// Only a NORMAL node is decommissioned. Any other mode used to fall through to starting a decommission.
func TestHandlePodDecommission_TransientModesWait(t *testing.T) {
	for _, mode := range []nodectl.OperationMode{
		nodectl.NodeOperationModeLeaving, nodectl.NodeOperationModeStarting, nodectl.NodeOperationModeJoining,
		nodectl.NodeOperationModeMoving, nodectl.NodeOperationModeDraining, nodectl.NodeOperationModeDrained,
	} {
		t.Run(string(mode), func(t *testing.T) {
			asserts := NewGomegaWithT(t)
			f := newDecommissionModeFixture(t, mode)

			asserts.Expect(f.handle()).To(Succeed()) // no Decommission call is expected on the mock
			asserts.Expect(f.replicas(asserts)).To(BeEquivalentTo(3))
			asserts.Expect(f.reconciler.Jobs.IsRunning("pod-decommission-" + f.podName)).To(BeFalse())
		})
	}
}

func TestHandlePodDecommission_NormalNodeStartsDecommission(t *testing.T) {
	asserts := NewGomegaWithT(t)
	f := newDecommissionModeFixture(t, nodectl.NodeOperationModeNormal)
	decommissioned := make(chan struct{})
	f.nctl.EXPECT().Decommission(gomock.Any(), "10.0.0.3").DoAndReturn(func(context.Context, string) error {
		close(decommissioned)
		return nil
	})

	asserts.Expect(f.handle()).To(Succeed())
	asserts.Eventually(decommissioned).Should(BeClosed())
}

// https://github.com/cin/mr-cassop/issues/213: jobs were never registered, so a reconcile arriving while a
// decommission call was still blocked started a second one. The mocks allow one OperationMode and one
// Decommission call, so the second reconcile must return before reaching either.
func TestHandlePodDecommission_RunningJobIsNotStartedTwice(t *testing.T) {
	asserts := NewGomegaWithT(t)
	f := newDecommissionModeFixture(t, nodectl.NodeOperationModeNormal)
	started, release := make(chan struct{}), make(chan struct{})
	f.nctl.EXPECT().Decommission(gomock.Any(), "10.0.0.3").DoAndReturn(func(context.Context, string) error {
		close(started)
		<-release
		return nil
	})

	asserts.Expect(f.handle()).To(Succeed())
	asserts.Eventually(started).Should(BeClosed())
	asserts.Expect(f.reconciler.Jobs.IsRunning("pod-decommission-" + f.podName)).To(BeTrue())

	asserts.Expect(f.handle()).To(Succeed()) // decommission in progress, waits

	close(release)
	asserts.Eventually(func() bool { return f.reconciler.Jobs.IsRunning("pod-decommission-" + f.podName) }).Should(BeFalse())
}

func TestHandlePodDecommission_FailedDecommissionJobIsReported(t *testing.T) {
	asserts := NewGomegaWithT(t)
	f := newDecommissionModeFixture(t, nodectl.NodeOperationModeNormal)
	f.nctl.EXPECT().Decommission(gomock.Any(), "10.0.0.3").
		Return(errors.New("Not enough live nodes to maintain replication factor in keyspace system_auth"))
	// Cassandra refused it, so the node is not leaving the ring
	f.nctl.EXPECT().OperationMode(gomock.Any(), "10.0.0.3").Return(nodectl.NodeOperationModeDecommissionFailed, nil)

	asserts.Expect(f.handle()).To(Succeed())
	asserts.Eventually(f.recorder.Events).Should(Receive(And(
		ContainSubstring(events.EventDecommissionFailed), ContainSubstring("system_auth"))))
}

// The Jolokia-to-JMX call has its own read timeout (about 30s), far shorter than a real decommission.
// It returns an error while Cassandra keeps decommissioning, so a node that's LEAVING or already
// DECOMMISSIONED must not be reported as a failed decommission.
func TestHandlePodDecommission_TimedOutCallOnLeavingNodeIsNotAFailure(t *testing.T) {
	for _, mode := range []nodectl.OperationMode{nodectl.NodeOperationModeLeaving, nodectl.NodeOperationModeDecommissioned} {
		t.Run(string(mode), func(t *testing.T) {
			asserts := NewGomegaWithT(t)
			f := newDecommissionModeFixture(t, nodectl.NodeOperationModeNormal)
			modeChecked := make(chan struct{})
			f.nctl.EXPECT().Decommission(gomock.Any(), "10.0.0.3").
				Return(errors.New("received error JMX response; error: java.net.SocketTimeoutException: Read timed out"))
			f.nctl.EXPECT().OperationMode(gomock.Any(), "10.0.0.3").DoAndReturn(func(context.Context, string) (nodectl.OperationMode, error) {
				close(modeChecked)
				return mode, nil
			})

			asserts.Expect(f.handle()).To(Succeed())
			asserts.Eventually(modeChecked).Should(BeClosed())
			asserts.Consistently(f.recorder.Events, 300*time.Millisecond).ShouldNot(Receive())
		})
	}
}

// A node that finished decommissioning but still answers JMX reports DECOMMISSIONED. It used to fall
// through to starting another decommission; now it's scaled away once peers no longer see it.
func TestHandlePodDecommission_DecommissionedNodeIsScaledDown(t *testing.T) {
	asserts := NewGomegaWithT(t)
	f := newDecommissionModeFixture(t, nodectl.NodeOperationModeDecommissioned)
	view := nodectl.ClusterView{
		LiveNodes:        []string{"10.0.0.1", "10.0.0.2"},
		EndpointToHostId: map[string]string{"10.0.0.1": "host-1", "10.0.0.2": "host-2"},
	}
	f.nctl.EXPECT().ClusterView(gomock.Any(), gomock.Any()).Return(view, nil).AnyTimes()

	asserts.Expect(f.handle()).To(Succeed())
	asserts.Expect(f.replicas(asserts)).To(BeEquivalentTo(2))
}

// https://github.com/cin/mr-cassop/issues/175: a pod with no IP isn't necessarily one that never
// joined. A pod that went Ready once is recorded in the pod IPs configmap; scaling it away would
// leave its node in the ring as down.
func TestHandlePodDecommission_PodWithoutIPThatJoinedEarlier(t *testing.T) {
	asserts := NewGomegaWithT(t)

	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec:       v1alpha1.CassandraClusterSpec{DCs: []v1alpha1.DC{{Name: "dc1", Replicas: proto.Int(4)}}},
	}
	sts := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-cassandra-dc1",
			Namespace: "default",
			Labels:    map[string]string{v1alpha1.CassandraClusterDC: "dc1"},
		},
		Spec: appsv1.StatefulSetSpec{Replicas: proto.Int32(5)},
	}
	// Pending: its Kubernetes node died, so it has no IP, but it went Ready before
	pendingPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-cassandra-dc1-4",
			Namespace: "default",
			Labels:    map[string]string{v1alpha1.CassandraClusterDC: "dc1"},
		},
	}
	podIPsCM := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: names.PodIPsConfigMap(cc.Name), Namespace: "default"},
		Data:       map[string]string{pendingPod.Name: "10.0.0.5"},
	}

	tClient := fake.NewClientBuilder().WithScheme(baseScheme).WithObjects(&sts, podIPsCM).Build()
	recorder := record.NewFakeRecorder(10)
	reconciler := &CassandraClusterReconciler{
		Client: tClient,
		Scheme: baseScheme,
		Log:    zap.NewNop().Sugar(),
		Jobs:   jobs.NewJobManager(nil, zap.NewNop().Sugar()),
		Events: events.NewEventRecorder(recorder),
	}

	err := reconciler.handlePodDecommission(context.Background(), cc, sts, map[string]string{}, pendingPod.Name, &v1.PodList{Items: []v1.Pod{pendingPod}})
	asserts.Expect(err).To(HaveOccurred())

	updatedSts := &appsv1.StatefulSet{}
	asserts.Expect(tClient.Get(context.Background(), client.ObjectKeyFromObject(&sts), updatedSts)).To(Succeed())
	asserts.Expect(*updatedSts.Spec.Replicas).To(BeEquivalentTo(5), "a pod that joined the ring must not be scaled away undecommissioned")
	asserts.Expect(recorder.Events).To(Receive(ContainSubstring(events.EventScaleDownWaitingForNode)))
}

// A node that's down shows up in UnreachableNodes, not LiveNodes. It must not count as
// decommissioned: with WhenScaled: Delete, scaling it away would delete the PVC of a node that
// still owns tokens.
func TestPodDecommissioned(t *testing.T) {
	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec:       v1alpha1.CassandraClusterSpec{DCs: []v1alpha1.DC{{Name: "dc1", Replicas: proto.Int(2)}}},
	}
	dcLabels := map[string]string{v1alpha1.CassandraClusterDC: "dc1"}
	pods := []v1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1-0", Labels: dcLabels}},
		{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1-1", Labels: dcLabels}},
		{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1-2", Labels: dcLabels}},
	}
	broadcastAddresses := map[string]string{
		"test-cluster-cassandra-dc1-0": "10.0.0.1",
		"test-cluster-cassandra-dc1-1": "10.0.0.2",
		"test-cluster-cassandra-dc1-2": "10.0.0.3",
	}

	live := []string{"10.0.0.1", "10.0.0.2"}
	remaining := map[string]string{"10.0.0.1": "host-1", "10.0.0.2": "host-2"}
	withNode3 := map[string]string{"10.0.0.1": "host-1", "10.0.0.2": "host-2", "10.0.0.3": "host-3"}
	tests := []struct {
		name    string
		view    nodectl.ClusterView
		want    bool
		wantErr bool
	}{
		// what the kind test showed: after decommission, peers still list the node as
		// unreachable (gossip status LEFT), but it's gone from token metadata
		{"left the ring", nodectl.ClusterView{LiveNodes: live, UnreachableNodes: []string{"10.0.0.3"}, EndpointToHostId: remaining}, true, false},
		{"down but still in the ring", nodectl.ClusterView{LiveNodes: live, UnreachableNodes: []string{"10.0.0.3"}, EndpointToHostId: withNode3}, false, false},
		{"still leaving", nodectl.ClusterView{LiveNodes: append(live, "10.0.0.3"), LeavingNodes: []string{"10.0.0.3"}, EndpointToHostId: withNode3}, false, false},
		{"no token metadata", nodectl.ClusterView{LiveNodes: live}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asserts := NewGomegaWithT(t)
			nctl := mocks.NewMockNodectl(gomock.NewController(t))
			nctl.EXPECT().ClusterView(gomock.Any(), gomock.Any()).Return(tt.view, nil).AnyTimes()

			reconciler := &CassandraClusterReconciler{Log: zap.NewNop().Sugar()}
			got, err := reconciler.podDecommissioned(context.Background(), cc, nctl, pods, pods[2], broadcastAddresses)
			if tt.wantErr {
				asserts.Expect(err).To(HaveOccurred())
				return
			}
			asserts.Expect(err).To(Succeed())
			asserts.Expect(got).To(Equal(tt.want))
		})
	}
}

// A scale-up that has to wait for a decommissioned PVC to be deleted must only hold back its own
// StatefulSet: the other DCs still scale in the same pass.
func TestReconcileCassandraScaling_BlockedScaleUpDoesNotBlockOtherDCs(t *testing.T) {
	asserts := NewGomegaWithT(t)

	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: v1alpha1.CassandraClusterSpec{DCs: []v1alpha1.DC{
			{Name: "dc1", Replicas: proto.Int(3)},
			{Name: "dc2", Replicas: proto.Int(3)},
		}},
	}
	newSts := func(dc string) *appsv1.StatefulSet {
		return &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-cassandra-" + dc,
				Namespace: "default",
				Labels:    util.MergeMap(map[string]string{v1alpha1.CassandraClusterDC: dc}, labels.Cassandra(cc)),
			},
			Spec: appsv1.StatefulSetSpec{Replicas: proto.Int32(2)},
		}
	}
	// dc1-2 was decommissioned and its PVC is still being deleted (deletion timestamp set)
	pendingPVC := testPVC("data-test-cluster-cassandra-dc1-2", map[string]string{decommissionedPVCAnnotation: "test-cluster-cassandra-dc1-2"})
	pendingPVC.Finalizers = []string{"kubernetes.io/pvc-protection"}
	pendingPVC.DeletionTimestamp = &metav1.Time{Time: time.Now()}

	tClient := fake.NewClientBuilder().WithScheme(baseScheme).WithObjects(newSts("dc1"), newSts("dc2"), pendingPVC).Build()
	recorder := record.NewFakeRecorder(10)
	reconciler := &CassandraClusterReconciler{
		Client: tClient,
		Scheme: baseScheme,
		Log:    zap.NewNop().Sugar(),
		Events: events.NewEventRecorder(recorder),
	}

	_, err := reconciler.reconcileCassandraScaling(context.Background(), cc, &v1.PodList{}, &v1.NodeList{}, cc.Spec.DCs, nil)
	asserts.Expect(err).To(Succeed())

	replicas := func(dc string) int32 {
		sts := &appsv1.StatefulSet{}
		asserts.Expect(tClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-cluster-cassandra-" + dc}, sts)).To(Succeed())
		return *sts.Spec.Replicas
	}
	asserts.Expect(replicas("dc1")).To(BeEquivalentTo(2), "dc1 must wait for its PVC to be deleted")
	asserts.Expect(replicas("dc2")).To(BeEquivalentTo(3), "dc2 must not be held back by dc1")
	asserts.Expect(recorder.Events).To(Receive(ContainSubstring(events.EventScaleUpWaitingForPVCs)))
}
