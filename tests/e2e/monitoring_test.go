package e2e

import (
	"fmt"
	"net/http"
	"time"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/expfmt"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

var _ = Describe("Cassandra cluster", func() {
	Context("when C* monitoring is enabled with tlp exporter", func() {
		ccName := "monitoring-tlp"
		AfterEach(func() {
			cleanupResources(ccName, cfg.operatorNamespace)
		})
		It("should be able to get C* metrics on tlp port", func() {
			cc := newCassandraClusterTmpl(ccName, cfg.operatorNamespace)
			cc.Spec.Cassandra.Monitoring.Enabled = true
			deployCassandraCluster(cc)

			By("check tlp exporter port")
			pf := portForwardPod(cc.Namespace, labels.Cassandra(cc), []string{fmt.Sprintf("%d:%d", dbv1alpha1.TlpPort, dbv1alpha1.TlpPort)})
			var resp *http.Response
			Eventually(func() (int, error) {
				var err error
				resp, err = http.Get(fmt.Sprintf("http://localhost:%d", dbv1alpha1.TlpPort))
				if err != nil {
					return 0, err
				}
				return resp.StatusCode, nil
			}, time.Second*20, time.Second*2).Should(Equal(200))

			By("read metrics from C* pod metrics exporter")
			var parser expfmt.TextParser
			metricFamilies, err := parser.TextToMetricFamilies(resp.Body)
			Expect(err).ToNot(HaveOccurred())

			By("ClientRequest TotalLatency Read metric")
			metric, found := getMetricByLabel(metricFamilies, "org_apache_cassandra_metrics_ClientRequest_TotalLatency", "scope", "Read")
			Expect(found).To(BeTrue())
			Expect(*metric.Untyped.Value).To(BeNumerically(">=", 0))

			By("ClientRequest TotalLatency Write metric")
			metric, found = getMetricByLabel(metricFamilies, "org_apache_cassandra_metrics_ClientRequest_TotalLatency", "scope", "Write")
			Expect(found).To(BeTrue())
			Expect(*metric.Untyped.Value).To(BeNumerically(">=", 0))

			By("Client connectedNativeClients metric")
			metric, found = getMetric(metricFamilies, "org_apache_cassandra_metrics_Client_connectedNativeClients")
			Expect(found).To(BeTrue())
			Expect(*metric.Untyped.Value).To(BeNumerically(">=", 0))

			By("HeapMemoryUsage committed metric")
			metric, found = getMetric(metricFamilies, "java_lang_Memory_HeapMemoryUsage_committed")
			Expect(found).To(BeTrue())
			Expect(*metric.Untyped.Value).To(BeNumerically(">=", 1))

			By("Runtime StartTime metric")
			metric, found = getMetric(metricFamilies, "java_lang_Runtime_StartTime")
			Expect(found).To(BeTrue())
			Expect(*metric.Untyped.Value).To(BeNumerically(">=", 1))

			pf.Close()

			By("check reaper admin port")
			pf = portForwardPod(cc.Namespace, labels.Reaper(cc), []string{fmt.Sprintf("%d:%d", dbv1alpha1.ReaperAdminPort, dbv1alpha1.ReaperAdminPort)})
			defer pf.Close()
			resp = &http.Response{}
			Eventually(func() (bool, error) {
				var err error
				resp, err = http.Get(fmt.Sprintf("http://localhost:%d/prometheusMetrics", dbv1alpha1.ReaperAdminPort))
				if err != nil {
					return false, err
				}

				By("read metrics from reaper pod metrics exporter")
				parser = expfmt.TextParser{}
				metricFamilies, err = parser.TextToMetricFamilies(resp.Body)
				if err != nil {
					return false, err
				}

				// Get the first metrics with retries to not fail if the metrics returned no error but not yet appeared
				By("SegmentRunner Renew Lead metric")
				metric, found = getMetric(metricFamilies, "io_cassandrareaper_service_SegmentRunner_renewLead")
				if !found {
					return false, nil
				}

				return true, nil
			}, time.Second*20, time.Second*2).Should(BeTrue(), "should have returned non empty metrics")

			By("SegmentRunner Renew Lead metric")
			metric, found = getMetric(metricFamilies, "io_cassandrareaper_service_SegmentRunner_renewLead")
			Expect(found).To(BeTrue())
			Expect(*metric.Summary.SampleCount).To(BeNumerically(">=", 0))

			By("SegmentRunner Take Lead metric")
			metric, found = getMetric(metricFamilies, "io_cassandrareaper_service_SegmentRunner_takeLead")
			Expect(found).To(BeTrue())
			Expect(*metric.Summary.SampleCount).To(BeNumerically(">=", 0))

			By("Jmx Connections metric")
			metric, found = getMetric(metricFamilies, "io_cassandrareaper_jmx_JmxConnectionFactory_jmxConnectionsIntializer")
			Expect(found).To(BeTrue())
			Expect(*metric.Summary.SampleCount).To(BeNumerically(">=", 1))

			By("Memory Heap Committed metric")
			metric, found = getMetric(metricFamilies, "jvm_memory_heap_committed")
			Expect(found).To(BeTrue())
			Expect(*metric.Gauge.Value).To(BeNumerically(">=", 1))
		})
	})
})
