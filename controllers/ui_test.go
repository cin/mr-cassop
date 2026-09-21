package controllers

import (
	"testing"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
