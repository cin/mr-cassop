package controllers

import (
	"context"
	"fmt"
	"slices"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/reaper"
)

// errRollStarted is returned right after repairs were paused and a statefulset update started
// rolling the pods. The reconcile stops there and runs again after the retry delay: a read of the
// statefulset straight after the update can still show the pre-update state, which looks like a
// finished roll and would resume the repairs at once.
var errRollStarted = errors.New("rolling update started and repairs were paused")

// pauseRepairsBeforeRoll pauses the running Reaper repair runs and the active repair schedules
// before an update of the Cassandra statefulset restarts the pods (#227). It records what it pauses
// in the CR status first, so a crash in between can't leave repairs paused with no record of them.
// It is best effort: a Reaper problem is reported as an event and never blocks the roll. It reports
// whether it paused anything.
func (r *CassandraClusterReconciler) pauseRepairsBeforeRoll(ctx context.Context, cc *dbv1alpha1.CassandraCluster) bool {
	reaperClient, err := r.reaperClientForCluster(ctx, cc)
	if err != nil {
		r.Log.Warnf("Can't pause repairs before the rolling update: %s", err)
		return false
	}

	toPause, err := listRepairsToPause(ctx, reaperClient)
	if err != nil {
		r.Events.Warning(cc, events.EventRepairsPauseFailed, fmt.Sprintf("Can't list the repairs to pause before the rolling update: %s", err))
		return false
	}
	if len(toPause.RunIDs) == 0 && len(toPause.ScheduleIDs) == 0 {
		return false
	}

	return r.recordAndPauseRepairs(ctx, cc, reaperClient, toPause)
}

func (r *CassandraClusterReconciler) recordAndPauseRepairs(ctx context.Context, cc *dbv1alpha1.CassandraCluster, reaperClient reaper.ReaperClient, toPause dbv1alpha1.PausedRepairs) bool {
	if err := r.recordPausedRepairs(ctx, cc, mergePausedRepairs(cc.Status.PausedRepairs, toPause)); err != nil {
		r.Events.Warning(cc, events.EventRepairsPauseFailed, fmt.Sprintf("Can't record the repairs to pause, leaving them running: %s", err))
		return false
	}

	if err := pauseRepairs(ctx, reaperClient, toPause); err != nil {
		r.Events.Warning(cc, events.EventRepairsPauseFailed, fmt.Sprintf("Some repairs could not be paused before the rolling update: %s", err))
	}
	r.Events.Normal(cc, events.EventRepairsPaused, fmt.Sprintf("Paused %d repair run(s) and %d repair schedule(s) for the rolling update", len(toPause.RunIDs), len(toPause.ScheduleIDs)))

	return true
}

func listRepairsToPause(ctx context.Context, reaperClient reaper.ReaperClient) (dbv1alpha1.PausedRepairs, error) {
	runs, err := reaperClient.RepairRuns(ctx)
	if err != nil {
		return dbv1alpha1.PausedRepairs{}, errors.Wrap(err, "can't list repair runs")
	}
	schedules, err := reaperClient.RepairSchedules(ctx)
	if err != nil {
		return dbv1alpha1.PausedRepairs{}, errors.Wrap(err, "can't list repair schedules")
	}

	return selectRepairsToPause(runs, schedules), nil
}

// selectRepairsToPause picks what is running or active. Runs and schedules a user already paused are
// not picked, so they are never resumed by the operator.
func selectRepairsToPause(runs []reaper.RepairRun, schedules []reaper.RepairSchedule) dbv1alpha1.PausedRepairs {
	toPause := dbv1alpha1.PausedRepairs{}
	for _, run := range runs {
		if run.State == reaper.RepairStateRunning {
			toPause.RunIDs = append(toPause.RunIDs, run.ID)
		}
	}
	for _, schedule := range schedules {
		if schedule.State == reaper.RepairScheduleStateActive {
			toPause.ScheduleIDs = append(toPause.ScheduleIDs, schedule.ID)
		}
	}

	return toPause
}

func mergePausedRepairs(existing, added dbv1alpha1.PausedRepairs) dbv1alpha1.PausedRepairs {
	return dbv1alpha1.PausedRepairs{
		RunIDs:      appendMissing(existing.RunIDs, added.RunIDs),
		ScheduleIDs: appendMissing(existing.ScheduleIDs, added.ScheduleIDs),
	}
}

func appendMissing(existing, added []string) []string {
	merged := slices.Clone(existing)
	for _, id := range added {
		if !slices.Contains(merged, id) {
			merged = append(merged, id)
		}
	}

	return merged
}

// pauseRepairs pauses everything it can and returns the first error
func pauseRepairs(ctx context.Context, reaperClient reaper.ReaperClient, toPause dbv1alpha1.PausedRepairs) error {
	var firstErr error
	for _, runID := range toPause.RunIDs {
		if err := reaperClient.PauseRepairRun(ctx, runID); err != nil && firstErr == nil {
			firstErr = errors.Wrapf(err, "can't pause repair run %s", runID)
		}
	}
	for _, scheduleID := range toPause.ScheduleIDs {
		if err := reaperClient.SetRepairScheduleState(ctx, scheduleID, false); err != nil && firstErr == nil {
			firstErr = errors.Wrapf(err, "can't pause repair schedule %s", scheduleID)
		}
	}

	return firstErr
}

