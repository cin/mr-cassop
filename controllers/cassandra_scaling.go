package controllers

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/types"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/nodectl"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

var errDCDecommissionBlocked = errors.New("DC decommission blocked")

// opModeCheckTimeout bounds the operation mode lookup made after a decommission call returned an error.
const opModeCheckTimeout = 30 * time.Second

func (r *CassandraClusterReconciler) reconcileCassandraScaling(ctx context.Context, cc *dbv1alpha1.CassandraCluster, podList *v1.PodList, nodesList *v1.NodeList, allDCs []dbv1alpha1.DC, adminRoleSecret *v1.Secret) (bool, error) {
	broadcastAddresses, err := getBroadcastAddresses(cc, excludePodsPendingRemoval(cc, podList.Items), nodesList.Items)
	if err != nil {
		return false, errors.Wrap(err, "can't get broadcast addresses")
	}

	stsList := &appsv1.StatefulSetList{}
	err = r.List(ctx, stsList, client.InNamespace(cc.Namespace), client.MatchingLabels(labels.Cassandra(cc)))
	if err != nil {
		return false, errors.Wrap(err, "can't get statefulsets")
	}

	pendingPVCPods, err := r.deleteDecommissionedPVCs(ctx, cc, podList.Items)
	if err != nil {
		return false, errors.Wrap(err, "can't clean up decommissioned PVCs")
	}

	if len(stsList.Items) == 0 {
		r.Log.Warn("couldn't find any statefulset to reconcile scaling")
		return false, nil
	}

	dcToReplicas := dcsMap(cc)
	desiredSTSNames, desiredSts := desiredDCsSTS(cc, stsList.Items)
	stsToDecommissionNames, stsToDecommission := decommissionDCsSTS(cc, stsList.Items)

	for _, stsName := range desiredSTSNames {
		sts := desiredSts[stsName]
		var oldReplicas int32 = 1
		var newReplicas int32 = 1
		if sts.Spec.Replicas != nil {
			oldReplicas = *sts.Spec.Replicas
		}

		newReplicas = dcToReplicas[sts.Labels[dbv1alpha1.CassandraClusterDC]]

		if oldReplicas == newReplicas { // no need for scaling
			continue
		}

		if oldReplicas < newReplicas { // scale up
			if podName, blocked := scaleUpBlockedByPVC(sts.Name, oldReplicas, newReplicas, pendingPVCPods); blocked {
				msg := fmt.Sprintf("Waiting for the decommissioned PVCs of pod %s to be deleted before scaling up", podName)
				r.Events.Normal(cc, events.EventScaleUpWaitingForPVCs, msg)
				r.Log.Info(msg)
				continue
			}
			sts.Spec.Replicas = &newReplicas
			err = r.Update(ctx, &sts)
			if err != nil {
				return true, errors.Wrap(err, "can't update replicas number for statefulset")
			}

			continue
		}

		// scale down
		if len(podList.Items) == 0 {
			r.Log.Warn("No pods found to perform scaledown")
			continue
		}

		decommissionPodName := sts.Name + "-" + strconv.Itoa(int(oldReplicas)-1)
		r.Log.Debugf("handling decommission for node %s", decommissionPodName)
		err = r.handlePodDecommission(ctx, cc, sts, broadcastAddresses, decommissionPodName, podList)
		if err != nil {
			return false, errors.Wrap(err, "failed to handle pod decommission")
		}

		return true, nil //return from the loop to scale down only one statefulset at a time
	}

	if len(stsToDecommissionNames) == 0 {
		return false, nil
	}

	err = r.handleDCsDecommission(ctx, cc, stsToDecommissionNames, stsToDecommission, allDCs, adminRoleSecret, broadcastAddresses, podList)
	if err != nil {
		return false, errors.Wrap(err, "failed to decommission DCs")
	}

	return true, nil
}

