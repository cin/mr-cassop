package integration

import (
	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
)

// This exercises the ParallelPodManagement -> OrderedReady migration fix (issue #155): a DC's
// statefulset starts out with ParallelPodManagement (fast concurrent pod creation during initial
// bootstrap), but Kubernetes never caps how many of its pods can be unavailable at once under
// that policy - not even during a routine update to an already-running, already-quorate cluster.
// A single field change (e.g. an image bump) can therefore recreate every Cassandra pod at once,
// which can drop `system_auth` below LOCAL_QUORUM on every node simultaneously and deadlock the
// DC (the same failure class as #150, just triggered by a routine rolling update instead of a
// PVC-reuse recreate). The fix: once a DC first reports fully ready, the operator flips its
// statefulset to OrderedReady - which Kubernetes does cap to one pod at a time - via a delete
// (with an orphan cascade, so the running pods and PVCs are untouched) and recreate, since
// podManagementPolicy is immutable on an existing statefulset.
var _ = Describe("statefulset pod management policy migration", func() {
	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: cassandraObjectMeta,
		Spec: v1alpha1.CassandraClusterSpec{
			DCs: []v1alpha1.DC{
				{
					Name:     "dc1",
					Replicas: proto.Int32(3),
				},
			},
			ImagePullSecretName: "pullSecretName",
			AdminRoleSecretName: "admin-role",
		},
	}

	getSts := func() *appsv1.StatefulSet {
		sts := &appsv1.StatefulSet{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: names.DC(cc.Name, "dc1"), Namespace: cc.Namespace,
		}, sts)).To(Succeed())
		return sts
	}

	It("stays Parallel during initial bootstrap, then migrates to OrderedReady once ready", func() {
		createAdminSecret(cc)
		Expect(k8sClient.Create(ctx, cc)).To(Succeed())
		markMocksAsReady(cc)
		waitForDCsToBeCreated(cc)

		By("using ParallelPodManagement for the fast initial multi-node bootstrap")
		Expect(getSts().Spec.PodManagementPolicy).To(Equal(appsv1.ParallelPodManagement))
		bootstrapUID := getSts().UID

		By("migrating to OrderedReady once the DC first reports fully ready")
		markAllDCsReady(cc)
		Eventually(func() appsv1.PodManagementPolicyType {
			return getSts().Spec.PodManagementPolicy
		}, mediumTimeout, mediumRetry).Should(Equal(appsv1.OrderedReadyPodManagement))

		By("recreating the statefulset object rather than mutating the immutable field in place")
		Expect(getSts().UID).NotTo(Equal(bootstrapUID))

		By("never migrating again - a second ready signal is a no-op")
		migratedUID := getSts().UID
		markAllDCsReady(cc) // re-populate ReadyReplicas on the recreated object and reconcile again
		Consistently(func() types.UID {
			return getSts().UID
		}, shortTimeout, shortRetry).Should(Equal(migratedUID))

		By("still applying later spec changes normally, one field at a time, without erroring on the now-immutable policy")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cc.Namespace, Name: cc.Name}, cc)).To(Succeed())
		cc.Spec.Cassandra = &v1alpha1.Cassandra{JVMOptions: []string{"-Xmx2G"}}
		Expect(k8sClient.Update(ctx, cc)).To(Succeed())

		podRestartChecksum := func() string {
			for _, env := range getSts().Spec.Template.Spec.Containers[0].Env {
				if env.Name == "POD_RESTART_CHECKSUM" {
					return env.Value
				}
			}
			return ""
		}
		originalChecksum := podRestartChecksum()
		Eventually(podRestartChecksum, mediumTimeout, mediumRetry).ShouldNot(Equal(originalChecksum))
		Expect(getSts().Spec.PodManagementPolicy).To(Equal(appsv1.OrderedReadyPodManagement))
	})
})
