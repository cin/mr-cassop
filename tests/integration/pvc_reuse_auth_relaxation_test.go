package integration

import (
	"fmt"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

// This exercises the PVC-reuse bootstrap deadlock fix (issue #150): when the operator
// detects it's bootstrapping against Cassandra data PVCs that already exist, it must
// temporarily relax auth_read_consistency_level to LOCAL_ONE so a lone starting node isn't
// stuck waiting on a quorum that can never form until its siblings (which are themselves
// waiting on it) come up - and it must stop doing so, permanently, once the cluster is ready.
var _ = Describe("temporary auth relaxation for clusters recreated from existing PVCs", func() {
	newCluster := func(configOverrides string) *v1alpha1.CassandraCluster {
		return &v1alpha1.CassandraCluster{
			ObjectMeta: cassandraObjectMeta,
			Spec: v1alpha1.CassandraClusterSpec{
				DCs: []v1alpha1.DC{
					{
						Name:     "dc1",
						Replicas: proto.Int32(3),
					},
				},
				ImagePullSecretName: "pull-secret-name",
				AdminRoleSecretName: "admin-role",
				Cassandra: &v1alpha1.Cassandra{
					Persistence:     v1alpha1.Persistence{Enabled: true},
					ConfigOverrides: configOverrides,
				},
			},
		}
	}

	// createExistingPVCs seeds one bound-looking data PVC per replica with the same labels
	// createClusterAdminSecrets queries by, simulating storage left over from a previous
	// cluster instance. Cleaned up per-spec since the shared "default" namespace/cluster name
	// used across this whole suite is not otherwise wiped between tests.
	createExistingPVCs := func(cc *v1alpha1.CassandraCluster) {
		pvcLabels := labels.ComponentLabels(cc, v1alpha1.CassandraClusterComponentCassandra)
		for i := 0; i < int(*cc.Spec.DCs[0].Replicas); i++ {
			pvc := &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("data-%s-%d", names.DC(cc.Name, cc.Spec.DCs[0].Name), i),
					Namespace: cc.Namespace,
					Labels:    pvcLabels,
				},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
					Resources: v1.VolumeResourceRequirements{
						Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
			DeferCleanup(func() {
				deleteAndUnblockPVC(pvc.Name, pvc.Namespace)
			})
		}
	}

	// renderedAuthReadConsistencyLevel returns the auth_read_consistency_level currently
	// baked into the rendered cassandra.yaml ConfigMap, or "" if the key isn't set at all.
	renderedAuthReadConsistencyLevel := func(cc *v1alpha1.CassandraCluster) string {
		cm := &v1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: names.ConfigMap(cc.Name), Namespace: cc.Namespace}, cm)).To(Succeed())

		cassandraYaml := make(map[string]interface{})
		Expect(yaml.Unmarshal([]byte(cm.Data["cassandra.yaml"]), &cassandraYaml)).To(Succeed())

		value, ok := cassandraYaml["auth_read_consistency_level"]
		if !ok {
			return ""
		}
		return fmt.Sprintf("%v", value)
	}

	recreatedFromExistingPVCs := func(cc *v1alpha1.CassandraCluster) bool {
		actual := &v1alpha1.CassandraCluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cc.Name, Namespace: cc.Namespace}, actual)).To(Succeed())
		return actual.Status.RecreatedFromExistingPVCs
	}

	It("injects LOCAL_ONE while not ready, then reverts once all DCs are ready", func() {
		cc := newCluster("")
		createAdminSecret(cc)
		createExistingPVCs(cc)
		Expect(k8sClient.Create(ctx, cc)).To(Succeed())
		markMocksAsReady(cc)
		waitForDCsToBeCreated(cc)

		By("detecting the pre-existing PVCs and recording it on the CR status")
		Eventually(func() bool {
			return recreatedFromExistingPVCs(cc)
		}, mediumTimeout, mediumRetry).Should(BeTrue())

		By("temporarily relaxing auth_read_consistency_level while DCs aren't ready yet")
		Eventually(func() string {
			return renderedAuthReadConsistencyLevel(cc)
		}, mediumTimeout, mediumRetry).Should(Equal("LOCAL_ONE"))

		By("marking every DC ready, as if the relaxed setting let the cluster bootstrap")
		markAllDCsReady(cc)

		By("no longer injecting the override once the cluster is ready")
		Eventually(func() string {
			return renderedAuthReadConsistencyLevel(cc)
		}, mediumTimeout, mediumRetry).Should(Equal(""))

		By("clearing the marker so the relaxation can never resurface later")
		Eventually(func() bool {
			return recreatedFromExistingPVCs(cc)
		}, mediumTimeout, mediumRetry).Should(BeFalse())
	})

	It("never masks the user's own auth_read_consistency_level override", func() {
		cc := newCluster("auth_read_consistency_level: QUORUM\n")
		createAdminSecret(cc)
		createExistingPVCs(cc)
		Expect(k8sClient.Create(ctx, cc)).To(Succeed())
		markMocksAsReady(cc)
		waitForDCsToBeCreated(cc)

		Eventually(func() bool {
			return recreatedFromExistingPVCs(cc)
		}, mediumTimeout, mediumRetry).Should(BeTrue())

		By("keeping the user's explicit setting instead of the temporary relaxation")
		Consistently(func() string {
			return renderedAuthReadConsistencyLevel(cc)
		}, shortTimeout, shortRetry).Should(Equal("QUORUM"))
	})
})

// deleteAndUnblockPVC deletes a PVC and strips any finalizers left on it. envtest runs a real
// API server (which admits the standard "kubernetes.io/pvc-protection" finalizer on create) but
// no controller-manager, so nothing ever removes that finalizer on delete; without this, the PVC
// would be stuck Terminating forever and collide with the next spec's fixed-name PVCs.
func deleteAndUnblockPVC(name, namespace string) {
	key := types.NamespacedName{Name: name, Namespace: namespace}
	_ = k8sClient.Delete(ctx, &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})

	Eventually(func() error {
		pvc := &v1.PersistentVolumeClaim{}
		err := k8sClient.Get(ctx, key, pvc)
		if kerrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(pvc.Finalizers) == 0 {
			return fmt.Errorf("pvc %s still present without finalizers", name)
		}
		pvc.Finalizers = nil
		return k8sClient.Update(ctx, pvc)
	}, mediumTimeout, mediumRetry).Should(Succeed())

	Eventually(func() bool {
		return kerrors.IsNotFound(k8sClient.Get(ctx, key, &v1.PersistentVolumeClaim{}))
	}, mediumTimeout, mediumRetry).Should(BeTrue())
}