func (r *CassandraClusterReconciler) handleDCsDecommission(ctx context.Context, cc *dbv1alpha1.CassandraCluster, stsNames []string, stsList map[string]appsv1.StatefulSet, allDCs []dbv1alpha1.DC, adminRoleSecret *v1.Secret, broadcastAddresses map[string]string, podList *v1.PodList) error {
	r.Log.Infof("Decommissioning DCs %v", stsNames)

	keyspacesToReconcile := desiredKeyspacesToReconcile(cc)
	cassandraOperatorAdminRole, cassandraOperatorAdminPassword, err := extractCredentials(adminRoleSecret)
	if err != nil {
		return err
	}
	cqlClient, err := r.CqlClient(newCassandraConfig(cc, cassandraOperatorAdminRole, cassandraOperatorAdminPassword, r.Log))
	if err != nil {
		return errors.Wrap(err, "can't create cql client")
	}
	defer cqlClient.CloseSession()

	currentKeyspaces, err := cqlClient.GetKeyspacesInfo()
	if err != nil {
		return errors.Wrap(err, "can't get keyspace info")
	}

	//prevent decommission if the DC still in use
	for _, existingKeyspace := range currentKeyspaces {
		if !keyspaceExists(keyspacesToReconcile, existingKeyspace.Name) { //not a system keyspace
			for _, sts := range stsList {
				if _, exists := existingKeyspace.Replication[sts.Labels[dbv1alpha1.CassandraClusterDC]]; exists {
					errMsg := fmt.Sprintf("Can't decommission DC %q since keyspace %q still replicates to that DC. "+
						"Remove that DC from replication settings for the decommission to proceed.", sts.Labels[dbv1alpha1.CassandraClusterDC], existingKeyspace.Name)
					r.Events.Warning(cc, events.EventDCDecommissionBlocked, errMsg)
					r.Log.Warnf(errMsg)
					return errDCDecommissionBlocked
				}
			}
		}
	}

	// prepare system keyspaces to not replicate to DCs being decommissioned
	for _, systemKeyspace := range keyspacesToReconcile {
		// system_auth should be reconciled only after scaledown is finished, otherwise will get quorum errors on JMX requests
		if systemKeyspace == keyspaceSystemAuth {
			continue
		}
		keyspaceInfo, found := getKeyspaceByName(currentKeyspaces, string(systemKeyspace))
		if !found {
			r.Log.Warnf("Keyspace %q doesn't exists", systemKeyspace)
			continue
		}

		desiredOptions := desiredReplicationOptions(cc, string(systemKeyspace), allDCs)
		if !cmp.Equal(keyspaceInfo.Replication, desiredOptions) {
			r.Log.Infof("Updating keyspace %q with replication options %v", systemKeyspace, desiredOptions)
			err = cqlClient.UpdateRF(string(systemKeyspace), desiredOptions)
			if err != nil {
				return errors.Wrapf(err, "failed to alter %q keyspace", systemKeyspace)
			}
		}
	}

	// decommission DCs
	for _, stsName := range stsNames {
		sts := stsList[stsName]
		if sts.Spec.Replicas != nil && *sts.Spec.Replicas == 0 { //all pods for DC are decommissioned already
			err = r.removeDC(ctx, cc, sts)
			if err != nil {
				return errors.Wrapf(err, "failed to remove dc %s", sts.Labels[dbv1alpha1.CassandraClusterDC])
			}

			return nil
		}
		decommissionPodName := sts.Name + "-" + strconv.Itoa(int(*sts.Spec.Replicas)-1)
		err = r.handlePodDecommission(ctx, cc, sts, broadcastAddresses, decommissionPodName, podList)
		if err != nil {
			return errors.Wrap(err, "failed to handle pod decommission")
		}
	}

	return nil
}

func findPod(podList *v1.PodList, name string) *v1.Pod {
	for i := range podList.Items {
		if podList.Items[i].Name == name {
			return &podList.Items[i]
		}
	}

	return nil
}

