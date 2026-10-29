package controllers

import (
	"context"
	"testing"

	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/config"
	"github.com/cin/mr-cassop/controllers/names"
)

func pdbTestCluster(name string, dcs ...string) *v1alpha1.CassandraCluster {
	cc := &v1alpha1.CassandraCluster{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("uid-" + name)}}
	for _, dc := range dcs {
		cc.Spec.DCs = append(cc.Spec.DCs, v1alpha1.DC{Name: dc, Replicas: proto.Int32(3)})
	}
	cc.Spec.Cassandra = &v1alpha1.Cassandra{
		PodDisruptionBudget: v1alpha1.PodDisruptionBudget{Enabled: ptr.To(true), MaxUnavailable: ptr.To[int32](1)},
	}
	return cc
}

func pdbTestReconciler(updates *int, objs ...client.Object) *CassandraClusterReconciler {
	return &CassandraClusterReconciler{
		Log:    zap.NewNop().Sugar(),
		Scheme: baseScheme,
		Client: fake.NewClientBuilder().WithScheme(baseScheme).WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				*updates++
				return c.Update(ctx, obj, opts...)
			},
		}).Build(),
	}
}

func listPDBs(g *WithT, r *CassandraClusterReconciler) map[string]policyv1.PodDisruptionBudget {
	list := &policyv1.PodDisruptionBudgetList{}
	g.Expect(r.List(context.Background(), list)).To(Succeed())
	found := map[string]policyv1.PodDisruptionBudget{}
	for _, pdb := range list.Items {
		found[pdb.Name] = pdb
	}
	return found
}

// A budget that selects no pods protects nothing, so its selector has to match the labels the Cassandra
// StatefulSet puts on the pods of exactly that DC.
func TestDesiredDCPodDisruptionBudgetSelectsOnlyThePodsOfItsDC(t *testing.T) {
	g := NewWithT(t)
	cc := pdbTestCluster("test-cluster", "dc1", "dc2")
	r := &CassandraClusterReconciler{Cfg: config.Config{DefaultProberImage: "prober/image", DefaultJolokiaImage: "jolokia/image", DefaultCassandraImage: "cassandra/image", DefaultReaperImage: "reaper/image"}}
	r.defaultCassandraCluster(cc)

	pdb := desiredDCPodDisruptionBudget(cc, cc.Spec.DCs[0])
	g.Expect(pdb.Name).To(Equal(names.DC("test-cluster", "dc1")))
	g.Expect(pdb.Spec.MaxUnavailable).To(HaveValue(Equal(intstr.FromInt32(1))))

	selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
	g.Expect(err).NotTo(HaveOccurred())
	dc1Pod := cassandraStatefulSet(cc, cc.Spec.DCs[0], checksumContainer{}, nil).Spec.Template.Labels
	dc2Pod := cassandraStatefulSet(cc, cc.Spec.DCs[1], checksumContainer{}, nil).Spec.Template.Labels
	g.Expect(selector.Matches(labels.Set(dc1Pod))).To(BeTrue(), "the budget must select its own DC's pods")
	g.Expect(selector.Matches(labels.Set(dc2Pod))).To(BeFalse(), "the budget must not select another DC's pods")
}

func TestDefaultPodDisruptionBudget(t *testing.T) {
	tests := []struct {
		name            string
		operatorDefault bool
		spec            v1alpha1.PodDisruptionBudget
		wantEnabled     bool
		wantMax         int32
	}{
		{"unset follows an operator default of true", true, v1alpha1.PodDisruptionBudget{}, true, 1},
		{"unset follows an operator default of false", false, v1alpha1.PodDisruptionBudget{}, false, 1},
		{"the cluster can turn it off", true, v1alpha1.PodDisruptionBudget{Enabled: ptr.To(false)}, false, 1},
		{"the cluster can turn it on", false, v1alpha1.PodDisruptionBudget{Enabled: ptr.To(true)}, true, 1},
		{"maxUnavailable is kept", true, v1alpha1.PodDisruptionBudget{MaxUnavailable: ptr.To[int32](2)}, true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &CassandraClusterReconciler{Cfg: config.Config{DefaultPodDisruptionBudgetEnabled: tt.operatorDefault}}
			cc := &v1alpha1.CassandraCluster{Spec: v1alpha1.CassandraClusterSpec{Cassandra: &v1alpha1.Cassandra{PodDisruptionBudget: tt.spec}}}

			r.defaultPodDisruptionBudget(cc)

			g.Expect(cc.Spec.Cassandra.PodDisruptionBudget.Enabled).To(HaveValue(Equal(tt.wantEnabled)))
			g.Expect(cc.Spec.Cassandra.PodDisruptionBudget.MaxUnavailable).To(HaveValue(Equal(tt.wantMax)))
		})
	}
}

