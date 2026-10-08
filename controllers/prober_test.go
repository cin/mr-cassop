package controllers

import (
	"context"
	"testing"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Regression test for #167: the Service selector must only contain labels the Prober Pods
// actually carry (labels.ComponentLabels), not the wider labels.CombinedComponentLabels that
// also inherits the CassandraCluster CR's own metadata.labels - otherwise a CR with any of its
// own labels set (e.g. added by GitOps tooling) produces a Service that matches zero Pods.
func TestReconcileProberServiceSelectorMatchesPodLabels(t *testing.T) {
	asserts := NewGomegaWithT(t)

	cc := &dbv1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "argocd",
			},
		},
	}

	reconciler := createBasicMockedReconciler()
	reconciler.Client = fake.NewClientBuilder().WithScheme(baseScheme).Build()

	asserts.Expect(reconciler.reconcileProberService(context.Background(), cc)).To(Succeed())

	svc := &v1.Service{}
	asserts.Expect(reconciler.Client.Get(context.Background(), types.NamespacedName{Name: names.ProberService(cc.Name), Namespace: cc.Namespace}, svc)).To(Succeed())

	proberPodLabels := labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentProber)
	asserts.Expect(svc.Spec.Selector).To(BeEquivalentTo(proberPodLabels),
		"Service selector must match exactly what the Prober Deployment's Pod template carries")

	// The Service's own metadata labels are informational and may still inherit the CR's labels.
	asserts.Expect(svc.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "argocd"))
}