func (r *CassandraClusterReconciler) handlePodDecommission(ctx context.Context, cc *dbv1alpha1.CassandraCluster, sts appsv1.StatefulSet, broadcastAddresses map[string]string, decommissionPodName string, podList *v1.PodList) error {
	jobName := "pod-decommission-" + decommissionPodName
	if r.Jobs.IsRunning(jobName) {
		r.Log.Infof("decommission in progress, waiting to finish")
		return nil
	}

	decommissionPod := findPod(podList, decommissionPodName)
	if decommissionPod == nil {
		return errors.Errorf("couldn't find pod %q to start the decommission", decommissionPodName)
	}

	if len(decommissionPod.Status.PodIP) == 0 {
		return r.handlePodWithoutIP(ctx, cc, sts, decommissionPod)
	}

	adminSecret, err := r.adminRoleSecret(ctx, cc)
	if err != nil {
		r.Log.Warnf("can't get admin secret %s", adminSecret.Name)
	}

	roleName, rolePassword, err := extractCredentials(adminSecret)
	if err != nil {
		r.Log.Warn("can't extract secret data")
	}
	nctl := r.NodectlClient(jolokiaURL(cc).String(), roleName, rolePassword, r.Log)

	broadcastIP := broadcastAddresses[decommissionPod.Name]
	r.Log.Debugf("checking operation mode for node %s", decommissionPod.Name)
	opMode, opModeErr := nctl.OperationMode(ctx, broadcastIP)
	if opModeErr != nil { // the node may be decommissioned
		r.Log.Warnf("couldn't get operation mode for pod %s, checking if it is decommissioned already.", decommissionPodName)
		return r.scaleDownIfDecommissioned(ctx, cc, sts, nctl, decommissionPod, podList, broadcastAddresses,
			errors.Wrapf(opModeErr, "can't reach node %s to check its operation mode", decommissionPodName))
	}

	if opMode == nodectl.NodeOperationModeNormal {
		if err = r.checkScaleDownNotBlockedByReplication(cc, sts, roleName, rolePassword); err != nil {
			return err
		}
	}

	return r.handleDecommissionOpMode(ctx, cc, sts, nctl, decommissionPod, podList, broadcastAddresses, opMode)
}

// handleDecommissionOpMode decides what to do with the node being scaled away based on its operation mode.
// Only a NORMAL node can start a decommission. A node that failed to decommission or join is not retried:
// the cause (e.g. a keyspace's replication factor is higher than the remaining node count) doesn't go away
// on its own, so the operator reports it and leaves the node alone.
func (r *CassandraClusterReconciler) handleDecommissionOpMode(ctx context.Context, cc *dbv1alpha1.CassandraCluster, sts appsv1.StatefulSet, nctl nodectl.Nodectl, pod *v1.Pod, podList *v1.PodList, broadcastAddresses map[string]string, opMode nodectl.OperationMode) error {
	switch opMode {
	case nodectl.NodeOperationModeNormal:
		return r.startDecommission(cc, nctl, pod, broadcastAddresses[pod.Name])
	case nodectl.NodeOperationModeDecommissioned:
		return r.scaleDownIfDecommissioned(ctx, cc, sts, nctl, pod, podList, broadcastAddresses, errors.Errorf("node %s is DECOMMISSIONED", pod.Name))
	case nodectl.NodeOperationModeDecommissionFailed:
		r.warnWithEvent(cc, events.EventDecommissionFailed, fmt.Sprintf("Node %s is in DECOMMISSION_FAILED after a refused or failed decommission, and it's not retried automatically. "+
			"The node is still in the ring and serving. The most common cause is a keyspace whose replication factor in the DC is not lower than its node count (see the "+
			"earlier DecommissionFailed event for Cassandra's message). The operator never changes replication factors: lower them yourself, then run "+
			"`nodetool decommission` on the pod, or restart the pod to clear the state and let the scale-down start again. Raising the DC's replicas does not clear it", pod.Name))
		return errors.Errorf("node %s is in DECOMMISSION_FAILED; waiting before scaling down", pod.Name)
	case nodectl.NodeOperationModeJoiningFailed:
		r.warnWithEvent(cc, events.EventNodeJoiningFailed, fmt.Sprintf("Node %s failed to join the ring (JOINING_FAILED), so it can't be decommissioned. Restart the pod to retry the join", pod.Name))
		return errors.Errorf("node %s is in JOINING_FAILED; waiting before scaling down", pod.Name)
	default: // LEAVING, or a transient mode (STARTING, JOINING, MOVING, DRAINING, DRAINED)
		r.Log.Infof("node %s/%s is in operation mode %s, waiting before decommissioning it", pod.Namespace, pod.Name, opMode)
		return nil
	}
}

