package controllers

import (
	"context"
	"strconv"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/labels"
)

// decommissionedPVCAnnotation marks a Cassandra PVC whose node the operator confirmed left the
// ring; the value is the pod's name. The operator only ever deletes PVCs carrying it, so a PVC
// left behind by anything else (a manual `kubectl scale`, a pod scaled away without a
// decommission) is never removed automatically.
const decommissionedPVCAnnotation = "db.ibm.com/decommissioned-pod"

// markPodPVCsDecommissioned annotates podName's PVCs for deletion. Call it only once
// podDecommissioned has confirmed the node no longer owns tokens.
func (r *CassandraClusterReconciler) markPodPVCsDecommissioned(ctx context.Context, sts appsv1.StatefulSet, podName string) error {
	for _, claim := range sts.Spec.VolumeClaimTemplates {
		pvc := &v1.PersistentVolumeClaim{}
		err := r.Get(ctx, types.NamespacedName{Namespace: sts.Namespace, Name: claim.Name + "-" + podName}, pvc)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return errors.Wrapf(err, "can't get PVC %s-%s", claim.Name, podName)
		}
		if pvc.Annotations[decommissionedPVCAnnotation] == podName {
			continue
		}

		patch := client.MergeFrom(pvc.DeepCopy())
		if pvc.Annotations == nil {
			pvc.Annotations = map[string]string{}
		}
		pvc.Annotations[decommissionedPVCAnnotation] = podName
		if err = r.Patch(ctx, pvc, patch); err != nil {
			return errors.Wrapf(err, "can't mark PVC %s as decommissioned", pvc.Name)
		}
	}
	return nil
}

// deleteDecommissionedPVCs deletes the marked PVCs whose pod is gone. It returns the pods that
// still have marked PVCs (not yet deletable, or deleted but not yet removed), so a scale-up can
// wait instead of letting a new pod reattach a decommissioned node's data.
func (r *CassandraClusterReconciler) deleteDecommissionedPVCs(ctx context.Context, cc *dbv1alpha1.CassandraCluster, pods []v1.Pod) (map[string]bool, error) {
	pvcs := &v1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(cc.Namespace), client.MatchingLabels(labels.Cassandra(cc))); err != nil {
		return nil, errors.Wrap(err, "can't list cassandra PVCs")
	}

	existingPods := make(map[string]bool, len(pods))
	for _, pod := range pods {
		existingPods[pod.Name] = true
	}

	pending := map[string]bool{}
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		podName, marked := pvc.Annotations[decommissionedPVCAnnotation]
		if !marked {
			continue
		}
		pending[podName] = true
		if existingPods[podName] || pvc.DeletionTimestamp != nil {
			continue
		}

		r.Log.Infof("deleting PVC %s of decommissioned node %s", pvc.Name, podName)
		if err := r.Delete(ctx, pvc); client.IgnoreNotFound(err) != nil {
			return nil, errors.Wrapf(err, "can't delete PVC %s", pvc.Name)
		}
	}
	return pending, nil
}

// scaleUpBlockedByPVC reports the first pod a scale-up from oldReplicas to newReplicas would
// create that still has decommissioned PVCs.
func scaleUpBlockedByPVC(stsName string, oldReplicas, newReplicas int32, pendingPVCPods map[string]bool) (string, bool) {
	for ordinal := oldReplicas; ordinal < newReplicas; ordinal++ {
		podName := stsName + "-" + strconv.Itoa(int(ordinal))
		if pendingPVCPods[podName] {
			return podName, true
		}
	}
	return "", false
}