// recordPausedRepairs stores what the operator paused in the CR status and updates cc to match. It
// updates the latest version of the CR, retrying on conflict, because cc is a snapshot from the
// start of the reconcile.
func (r *CassandraClusterReconciler) recordPausedRepairs(ctx context.Context, cc *dbv1alpha1.CassandraCluster, paused dbv1alpha1.PausedRepairs) error {
	latest := &dbv1alpha1.CassandraCluster{}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: cc.Name, Namespace: cc.Namespace}, latest); err != nil {
			return err
		}
		latest.Status.PausedRepairs = paused
		return r.Status().Update(ctx, latest)
	})
	if err != nil {
		return errors.Wrap(err, "can't update the paused repairs in the status")
	}

	cc.Status.PausedRepairs = paused
	cc.ResourceVersion = latest.ResourceVersion

	return nil
}

// resumeRepairsAfterRoll resumes what pauseRepairsBeforeRoll paused once every statefulset has
// finished rolling. While a roll is still in progress it asks for a requeue, which also keeps the
// reconcile from starting new repairs in the gap between two pods.
func (r *CassandraClusterReconciler) resumeRepairsAfterRoll(ctx context.Context, cc *dbv1alpha1.CassandraCluster, reaperClient reaper.ReaperClient) (ctrl.Result, error) {
	paused := cc.Status.PausedRepairs
	if len(paused.RunIDs) == 0 && len(paused.ScheduleIDs) == 0 {
		return ctrl.Result{}, nil
	}

	rolled, err := r.statefulSetsRolled(ctx, cc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !rolled {
		r.Log.Infof("Rolling update in progress, repairs stay paused. Trying again in %s...", r.Cfg.RetryDelay)
		return ctrl.Result{RequeueAfter: r.Cfg.RetryDelay}, nil
	}

	if err = resumeRepairs(ctx, reaperClient, paused); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "can't resume the repairs paused for the rolling update")
	}
	if err = r.recordPausedRepairs(ctx, cc, dbv1alpha1.PausedRepairs{}); err != nil {
		return ctrl.Result{}, err
	}
	r.Events.Normal(cc, events.EventRepairsResumed, "Resumed the repairs paused for the rolling update")

	return ctrl.Result{}, nil
}

// resumeRepairs resumes the recorded runs and schedules that are still paused. One that is gone, or
// that is in another state now, was dealt with by someone else and is left alone.
func resumeRepairs(ctx context.Context, reaperClient reaper.ReaperClient, paused dbv1alpha1.PausedRepairs) error {
	runs, err := reaperClient.RepairRuns(ctx)
	if err != nil {
		return errors.Wrap(err, "can't list repair runs")
	}
	for _, run := range runs {
		if run.State == reaper.RepairStatePaused && slices.Contains(paused.RunIDs, run.ID) {
			if err = reaperClient.ResumeRepairRun(ctx, run.ID); err != nil {
				return errors.Wrapf(err, "can't resume repair run %s", run.ID)
			}
		}
	}

	return resumeSchedules(ctx, reaperClient, paused.ScheduleIDs)
}

func resumeSchedules(ctx context.Context, reaperClient reaper.ReaperClient, scheduleIDs []string) error {
	schedules, err := reaperClient.RepairSchedules(ctx)
	if err != nil {
		return errors.Wrap(err, "can't list repair schedules")
	}
	for _, schedule := range schedules {
		if schedule.State == reaper.RepairScheduleStatePaused && slices.Contains(scheduleIDs, schedule.ID) {
			if err = reaperClient.SetRepairScheduleState(ctx, schedule.ID, true); err != nil {
				return errors.Wrapf(err, "can't resume repair schedule %s", schedule.ID)
			}
		}
	}

	return nil
}

func (r *CassandraClusterReconciler) statefulSetsRolled(ctx context.Context, cc *dbv1alpha1.CassandraCluster) (bool, error) {
	for _, dc := range cc.Spec.DCs {
		sts := &appsv1.StatefulSet{}
		err := r.Get(ctx, types.NamespacedName{Name: names.DC(cc.Name, dc.Name), Namespace: cc.Namespace}, sts)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, errors.Wrapf(err, "can't get the statefulset of dc %q", dc.Name)
		}
		if !statefulSetRolled(sts) {
			return false, nil
		}
	}

	return true, nil
}

// statefulSetRolled reports whether the statefulset controller has seen the latest spec and every
// pod is on the latest revision and ready, i.e. no rolling update is under way or about to start.
func statefulSetRolled(sts *appsv1.StatefulSet) bool {
	replicas := ptr.Deref(sts.Spec.Replicas, 1)

	return sts.Status.ObservedGeneration >= sts.Generation &&
		sts.Status.CurrentRevision == sts.Status.UpdateRevision &&
		sts.Status.UpdatedReplicas == replicas &&
		sts.Status.ReadyReplicas == replicas
}