// scaleDownIfDecommissioned scales the statefulset down once a quorum of peers no longer sees the node in
// the ring. Otherwise it returns an error built from reason: the decommission hasn't finished propagating,
// or the node is down but still owns tokens. A down node blocks scale-down until it's back or removed from
// the ring by hand (nodetool removenode), rather than being scaled away.
func (r *CassandraClusterReconciler) scaleDownIfDecommissioned(ctx context.Context, cc *dbv1alpha1.CassandraCluster, sts appsv1.StatefulSet, nctl nodectl.Nodectl, pod *v1.Pod, podList *v1.PodList, broadcastAddresses map[string]string, reason error) error {
	decommissioned, err := r.podDecommissioned(ctx, cc, nctl, podList.Items, *pod, broadcastAddresses)
	if err != nil {
		return errors.Wrap(err, "failed to check if the pod is decommissioned")
	}
	if !decommissioned {
		return errors.Wrapf(reason, "peer node(s) still list %s in the ring (down, or not yet fully decommissioned); waiting before scaling down", pod.Name)
	}

	// mark before scaling down, so a failure here is retried while the pod still exists
	if err = r.markPodPVCsDecommissioned(ctx, sts, pod.Name); err != nil {
		return err
	}
	*sts.Spec.Replicas = *sts.Spec.Replicas - 1
	r.Log.Infof("node %s/%s is decommissioned, scaling down the statefulset", pod.Namespace, pod.Name)
	if err = r.Update(ctx, &sts); err != nil {
		return errors.Wrap(err, "couldn't scale down the statefulset to remove the decommissioned node")
	}

	return nil
}

// warnWithEvent logs msg and emits it as a warning event, so it's visible both in the operator logs and in `kubectl describe`.
func (r *CassandraClusterReconciler) warnWithEvent(cc *dbv1alpha1.CassandraCluster, reason events.EventReason, msg string) {
	r.Log.Warn(msg)
	r.Events.Warning(cc, reason, msg)
}

func (r *CassandraClusterReconciler) startDecommission(cc *dbv1alpha1.CassandraCluster, nctl nodectl.Nodectl, pod *v1.Pod, broadcastIP string) error {
	r.Log.Infof("starting decommision of node %s/%s", pod.Namespace, pod.Name)
	err := r.Jobs.Run("pod-decommission-"+pod.Name, cc, func() error {
		return r.runDecommission(cc, nctl, pod, broadcastIP)
	})
	if err != nil {
		return errors.Wrapf(err, "failed to start job to decommission pod %s", pod.Name)
	}

	return nil
}

// runDecommission blocks until the decommission call returns. The call can return an error while
// Cassandra keeps decommissioning: the Jolokia-to-JMX connection has its own read timeout (about
// 30s), far shorter than streaming a node's data out. So an error is only a failed decommission
// if the node isn't leaving the ring; otherwise the operation mode drives the rest of the scale-down.
func (r *CassandraClusterReconciler) runDecommission(cc *dbv1alpha1.CassandraCluster, nctl nodectl.Nodectl, pod *v1.Pod, broadcastIP string) error {
	decommissionCtx := context.Background() //reconcile context may cancel the job sooner that needed
	decommissionErr := nctl.Decommission(decommissionCtx, broadcastIP)
	if decommissionErr == nil {
		return nil
	}

	if r.nodeIsLeaving(nctl, broadcastIP) {
		r.Log.Infof("decommission call for node %s/%s returned an error, but the node is leaving the ring: %s", pod.Namespace, pod.Name, decommissionErr.Error())
		return nil
	}

	r.Log.Errorf("failed to decommission pod, error: %s", decommissionErr.Error())
	r.warnWithEvent(cc, events.EventDecommissionFailed, fmt.Sprintf("Decommission of node %s failed: %s", pod.Name, decommissionErr.Error()))
	return decommissionErr
}

