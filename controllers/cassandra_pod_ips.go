package controllers

import (
	"context"
	"strconv"
	"strings"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/compare"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/util"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *CassandraClusterReconciler) reconcilePodIPsConfigMap(ctx context.Context, cc *v1alpha1.CassandraCluster, pods []v1.Pod, broadcastAddresses map[string]string) (map[string]string, error) {
	desiredCM := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.PodIPsConfigMap(cc.Name),
			Namespace: cc.Namespace,
		},
	}

	err := controllerutil.SetControllerReference(cc, desiredCM, r.Scheme)
	if err != nil {
		return nil, errors.Wrap(err, "can't set controller reference")
	}

	actualCM := &v1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: names.PodIPsConfigMap(cc.Name), Namespace: cc.Namespace}, actualCM)
	if err != nil && apierrors.IsNotFound(err) {
		err = r.Create(ctx, desiredCM)
		if err != nil {
			return nil, errors.Wrap(err, "can't create pod IPs configmap")
		}
		return desiredCM.Data, nil
	} else if err != nil {
		return nil, errors.Wrap(err, "can't get pod IPs configmap")
	} else {
		if actualCM.Data == nil {
			actualCM.Data = make(map[string]string)
		}

		// keep previous IPs for pods that are only temporarily gone (e.g. being recreated), but drop
		// the ones that were scaled away
		data := util.MergeMap(make(map[string]string, len(broadcastAddresses)), actualCM.Data)
		stsReplicas, err := r.statefulSetReplicas(ctx, cc)
		if err != nil {
			return nil, err
		}
		prunePodIPsOfRemovedPods(data, pods, stsReplicas)

		for _, pod := range pods {
			if data[pod.Name] != broadcastAddresses[pod.Name] && broadcastAddresses[pod.Name] != "" && podReady(pod) {
				data[pod.Name] = broadcastAddresses[pod.Name]

			}
		}

		desiredCM.Annotations = util.MergeMap(actualCM.Annotations, desiredCM.Annotations)
		desiredCM.Data = data
		if !compare.EqualConfigMap(actualCM, desiredCM) {
			r.Log.Info("Updating pod IPs configmap")
			r.Log.Debugf(compare.DiffConfigMap(actualCM, desiredCM))
			r.Log.Debugf(cmp.Diff(actualCM.Data, desiredCM.Data))
			actualCM.Data = data
			actualCM.Labels = desiredCM.Labels
			actualCM.OwnerReferences = desiredCM.OwnerReferences
			actualCM.Annotations = desiredCM.Annotations
			err = r.Update(ctx, actualCM)
			if err != nil {
				return nil, errors.Wrap(err, "can't update pod IPs configmap")
			}
		} else {
			r.Log.Debugf("No updates for pod IPs configmap")
		}
	}

	return actualCM.Data, nil
}

// statefulSetReplicas returns the current replica count of each of cc's Cassandra StatefulSets,
// keyed by StatefulSet name.
func (r *CassandraClusterReconciler) statefulSetReplicas(ctx context.Context, cc *v1alpha1.CassandraCluster) (map[string]int32, error) {
	stsList := &appsv1.StatefulSetList{}
	if err := r.List(ctx, stsList, client.InNamespace(cc.Namespace), client.MatchingLabels(labels.Cassandra(cc))); err != nil {
		return nil, errors.Wrap(err, "can't list statefulsets")
	}

	replicas := make(map[string]int32, len(stsList.Items))
	for _, sts := range stsList.Items {
		replicas[sts.Name] = ptr.Deref(sts.Spec.Replicas, 1)
	}
	return replicas, nil
}

// prunePodIPsOfRemovedPods deletes entries for pods that no longer exist and whose ordinal is at or
// beyond their StatefulSet's current replica count, or whose StatefulSet is gone. Those nodes were
// decommissioned, so a pod that later reuses the ordinal starts empty; handing it the old IP as
// CASSANDRA_NODE_PREVIOUS_IP would make it replace_address a node that already left the ring, which
// Cassandra refuses. The StatefulSet's count (not the CR's desired count) is used because the
// operator only lowers it once the node has been decommissioned: a pod above the desired count
// that is still mid-decommission, or briefly absent, keeps its entry.
func prunePodIPsOfRemovedPods(data map[string]string, pods []v1.Pod, stsReplicas map[string]int32) {
	existing := make(map[string]bool, len(pods))
	for _, pod := range pods {
		existing[pod.Name] = true
	}

	for podName := range data {
		if !existing[podName] && !podWithinStatefulSet(podName, stsReplicas) {
			delete(data, podName)
		}
	}
}

// podWithinStatefulSet reports whether podName belongs to one of the StatefulSets and its ordinal
// is below that StatefulSet's replica count.
func podWithinStatefulSet(podName string, stsReplicas map[string]int32) bool {
	for stsName, replicas := range stsReplicas {
		ordinalStr, found := strings.CutPrefix(podName, stsName+"-")
		if !found {
			continue
		}
		ordinal, err := strconv.Atoi(ordinalStr)
		if err != nil { // a StatefulSet whose name has this one's name as a prefix
			continue
		}
		return int32(ordinal) < replicas
	}
	return false
}

func podReady(pod v1.Pod) bool {
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if !containerStatus.Ready {
			return false
		}
	}

	return true
}
