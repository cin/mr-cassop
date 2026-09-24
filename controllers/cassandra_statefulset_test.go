package controllers

import (
	"testing"

	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/config"
)

// TestCassandraStatefulSet_UpdateStrategyAndPVCRetentionPolicyMatchAPIServerDefaults pins the
// fix for the spurious-reconcile-diff bug (#162): the API server defaults
// PersistentVolumeClaimRetentionPolicy to Retain/Retain when it's left nil, so the desired spec
// must set that explicitly to match. UpdateStrategy.RollingUpdate is the opposite case - the API
// server only defaults its Partition/MaxUnavailable sub-fields when RollingUpdate itself is
// non-nil, and whether it also defaults MaxUnavailable depends on the MaxUnavailableStatefulSet
// feature gate's on/off state, which differs by k8s minor/patch version. Setting RollingUpdate to
// a non-nil struct (as earlier versions of this code did, e.g. to hardcode Partition or
// MaxUnavailable) re-creates exactly the version-dependent mismatch this test guards against;
// leaving it nil is the only value the API server never overwrites on any version. This is a unit
// test, not an envtest one, because the two fields need opposite treatment for the same reason -
// matching one API server version's defaulting doesn't guarantee matching another's - so pinning
// the exact values here is more reliable than asserting behavior against any single envtest
// k8s binary.
func TestCassandraStatefulSet_UpdateStrategyAndPVCRetentionPolicyMatchAPIServerDefaults(t *testing.T) {
	g := NewGomegaWithT(t)

	reconciler := &CassandraClusterReconciler{
		Cfg: config.Config{
			DefaultProberImage:    "prober/image",
			DefaultJolokiaImage:   "jolokia/image",
			DefaultCassandraImage: "cassandra/image",
			DefaultReaperImage:    "reaper/image",
		},
	}

	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: v1alpha1.CassandraClusterSpec{
			DCs: []v1alpha1.DC{
				{Name: "dc1", Replicas: proto.Int32(3)},
			},
		},
	}
	reconciler.defaultCassandraCluster(cc)

	sts := cassandraStatefulSet(cc, cc.Spec.DCs[0], checksumContainer{}, &v1.Secret{})

	g.Expect(sts.Spec.UpdateStrategy.Type).To(Equal(appsv1.RollingUpdateStatefulSetStrategyType))
	g.Expect(sts.Spec.UpdateStrategy.RollingUpdate).To(BeNil())

	g.Expect(sts.Spec.PersistentVolumeClaimRetentionPolicy).To(Equal(&appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
		WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
		WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
	}))
}
