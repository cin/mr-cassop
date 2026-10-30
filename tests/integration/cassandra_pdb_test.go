package integration

import (
	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

var _ = Describe("cassandra pod disruption budget", func() {
	newCluster := func(pdb v1alpha1.PodDisruptionBudget) *v1alpha1.CassandraCluster {
		return &v1alpha1.CassandraCluster{
			ObjectMeta: cassandraObjectMeta,
			Spec: v1alpha1.CassandraClusterSpec{
				DCs:                 []v1alpha1.DC{{Name: "dc1", Replicas: proto.Int32(3)}},
				ImagePullSecretName: "pullSecretName",
				AdminRoleSecretName: "admin-role",
				Cassandra:           &v1alpha1.Cassandra{PodDisruptionBudget: pdb},
			},
		}
	}
	pdbName := types.NamespacedName{Name: names.DC(cassandraObjectMeta.Name, "dc1"), Namespace: cassandraObjectMeta.Namespace}

	getPDB := func() (*policyv1.PodDisruptionBudget, error) {
		pdb := &policyv1.PodDisruptionBudget{}
		return pdb, k8sClient.Get(ctx, pdbName, pdb)
	}

	updateSpec := func(change func(*v1alpha1.PodDisruptionBudget)) {
		Eventually(func() error {
			current := &v1alpha1.CassandraCluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: cassandraObjectMeta.Name, Namespace: cassandraObjectMeta.Namespace}, current); err != nil {
				return err
			}
			change(&current.Spec.Cassandra.PodDisruptionBudget)
			return k8sClient.Update(ctx, current)
		}, mediumTimeout, mediumRetry).Should(Succeed())
	}

	It("creates a budget for the DC, follows maxUnavailable, and removes it when disabled", func() {
		cc := newCluster(v1alpha1.PodDisruptionBudget{Enabled: ptr.To(true)})
		createReadyCluster(cc)

		By("creating a budget that selects the DC's pods and is owned by the cluster")
		Eventually(func() error { _, err := getPDB(); return err }, mediumTimeout, mediumRetry).Should(Succeed())
		pdb, err := getPDB()
		Expect(err).NotTo(HaveOccurred())
		Expect(pdb.Spec.MaxUnavailable).To(HaveValue(Equal(intstr.FromInt32(1))))
		Expect(pdb.Spec.Selector.MatchLabels).To(HaveKeyWithValue(v1alpha1.CassandraClusterDC, "dc1"))
		Expect(pdb.Spec.Selector.MatchLabels).To(HaveKeyWithValue(v1alpha1.CassandraClusterInstance, cassandraObjectMeta.Name))
		Expect(metav1.GetControllerOf(pdb)).NotTo(BeNil())
		Expect(metav1.GetControllerOf(pdb).Name).To(Equal(cassandraObjectMeta.Name))

		By("updating it when maxUnavailable changes")
		updateSpec(func(p *v1alpha1.PodDisruptionBudget) { p.MaxUnavailable = ptr.To[int32](2) })
		Eventually(func() *intstr.IntOrString {
			current, err := getPDB()
			Expect(err).NotTo(HaveOccurred())
			return current.Spec.MaxUnavailable
		}, mediumTimeout, mediumRetry).Should(HaveValue(Equal(intstr.FromInt32(2))))

		By("deleting it when disabled")
		updateSpec(func(p *v1alpha1.PodDisruptionBudget) { p.Enabled = ptr.To(false) })
		Eventually(func() bool {
			_, err := getPDB()
			return err != nil
		}, mediumTimeout, mediumRetry).Should(BeTrue())
	})

	It("doesn't create a budget when the operator default is off and the cluster doesn't ask for one", func() {
		cc := newCluster(v1alpha1.PodDisruptionBudget{})
		createReadyCluster(cc)

		Consistently(func() bool {
			_, err := getPDB()
			return err != nil
		}, shortTimeout, shortRetry).Should(BeTrue())
	})
})
