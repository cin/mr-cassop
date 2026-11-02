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

// Regression test for the TLS JMX hostname check: Jolokia connects to a node's JMX by pod IP, but the
// node certificate is shared by all nodes (CN=localhost, SAN DNS:localhost), so a JVM that checks the
// hostname on the RMI-over-SSL connection can never connect and the Prober marks every pod unready.
func TestJolokiaContainerTLSOptions(t *testing.T) {
	g := NewWithT(t)

	cc := &dbv1alpha1.CassandraCluster{}
	cc.Spec.Encryption.Client.NodeTLSSecret = dbv1alpha1.NodeTLSSecret{
		KeystoreFileKey:       "keystore.p12",
		KeystorePasswordKey:   "keystore.password",
		TruststoreFileKey:     "truststore.p12",
		TruststorePasswordKey: "truststore.password",
	}
	secret := &v1.Secret{Data: map[string][]byte{
		"keystore.password":   []byte("ks-pass\n"),
		"truststore.password": []byte("ts-pass"),
	}}

	t.Run("client TLS disabled", func(t *testing.T) {
		container := jolokiaContainer(cc, secret)
		g.Expect(container.Env).To(BeEmpty())
		g.Expect(container.VolumeMounts).To(BeEmpty())
	})

	t.Run("client TLS enabled", func(t *testing.T) {
		tlsCC := cc.DeepCopy()
		tlsCC.Spec.Encryption.Client.Enabled = true

		container := jolokiaContainer(tlsCC, secret)
		g.Expect(container.Env).To(HaveLen(1))
		g.Expect(container.Env[0].Name).To(Equal("CATALINA_OPTS"))
		opts := container.Env[0].Value
		g.Expect(opts).To(ContainSubstring("-Djavax.net.ssl.keyStore=" + cassandraClientTLSDir + "/keystore.p12"))
		g.Expect(opts).To(ContainSubstring("-Djavax.net.ssl.keyStorePassword=ks-pass "))
		g.Expect(opts).To(ContainSubstring("-Djavax.net.ssl.trustStorePassword=ts-pass"))
		g.Expect(opts).To(ContainSubstring("-Djdk.rmi.ssl.client.enableEndpointIdentification=false"))
	})
}

// The setting only belongs on JMX clients. The Cassandra container is the JMX server and shares
// tlsJVMArgs with the clients, so it must not carry the client-side option.
func TestTLSJVMArgsDoNotDisableEndpointIdentification(t *testing.T) {
	g := NewWithT(t)

	cc := &dbv1alpha1.CassandraCluster{}
	secret := &v1.Secret{}
	g.Expect(tlsJVMArgs(cc, secret)).ToNot(ContainSubstring("enableEndpointIdentification"))
	g.Expect(jmxClientTLSJVMArgs(cc, secret)).To(ContainSubstring("enableEndpointIdentification=false"))
}
