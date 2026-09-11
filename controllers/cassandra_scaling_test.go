package controllers

import (
	"context"
	"testing"

	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/jobs"
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