// nodeIsLeaving reports whether the node is decommissioning or has finished doing so.
func (r *CassandraClusterReconciler) nodeIsLeaving(nctl nodectl.Nodectl, nodeIP string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), opModeCheckTimeout)
	defer cancel()

	opMode, err := nctl.OperationMode(ctx, nodeIP)
	if err != nil {
		r.Log.Warnf("couldn't get the operation mode of %s after a failed decommission call: %s", nodeIP, err.Error())
		return false
	}

	return opMode == nodectl.NodeOperationModeLeaving || opMode == nodectl.NodeOperationModeDecommissioned
}

// handlePodWithoutIP scales away a pod that has no IP, unless it's recorded in the pod IPs configmap.
// An unrecorded pod never went Ready, so it never joined the ring (e.g. leftover from a scale-up
// that exceeded cluster capacity) and there's nothing to decommission. A recorded pod joined and
// only lost its IP because it can't run right now (e.g. its Kubernetes node died); scaling it away
// would leave the node in the ring as down, owning tokens, until someone runs nodetool removenode.
func (r *CassandraClusterReconciler) handlePodWithoutIP(ctx context.Context, cc *dbv1alpha1.CassandraCluster, sts appsv1.StatefulSet, pod *v1.Pod) error {
	joined, err := r.podRecordedAsJoined(ctx, cc, pod.Name)
	if err != nil {
		return err
	}

	if joined {
		msg := fmt.Sprintf("Pod %s has no IP but was part of the ring before, so it's not scaled away without being decommissioned. "+
			"Bring the pod back, or remove its node from the ring by hand (nodetool removenode) if it's gone for good", pod.Name)
		r.Events.Warning(cc, events.EventScaleDownWaitingForNode, msg)
		return errors.Errorf("pod %s has no IP but joined the ring earlier; waiting before scaling down", pod.Name)
	}

	r.Log.Infof("pod %s/%s never joined the cluster (no IP assigned), scaling down the statefulset without decommissioning",
		pod.Namespace, pod.Name)
	*sts.Spec.Replicas = *sts.Spec.Replicas - 1
	return r.Update(ctx, &sts)
}

// podRecordedAsJoined reports whether the pod IPs configmap has an entry for the pod. The operator
// records a pod's IP the first time it goes Ready, and prober only reports a node Ready once its
// gossip status is NORMAL, so an entry means the node joined the ring.
func (r *CassandraClusterReconciler) podRecordedAsJoined(ctx context.Context, cc *dbv1alpha1.CassandraCluster, podName string) (bool, error) {
	podIPsCM := &v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: names.PodIPsConfigMap(cc.Name), Namespace: cc.Namespace}, podIPsCM)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "can't get pod IPs configmap")
	}

	_, recorded := podIPsCM.Data[podName]
	return recorded, nil
}

