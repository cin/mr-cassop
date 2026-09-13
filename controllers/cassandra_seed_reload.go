package controllers

import (
	"context"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/nodectl"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// seedReloadState bundles the pod/network state reconcileSeedReload's helpers all need,
// so it doesn't have to be threaded through each of them as separate parameters.
type seedReloadState struct {
	cc                 *v1alpha1.CassandraCluster
	pods               []v1.Pod
	broadcastAddresses map[string]string
}

// reconcileSeedReload detects a seed pod whose broadcast IP has changed since it was
// last seen ready (e.g. it was recreated by a rolling update) and asks every other
// already-running pod to reload its seed list via JMX. Cassandra's internode messaging
// only resolves seed hostnames once at daemon startup, so already-running peers never
// notice a seed's new IP on their own and can deadlock forever waiting to contact it -
// nodetool's "reloadseeds" (StorageService.reloadSeeds()) is the live fix, requiring no
// restart. This is a best-effort nudge, not a precondition: errors here are logged and
// swallowed, and the underlying condition is naturally retried every reconcile until the
// seed becomes ready again.
//
// The pod IPs configmap (our record of each pod's last-known-ready IP) only gets updated
// once the seed pod itself reports ready again, which for the seed's own readiness probe
// requires cross-node gossip agreement (see prober/prober/node_states.go:isNodeReady) - so
// it can lag the seed's actual IP change by a long, unpredictable amount under churn, and
// this reconciler runs far more often than that (every StatefulSet status change plus the
// retry backoff of any other in-flight condition). Without its own throttle this would
// detect the same "changed" IP and re-issue the JMX nudge on every single one of those
// reconciles until the configmap catches up - seedReloadNudgedIPs remembers the IP we last
// nudged peers about per seed pod so we only do it once per actual IP change.
func (r *CassandraClusterReconciler) reconcileSeedReload(
	ctx context.Context, cc *v1alpha1.CassandraCluster, podList *v1.PodList, nodesList *v1.NodeList,
) error {
	pods := excludePodsPendingRemoval(cc, podList.Items)

	broadcastAddresses, err := getBroadcastAddresses(cc, pods, nodesList.Items)
	if err != nil {
		return errors.Wrap(err, "can't get broadcast addresses")
	}

	state := seedReloadState{cc: cc, pods: pods, broadcastAddresses: broadcastAddresses}

	changedSeeds, err := r.changedUnnudgedSeedIPs(ctx, state)
	if err != nil || len(changedSeeds) == 0 {
		return err
	}

	return r.nudgePeersForChangedSeeds(ctx, state, changedSeeds)
}

// changedUnnudgedSeedIPs returns the current broadcast IP of every seed pod whose IP
// changed since it was last recorded, minus any we've already nudged peers about.
func (r *CassandraClusterReconciler) changedUnnudgedSeedIPs(
	ctx context.Context, state seedReloadState,
) (map[string]string, error) {
	podIPsCMName := names.PodIPsConfigMap(state.cc.Name)
	podIPsCM := &v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: podIPsCMName, Namespace: state.cc.Namespace}, podIPsCM)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil // nothing recorded yet, nothing to compare against
		}
		return nil, errors.Wrap(err, "can't get pod IPs configmap")
	}

	changed := changedSeedIPs(state.pods, state.broadcastAddresses, podIPsCM.Data)
	return r.unnudgedSeedIPs(state.cc, changed), nil
}

// nudgePeersForChangedSeeds asks every ready peer of each changed seed pod to reload its
// seed list via JMX, one seed at a time.
func (r *CassandraClusterReconciler) nudgePeersForChangedSeeds(
	ctx context.Context, state seedReloadState, changedSeeds map[string]string,
) error {
	adminSecret, err := r.adminRoleSecret(ctx, state.cc)
	if err != nil {
		return errors.Wrap(err, "can't get admin secret")
	}

	roleName, rolePassword, err := extractCredentials(adminSecret)
	if err != nil {
		return errors.Wrap(err, "can't extract admin credentials")
	}

	nctl := r.NodectlClient(jolokiaURL(state.cc).String(), roleName, rolePassword, r.Log)
	for seedPodName, newIP := range changedSeeds {
		r.nudgePeersForChangedSeed(ctx, nctl, state, seedPodName, newIP)
	}

	return nil
}

// nudgePeersForChangedSeed asks every ready peer of seedPodName to reload its seed list,
// then records newIP as nudged so this same change isn't repeated on later reconciles.
func (r *CassandraClusterReconciler) nudgePeersForChangedSeed(
	ctx context.Context, nctl nodectl.Nodectl, state seedReloadState, seedPodName, newIP string,
) {
	peerIPs := readyPeerIPs(state.pods, state.broadcastAddresses, seedPodName)
	for _, peerIP := range peerIPs {
		if err := nctl.ReloadSeeds(ctx, peerIP); err != nil {
			r.Log.Debugf("failed to reload seeds on peer %s after seed pod %s's IP changed: %s",
				peerIP, seedPodName, err)
		}
	}

	r.markSeedReloadNudged(state.cc, seedPodName, newIP)
	r.Log.Infof("seed pod %s's IP changed, asked %d peer(s) to reload their seed list", seedPodName, len(peerIPs))
}

// unnudgedSeedIPs drops any seed pod from changed whose current IP we've already nudged
// peers about, so a seed's IP change only triggers the reload once no matter how many
// reconciles pass before our own bookkeeping of its IP catches up.
func (r *CassandraClusterReconciler) unnudgedSeedIPs(
	cc *v1alpha1.CassandraCluster, changed map[string]string,
) map[string]string {
	filtered := make(map[string]string, len(changed))
	for podName, ip := range changed {
		key := seedReloadNudgeKey(cc, podName)
		if nudgedIP, ok := r.seedReloadNudgedIPs.Load(key); ok && nudgedIP == ip {
			continue
		}
		filtered[podName] = ip
	}

	return filtered
}

func (r *CassandraClusterReconciler) markSeedReloadNudged(cc *v1alpha1.CassandraCluster, podName, ip string) {
	r.seedReloadNudgedIPs.Store(seedReloadNudgeKey(cc, podName), ip)
}

func seedReloadNudgeKey(cc *v1alpha1.CassandraCluster, podName string) string {
	return cc.Namespace + "/" + cc.Name + "/" + podName
}

// changedSeedIPs returns the current broadcast IP of every seed pod whose IP differs
// from what was last recorded for it while it was ready.
func changedSeedIPs(pods []v1.Pod, broadcastAddresses, lastKnownIPs map[string]string) map[string]string {
	changed := make(map[string]string)
	for _, pod := range pods {
		if !isSeedPod(pod) {
			continue
		}

		lastIP := lastKnownIPs[pod.Name]
		if lastIP == "" {
			continue
		}

		currentIP := broadcastAddresses[pod.Name]
		if currentIP != "" && currentIP != lastIP {
			changed[pod.Name] = currentIP
		}
	}

	return changed
}

// readyPeerIPs returns the broadcast IPs of every ready pod other than excludePodName.
func readyPeerIPs(pods []v1.Pod, broadcastAddresses map[string]string, excludePodName string) []string {
	var ips []string
	for _, pod := range pods {
		if pod.Name == excludePodName || !podReady(pod) {
			continue
		}
		if ip := broadcastAddresses[pod.Name]; ip != "" {
			ips = append(ips, ip)
		}
	}

	return ips
}
