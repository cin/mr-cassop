package controllers

import (
	"context"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/compare"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/pkg/errors"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// reconcilePodDisruptionBudgets keeps one PodDisruptionBudget per DC, so a node drain or any other voluntary
// eviction can't take more than spec.cassandra.podDisruptionBudget.maxUnavailable pods of a DC down at once.
// Budgets of DCs that were removed, and all of them when the feature is disabled, are deleted.
func (r *CassandraClusterReconciler) reconcilePodDisruptionBudgets(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	keep := map[string]bool{}
	if enabled := cc.Spec.Cassandra.PodDisruptionBudget.Enabled; enabled != nil && *enabled {
		for _, dc := range cc.Spec.DCs {
			keep[names.DC(cc.Name, dc.Name)] = true
			if err := r.reconcileDCPodDisruptionBudget(ctx, cc, dc); err != nil {
				return errors.Wrapf(err, "failed to reconcile pod disruption budget of dc %q", dc.Name)
			}
		}
	}

	return r.deleteStalePodDisruptionBudgets(ctx, cc, keep)
}

func desiredDCPodDisruptionBudget(cc *dbv1alpha1.CassandraCluster, dc dbv1alpha1.DC) *policyv1.PodDisruptionBudget {
	pdbLabels := labels.WithDCLabel(labels.CombinedComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra), dc.Name)
	maxUnavailable := intstr.FromInt32(defaultPodDisruptionBudgetMaxUnavailable)
	if configured := cc.Spec.Cassandra.PodDisruptionBudget.MaxUnavailable; configured != nil {
		maxUnavailable = intstr.FromInt32(*configured)
	}

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.DC(cc.Name, dc.Name),
			Namespace: cc.Namespace,
			Labels:    pdbLabels,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels.WithDCLabel(labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra), dc.Name),
			},
		},
	}
}

func (r *CassandraClusterReconciler) reconcileDCPodDisruptionBudget(ctx context.Context, cc *dbv1alpha1.CassandraCluster, dc dbv1alpha1.DC) error {
	desired := desiredDCPodDisruptionBudget(cc, dc)
	if err := controllerutil.SetControllerReference(cc, desired, r.Scheme); err != nil {
		return errors.Wrap(err, "cannot set controller reference")
	}

	actual := &policyv1.PodDisruptionBudget{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, actual)
	if apierrors.IsNotFound(err) {
		r.Log.Infof("Creating pod disruption budget for DC %q", dc.Name)
		return errors.Wrap(r.Create(ctx, desired), "failed to create pod disruption budget")
	}
	if err != nil {
		return errors.Wrap(err, "failed to get pod disruption budget")
	}

	return r.updatePodDisruptionBudget(ctx, desired, actual, dc.Name)
}

func (r *CassandraClusterReconciler) updatePodDisruptionBudget(ctx context.Context, desired, actual *policyv1.PodDisruptionBudget, dcName string) error {
	if compare.EqualPodDisruptionBudget(actual, desired) {
		r.Log.Debugf("No updates to pod disruption budget for DC %q", dcName)
		return nil
	}
	r.Log.Infof("Updating pod disruption budget for DC %q", dcName)
	r.Log.Debug(compare.DiffPodDisruptionBudget(actual, desired))
	actual.Spec = desired.Spec
	actual.Labels = desired.Labels
	return errors.Wrap(r.Update(ctx, actual), "failed to update pod disruption budget")
}

// deleteStalePodDisruptionBudgets deletes the cluster's budgets whose names aren't in keep.
func (r *CassandraClusterReconciler) deleteStalePodDisruptionBudgets(ctx context.Context, cc *dbv1alpha1.CassandraCluster, keep map[string]bool) error {
	existing := &policyv1.PodDisruptionBudgetList{}
	selector := client.MatchingLabels(labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra))
	if err := r.List(ctx, existing, client.InNamespace(cc.Namespace), selector); err != nil {
		return errors.Wrap(err, "failed to list pod disruption budgets")
	}

	for i := range existing.Items {
		pdb := &existing.Items[i]
		if keep[pdb.Name] {
			continue
		}
		r.Log.Infof("Deleting pod disruption budget %q", pdb.Name)
		if err := r.Delete(ctx, pdb); client.IgnoreNotFound(err) != nil {
			return errors.Wrapf(err, "failed to delete pod disruption budget %q", pdb.Name)
		}
	}

	return nil
}
