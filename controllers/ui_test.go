package controllers

import (
	"context"
	"testing"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/compare"
	"github.com/cin/mr-cassop/controllers/names"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// The UI must mount the same secret prober validates Basic Auth against, as a volume (not
// secretKeyRef env vars) so an admin password rotation reaches it without a pod restart.
func TestUIMountsProberAuthSecret(t *testing.T) {
	g := NewGomegaWithT(t)

	for jmxAuth, wantSecret := range map[string]string{
		jmxAuthenticationInternal:   names.ActiveAdminSecret("test-cluster"),
		jmxAuthenticationLocalFiles: "user-admin-secret",
	} {
		cc := &dbv1alpha1.CassandraCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
			Spec:       dbv1alpha1.CassandraClusterSpec{JMXAuth: jmxAuth, AdminRoleSecretName: "user-admin-secret"},
		}

		volume := uiProberCredentialsVolume(cc)
		g.Expect(volume.Secret).NotTo(BeNil())
		g.Expect(volume.Secret.SecretName).To(Equal(wantSecret), "jmxAuth=%s", jmxAuth)

		container := uiContainer(cc)
		g.Expect(container.VolumeMounts).To(ContainElement(HaveField("Name", volume.Name)))
		for _, env := range container.Env {
			g.Expect(env.ValueFrom).To(BeNil(), "env %s must not be sourced from the secret", env.Name)
		}
	}
}

// A DC literally named "ui" must not share a Service name with the UI, or reconcileDCService and
// reconcileUIService would fight over the same object.
func TestUINameDoesNotCollideWithDCService(t *testing.T) {
	g := NewGomegaWithT(t)
	g.Expect(names.UI("test-cluster")).NotTo(Equal(names.DCService("test-cluster", "ui")))
}

func TestUIDeploymentIsHardened(t *testing.T) {
	g := NewGomegaWithT(t)
	cc := &dbv1alpha1.CassandraCluster{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"}}

	podSpec := desiredUIDeployment(cc, "").Spec.Template.Spec
	g.Expect(podSpec.SecurityContext.RunAsNonRoot).To(HaveValue(BeTrue()))
	g.Expect(podSpec.SecurityContext.SeccompProfile.Type).To(Equal(v1.SeccompProfileTypeRuntimeDefault))

	sc := podSpec.Containers[0].SecurityContext
	g.Expect(sc.AllowPrivilegeEscalation).To(HaveValue(BeFalse()))
	g.Expect(sc.ReadOnlyRootFilesystem).To(HaveValue(BeTrue()))
	g.Expect(sc.Capabilities.Drop).To(ConsistOf(v1.Capability("ALL")))
}

// https://github.com/cin/mr-cassop/issues/214: the operator replaces its immutable active admin secret,
// and kubelet never refreshes a volume backed by a replaced secret. A new secret UID must change the pod
// template so the Deployment rolls; the same UID must not, or every reconcile would be a spurious diff.
func TestUIDeploymentRollsWhenCredentialsSecretIsReplaced(t *testing.T) {
	g := NewGomegaWithT(t)
	cc := &dbv1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec:       dbv1alpha1.CassandraClusterSpec{JMXAuth: jmxAuthenticationInternal},
	}

	before := desiredUIDeployment(cc, "uid-1")
	g.Expect(compare.EqualDeployment(before, desiredUIDeployment(cc, "uid-1"))).To(BeTrue())
	g.Expect(compare.EqualDeployment(before, desiredUIDeployment(cc, "uid-2"))).To(BeFalse())
	g.Expect(before.Spec.Template.Annotations).To(HaveKeyWithValue(uiCredentialsSecretAnnotation, "uid-1"))
}

func TestUICredentialsSecretUID(t *testing.T) {
	g := NewGomegaWithT(t)
	cc := &dbv1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec:       dbv1alpha1.CassandraClusterSpec{JMXAuth: jmxAuthenticationInternal},
	}
	secret := &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: names.ActiveAdminSecret(cc.Name), Namespace: "default", UID: "uid-1"}}
	r := &CassandraClusterReconciler{Client: fake.NewClientBuilder().WithScheme(baseScheme).Build()}

	uid, err := r.uiCredentialsSecretUID(context.Background(), cc)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(uid).To(BeEmpty(), "a missing secret isn't an error; the pod can't start until it exists")

	g.Expect(r.Create(context.Background(), secret)).To(Succeed())
	uid, err = r.uiCredentialsSecretUID(context.Background(), cc)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(uid).NotTo(BeEmpty())
}