func (r *CassandraClusterReconciler) podDecommissioned(ctx context.Context, cc *dbv1alpha1.CassandraCluster, nctl nodectl.Nodectl, pods []v1.Pod, decommissionPod v1.Pod, broadcastAddresses map[string]string) (bool, error) {
	quorum := 0 //number of pods that need to agree that the pods is decommissioned
	for _, pod := range pods {
		podDC := pod.Labels[dbv1alpha1.CassandraClusterDC]
		for _, dc := range cc.Spec.DCs {
			if podDC == dc.Name {
				quorum++
				break
			}
		}
	}
	quorum-- //one of the pods can be in the process of decommission

	desiredDCs := dcsMap(cc)

	notInRingView := 0
	for _, pod := range pods {
		_, exists := desiredDCs[pod.Labels[dbv1alpha1.CassandraClusterDC]]

		// if it's the pod being decommissioned, or a pod from a DC being decommissioned
		if pod.Name == decommissionPod.Name || !exists {
			continue
		}

		r.Log.Debugf("checking cluster view for node %s", pod.Name)
		clusterView, nctlErr := nctl.ClusterView(ctx, broadcastAddresses[pod.Name])
		if nctlErr != nil {
			return false, errors.Wrap(nctlErr, "can't get cluster view")
		}

		// Not being in LiveNodes also covers a node that's merely down, and scaling that away
		// would delete the PVC of a node that still owns tokens. Only token ownership says the
		// node actually left the ring.
		ownsTokens, err := clusterView.OwnsTokens(broadcastAddresses[decommissionPod.Name])
		if err != nil {
			return false, errors.Wrapf(err, "can't tell from node %s whether %s left the ring", pod.Name, decommissionPod.Name)
		}
		if !ownsTokens {
			notInRingView++
		}
	}

	r.Log.Debugf("%d nodes don't see node %s in the ring", notInRingView, decommissionPod.Name)
	return notInRingView >= quorum, nil
}

func (r *CassandraClusterReconciler) removeDC(ctx context.Context, cc *dbv1alpha1.CassandraCluster, sts appsv1.StatefulSet) error {
	dcName := sts.Labels[dbv1alpha1.CassandraClusterDC]

	reaperDeploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: names.ReaperDeployment(cc.Name, dcName), Namespace: cc.Namespace}, reaperDeploy)
	if err == nil {
		err = r.Delete(ctx, reaperDeploy)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	svc := &v1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: names.DCService(cc.Name, dcName), Namespace: cc.Namespace}, svc)
	if err == nil {
		err = r.Delete(ctx, svc)
		if err == nil {
			return errors.WithStack(err)
		}
	}

	err = r.Delete(ctx, &sts)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func dcsMap(cc *dbv1alpha1.CassandraCluster) map[string]int32 {
	dcToReplicas := make(map[string]int32)
	for _, dc := range cc.Spec.DCs {
		dcToReplicas[dc.Name] = *dc.Replicas
	}

	return dcToReplicas
}

func desiredDCsSTS(cc *dbv1alpha1.CassandraCluster, allSTS []appsv1.StatefulSet) (desiredSTSNames []string, desiredSTS map[string]appsv1.StatefulSet) {
	desiredSTS = make(map[string]appsv1.StatefulSet)
	for i, sts := range allSTS {
		for _, dc := range cc.Spec.DCs {
			if sts.Labels[dbv1alpha1.CassandraClusterDC] == dc.Name {
				desiredSTS[allSTS[i].Name] = allSTS[i]
				desiredSTSNames = append(desiredSTSNames, sts.Name)
				break
			}
		}
	}

	sort.Strings(desiredSTSNames)

	return desiredSTSNames, desiredSTS
}

func decommissionDCsSTS(cc *dbv1alpha1.CassandraCluster, allSTS []appsv1.StatefulSet) (stsToDecommissionNames []string, stsToDecommission map[string]appsv1.StatefulSet) {
	stsToDecommission = make(map[string]appsv1.StatefulSet)
	for i, sts := range allSTS {
		desiredSTS := false
		for _, dc := range cc.Spec.DCs {
			if sts.Labels[dbv1alpha1.CassandraClusterDC] == dc.Name {
				desiredSTS = true
				break
			}
		}
		if !desiredSTS {
			stsToDecommission[sts.Name] = allSTS[i]
			stsToDecommissionNames = append(stsToDecommissionNames, sts.Name)
		}
	}

	sort.Strings(stsToDecommissionNames)

	return stsToDecommissionNames, stsToDecommission
}
