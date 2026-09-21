package controllers

import (
	"testing"

	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cin/mr-cassop/api/v1alpha1"
)

func TestPrunePodIPsOfRemovedPods(t *testing.T) {
	asserts := NewGomegaWithT(t)

	cc := &v1alpha1.CassandraCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
		Spec: v1alpha1.CassandraClusterSpec{DCs: []v1alpha1.DC{
			{Name: "dc1", Replicas: proto.Int(2)},
			{Name: "dc1-b", Replicas: proto.Int(3)}, // name has "dc1" as a prefix
		}},
	}
	data := map[string]string{
		"test-cluster-cassandra-dc1-0":   "10.0.0.1", // exists
		"test-cluster-cassandra-dc1-1":   "10.0.0.2", // temporarily gone, within replicas
		"test-cluster-cassandra-dc1-2":   "10.0.0.3", // still exists, being decommissioned
		"test-cluster-cassandra-dc1-3":   "10.0.0.4", // scaled away
		"test-cluster-cassandra-dc1-b-2": "10.0.1.3", // temporarily gone, within dc1-b's replicas
		"test-cluster-cassandra-dc2-0":   "10.0.2.1", // DC removed
	}
	pods := []v1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1-0"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-cassandra-dc1-2"}},
	}

	prunePodIPsOfRemovedPods(cc, data, pods)

	asserts.Expect(data).To(Equal(map[string]string{
		"test-cluster-cassandra-dc1-0":   "10.0.0.1",
		"test-cluster-cassandra-dc1-1":   "10.0.0.2",
		"test-cluster-cassandra-dc1-2":   "10.0.0.3",
		"test-cluster-cassandra-dc1-b-2": "10.0.1.3",
	}))
}
