package controllers

import (
	"testing"

	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPrunePodIPsOfRemovedPods(t *testing.T) {
	asserts := NewGomegaWithT(t)

	stsReplicas := map[string]int32{
		"test-cluster-cassandra-dc1":   2,
		"test-cluster-cassandra-dc1-b": 3, // name has "dc1" as a prefix
	}
	data := map[string]string{
		"test-cluster-cassandra-dc1-0":   "10.0.0.1", // exists
		"test-cluster-cassandra-dc1-1":   "10.0.0.2", // temporarily gone, within replicas
		"test-cluster-cassandra-dc1-2":   "10.0.0.3", // exists, being decommissioned
		"test-cluster-cassandra-dc1-3":   "10.0.0.4", // scaled away
		"test-cluster-cassandra-dc1-b-2": "10.0.1.3", // temporarily gone, within dc1-b's replicas
		"test-cluster-cassandra-dc2-0":   "10.0.2.1", // StatefulSet removed
	}
	pods := []v1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1-0"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1-2"}},
	}

	prunePodIPsOfRemovedPods(data, pods, stsReplicas)

	asserts.Expect(data).To(Equal(map[string]string{
		"test-cluster-cassandra-dc1-0":   "10.0.0.1",
		"test-cluster-cassandra-dc1-1":   "10.0.0.2",
		"test-cluster-cassandra-dc1-2":   "10.0.0.3",
		"test-cluster-cassandra-dc1-b-2": "10.0.1.3",
	}))
}
