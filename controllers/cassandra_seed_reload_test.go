package controllers

import (
	"testing"

	"github.com/cin/mr-cassop/api/v1alpha1"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func seedReloadTestPod(name string, isSeed, ready bool) v1.Pod {
	labels := map[string]string{}
	if isSeed {
		labels[v1alpha1.CassandraClusterSeed] = "seed"
	}

	condStatus := v1.ConditionFalse
	if ready {
		condStatus = v1.ConditionTrue
	}

	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: v1.PodStatus{
			ContainerStatuses: []v1.ContainerStatus{
				{Ready: ready},
			},
			Conditions: []v1.PodCondition{
				{Type: v1.PodReady, Status: condStatus},
			},
		},
	}
}

func TestChangedSeedIPs(t *testing.T) {
	g := NewGomegaWithT(t)

	pods := []v1.Pod{
		seedReloadTestPod("dc1-0", true, true),  // seed, IP changed
		seedReloadTestPod("dc1-1", false, true), // non-seed, IP changed - should be ignored
		seedReloadTestPod("dc1-2", true, true),  // seed, IP unchanged
	}

	broadcastAddresses := map[string]string{
		"dc1-0": "10.244.2.37",
		"dc1-1": "10.244.3.99",
		"dc1-2": "10.244.1.35",
	}

	lastKnownIPs := map[string]string{
		"dc1-0": "10.244.2.35",
		"dc1-1": "10.244.3.10",
		"dc1-2": "10.244.1.35",
	}

	changed := changedSeedIPs(pods, broadcastAddresses, lastKnownIPs)

	g.Expect(changed).To(HaveLen(1))
	g.Expect(changed).To(HaveKeyWithValue("dc1-0", "10.244.2.37"))
}

func TestChangedSeedIPsIgnoresPodsWithNoRecordedIP(t *testing.T) {
	g := NewGomegaWithT(t)

	pods := []v1.Pod{
		seedReloadTestPod("dc1-0", true, true), // seed, never recorded before (e.g. brand new cluster)
	}

	broadcastAddresses := map[string]string{"dc1-0": "10.244.2.37"}
	lastKnownIPs := map[string]string{}

	changed := changedSeedIPs(pods, broadcastAddresses, lastKnownIPs)

	g.Expect(changed).To(BeEmpty())
}

func TestReadyPeerIPs(t *testing.T) {
	g := NewGomegaWithT(t)

	pods := []v1.Pod{
		seedReloadTestPod("dc1-0", true, true),   // the changed seed itself - excluded
		seedReloadTestPod("dc1-1", false, true),  // ready peer - included
		seedReloadTestPod("dc1-2", false, false), // not ready yet - excluded
	}

	broadcastAddresses := map[string]string{
		"dc1-0": "10.244.2.37",
		"dc1-1": "10.244.3.37",
		"dc1-2": "10.244.1.37",
	}

	peers := readyPeerIPs(pods, broadcastAddresses, "dc1-0")

	g.Expect(peers).To(ConsistOf("10.244.3.37"))
}