func TestReconcilePodDisruptionBudgetsCreatesOnePerDCAndIsIdempotent(t *testing.T) {
	g := NewWithT(t)
	updates := 0
	r := pdbTestReconciler(&updates)
	cc := pdbTestCluster("test-cluster", "dc1", "dc2")

	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())

	found := listPDBs(g, r)
	g.Expect(found).To(HaveLen(2))
	for _, dc := range []string{"dc1", "dc2"} {
		pdb := found[names.DC("test-cluster", dc)]
		g.Expect(pdb.OwnerReferences).To(HaveLen(1), "owned by the cluster, so it's garbage collected with it")
		g.Expect(pdb.OwnerReferences[0].Name).To(Equal("test-cluster"))
	}

	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())
	g.Expect(updates).To(BeZero(), "a second pass over an unchanged cluster must not update anything")
}

func TestReconcilePodDisruptionBudgetsFollowsMaxUnavailable(t *testing.T) {
	g := NewWithT(t)
	updates := 0
	r := pdbTestReconciler(&updates)
	cc := pdbTestCluster("test-cluster", "dc1")
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())

	cc.Spec.Cassandra.PodDisruptionBudget.MaxUnavailable = ptr.To[int32](2)
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())

	g.Expect(updates).To(Equal(1))
	pdb := listPDBs(g, r)[names.DC("test-cluster", "dc1")]
	g.Expect(pdb.Spec.MaxUnavailable).To(HaveValue(Equal(intstr.FromInt32(2))))
}

func TestReconcilePodDisruptionBudgetsDeletesWhenDisabled(t *testing.T) {
	g := NewWithT(t)
	updates := 0
	r := pdbTestReconciler(&updates)
	cc := pdbTestCluster("test-cluster", "dc1", "dc2")
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())
	g.Expect(listPDBs(g, r)).To(HaveLen(2))

	cc.Spec.Cassandra.PodDisruptionBudget.Enabled = ptr.To(false)
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())

	g.Expect(listPDBs(g, r)).To(BeEmpty())
}

func TestReconcilePodDisruptionBudgetsDeletesTheBudgetOfARemovedDC(t *testing.T) {
	g := NewWithT(t)
	updates := 0
	r := pdbTestReconciler(&updates)
	cc := pdbTestCluster("test-cluster", "dc1", "dc2")
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())

	cc.Spec.DCs = cc.Spec.DCs[:1]
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())

	g.Expect(listPDBs(g, r)).To(HaveKey(names.DC("test-cluster", "dc1")))
	g.Expect(listPDBs(g, r)).NotTo(HaveKey(names.DC("test-cluster", "dc2")))
}

// Other clusters in the namespace, and budgets the operator didn't create, must survive a cleanup.
func TestReconcilePodDisruptionBudgetsLeavesOtherBudgetsAlone(t *testing.T) {
	g := NewWithT(t)
	updates := 0
	r := pdbTestReconciler(&updates)
	other := pdbTestCluster("other-cluster", "dc1")
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), other)).To(Succeed())
	userPDB := &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: "user-pdb", Namespace: "default"}}
	g.Expect(r.Create(context.Background(), userPDB)).To(Succeed())

	cc := pdbTestCluster("test-cluster", "dc1")
	cc.Spec.Cassandra.PodDisruptionBudget.Enabled = ptr.To(false)
	g.Expect(r.reconcilePodDisruptionBudgets(context.Background(), cc)).To(Succeed())

	g.Expect(listPDBs(g, r)).To(And(HaveKey(names.DC("other-cluster", "dc1")), HaveKey("user-pdb")))
}
