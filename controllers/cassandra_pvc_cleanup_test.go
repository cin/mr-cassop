package controllers

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/labels"
)

var pvcTestCluster = &v1alpha1.CassandraCluster{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"}}

func testPVC(name string, annotations map[string]string) *v1.PersistentVolumeClaim {
	return &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:        name,
		Namespace:   "default",
		Labels:      labels.Cassandra(pvcTestCluster),
		Annotations: annotations,
	}}
}

func pvcTestReconciler(objs ...client.Object) *CassandraClusterReconciler {
	return &CassandraClusterReconciler{
		Client: fake.NewClientBuilder().WithScheme(baseScheme).WithObjects(objs...).Build(),
		Log:    zap.NewNop().Sugar(),
	}
}

func pvcExists(g *WithT, r *CassandraClusterReconciler, name string) bool {
	err := r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, &v1.PersistentVolumeClaim{})
	if apierrors.IsNotFound(err) {
		return false
	}
	g.Expect(err).To(Succeed())
	return true
}

func TestMarkPodPVCsDecommissioned(t *testing.T) {
	g := NewGomegaWithT(t)
	pod := "test-cluster-cassandra-dc1-3"
	r := pvcTestReconciler(testPVC("data-"+pod, nil), testPVC("data-test-cluster-cassandra-dc1-2", nil))
	sts := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{VolumeClaimTemplates: []v1.PersistentVolumeClaim{
			{ObjectMeta: metav1.ObjectMeta{Name: "data"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "commitlog"}}, // no PVC for it: skipped, not an error
		}},
	}

	g.Expect(r.markPodPVCsDecommissioned(context.Background(), sts, pod)).To(Succeed())

	marked := &v1.PersistentVolumeClaim{}
	g.Expect(r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "data-" + pod}, marked)).To(Succeed())
	g.Expect(marked.Annotations).To(HaveKeyWithValue(decommissionedPVCAnnotation, pod))

	other := &v1.PersistentVolumeClaim{}
	g.Expect(r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "data-test-cluster-cassandra-dc1-2"}, other)).To(Succeed())
	g.Expect(other.Annotations).NotTo(HaveKey(decommissionedPVCAnnotation))
}

func TestDeleteDecommissionedPVCs(t *testing.T) {
	g := NewGomegaWithT(t)
	gone := "test-cluster-cassandra-dc1-3"
	stillRunning := "test-cluster-cassandra-dc1-4"
	r := pvcTestReconciler(
		testPVC("data-"+gone, map[string]string{decommissionedPVCAnnotation: gone}),
		testPVC("data-"+stillRunning, map[string]string{decommissionedPVCAnnotation: stillRunning}),
		// no pod and beyond the replica count, but never confirmed decommissioned (e.g. a
		// manual `kubectl scale`): must be kept
		testPVC("data-test-cluster-cassandra-dc1-5", nil),
	)
	pods := []v1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: stillRunning, Namespace: "default"}}}

	pending, err := r.deleteDecommissionedPVCs(context.Background(), pvcTestCluster, pods)
	g.Expect(err).To(Succeed())

	g.Expect(pvcExists(g, r, "data-"+gone)).To(BeFalse())
	g.Expect(pvcExists(g, r, "data-"+stillRunning)).To(BeTrue(), "pod still exists")
	g.Expect(pvcExists(g, r, "data-test-cluster-cassandra-dc1-5")).To(BeTrue(), "never marked")
	g.Expect(pending).To(Equal(map[string]bool{gone: true, stillRunning: true}))
}

func TestScaleUpBlockedByPVC(t *testing.T) {
	g := NewGomegaWithT(t)
	pending := map[string]bool{"test-cluster-cassandra-dc1-3": true}

	pod, blocked := scaleUpBlockedByPVC("test-cluster-cassandra-dc1", 3, 5, pending)
	g.Expect(blocked).To(BeTrue())
	g.Expect(pod).To(Equal("test-cluster-cassandra-dc1-3"))

	_, blocked = scaleUpBlockedByPVC("test-cluster-cassandra-dc1", 4, 5, pending)
	g.Expect(blocked).To(BeFalse(), "ordinal 3 isn't being created")
}
