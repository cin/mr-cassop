package controllers

import (
	"context"
	"fmt"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/util"
	"github.com/pkg/errors"
)

type checksumContainer map[string]string

func (c checksumContainer) checksum() string {
	return util.Sha1(fmt.Sprintf("%v", c))
}

var (
	errTLSSecretNotFound = errors.New("TLS secret not found")
	errTLSSecretInvalid  = errors.New("TLS secret is not valid")
)

func (r *CassandraClusterReconciler) reconcileCassandra(ctx context.Context, cc *dbv1alpha1.CassandraCluster, restartChecksum checksumContainer) error {
	rollStarted, err := r.reconcileDCs(ctx, cc, restartChecksum)
	if err != nil {
		return err
	}

	if err = r.reconcileCassandraPodLabels(ctx, cc); err != nil {
		return errors.Wrap(err, "Failed to reconcile cassandra pods labels")
	}

	if err = r.reconcilePodDisruptionBudgets(ctx, cc); err != nil {
		return errors.Wrap(err, "Failed to reconcile pod disruption budgets")
	}

	if rollStarted {
		return errRollStarted
	}

	return nil
}

// reconcileDCs reconciles the service and the statefulset of every DC, in one pass whether or not repairs were
// paused for a roll. It reports whether the update of any statefulset paused repairs.
func (r *CassandraClusterReconciler) reconcileDCs(ctx context.Context, cc *dbv1alpha1.CassandraCluster, restartChecksum checksumContainer) (bool, error) {
	rollStarted := false
	for _, dc := range cc.Spec.DCs {
		if err := r.reconcileDCService(ctx, cc, dc); err != nil {
			return false, errors.Wrapf(err, "failed to reconcile dc %q", dc.Name)
		}

		err := r.reconcileDCStatefulSet(ctx, cc, dc, restartChecksum)
		if err == errRollStarted {
			rollStarted = true
		} else if err != nil {
			return false, errors.Wrapf(err, "failed to reconcile dc %q", dc.Name)
		}
	}

	return rollStarted, nil
}

// retryAfterCassandraError reports whether an error from reconcileCassandra only means "try again after the
// retry delay" (a missing TLS secret, a roll that just started) and should not fail the reconcile.
func retryAfterCassandraError(err error) bool {
	switch errors.Cause(err) {
	case errTLSSecretNotFound, errTLSSecretInvalid, errRollStarted:
		return true
	}

	return false
}
