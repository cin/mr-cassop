package controllers

import (
	"context"
	"strconv"
	"strings"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/compare"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/util"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
		prunePodIPsOfRemovedPods(cc, data, pods)

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

// prunePodIPsOfRemovedPods deletes entries for pods that no longer exist and whose ordinal is at or
// beyond their DC's replica count, or whose DC was removed. Those nodes were decommissioned, so a
// pod that later reuses the ordinal starts empty; handing it the old IP as CASSANDRA_NODE_PREVIOUS_IP
// would make it replace_address a node that already left the ring, which Cassandra refuses.
func prunePodIPsOfRemovedPods(cc *v1alpha1.CassandraCluster, data map[string]string, pods []v1.Pod) {
	existing := make(map[string]bool, len(pods))
	for _, pod := range pods {
		existing[pod.Name] = true
	}

	for podName := range data {
		if !existing[podName] && !podWithinDesiredReplicas(cc, podName) {
			delete(data, podName)
		}
	}
}

// podWithinDesiredReplicas reports whether podName belongs to one of cc's DCs and its ordinal is
// below that DC's replica count.
func podWithinDesiredReplicas(cc *v1alpha1.CassandraCluster, podName string) bool {
	for _, dc := range cc.Spec.DCs {
		ordinalStr, found := strings.CutPrefix(podName, names.DC(cc.Name, dc.Name)+"-")
		if !found {
			continue
		}
		ordinal, err := strconv.Atoi(ordinalStr)
		if err != nil { // a DC whose name has this DC's name as a prefix
			continue
		}
		return dc.Replicas != nil && int32(ordinal) < *dc.Replicas
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
