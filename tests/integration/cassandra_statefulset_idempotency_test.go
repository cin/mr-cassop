package integration

import (
	"strconv"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
)

// This guards against a regression of the spurious-reconcile-diff bug fixed in #162: leaving
// StatefulSetSpec.UpdateStrategy.RollingUpdate or PersistentVolumeClaimRetentionPolicy set to
// something the API server's defaulting doesn't leave untouched makes compare.EqualStatefulSet
// see a diff that isn't really there, so reconcileDCStatefulSet issues a real Update on every
// single reconcile - the reconcile-loop-thrashing REVIEW.md's idempotency rule exists to
// prevent. The bug only shows up against a real API server's defaulting, so this needs the
// envtest control plane rather than a unit test. metadata.generation only advances when a write
// actually changes the persisted spec, so a stable generation across several forced reconciles
// means no spurious Update happened; a no-op annotation edit is what forces each of those
// reconciles, since re-reporting identical StatefulSet status (as a real cluster would once
// steady) doesn't - the API server itself skips persisting a status update with no content
// change, so it wouldn't re-enqueue the owning cluster at all.
var _ = Describe("cassandra statefulset reconcile idempotency", func() {
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

	nudgeReconcile := func(value int) {
		Eventually(func() error {
			current := &v1alpha1.CassandraCluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: cc.Name, Namespace: cc.Namespace}, current); err != nil {
				return err
			}
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations["test.mr-cassop.io/nudge"] = strconv.Itoa(value)
			return k8sClient.Update(ctx, current)
		}, mediumTimeout, mediumRetry).Should(Succeed())
	}

	It("does not update the statefulset on repeated reconciles of an already-reconciled cluster", func() {
		createReadyCluster(cc)

		By("letting the pod management policy migration (its own recreate) settle first")
		Eventually(func() appsv1.PodManagementPolicyType {
			return getSts().Spec.PodManagementPolicy
		}, mediumTimeout, mediumRetry).Should(Equal(appsv1.OrderedReadyPodManagement))

		baselineGeneration := getSts().Generation

		By("forcing several more reconciles via unrelated cluster edits")
		for i := range 3 {
			nudgeReconcile(i)
		}

		By("never re-issuing a spec update, since the desired and actual specs already match")
		Consistently(func() int64 {
			return getSts().Generation
		}, mediumTimeout, shortRetry).Should(Equal(baselineGeneration))
	})
})
