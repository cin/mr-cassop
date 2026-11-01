package controllers

import (
	"context"
	"fmt"
	"slices"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
// whether repairs are recorded as paused, which includes ones an earlier attempt paused: the caller
// must then not let this reconcile pass go on to resume them.
func (r *CassandraClusterReconciler) pauseRepairsBeforeRoll(
	ctx context.Context, cc *dbv1alpha1.CassandraCluster,
) bool {
	r.pauseRunningRepairs(ctx, cc)

	return !cc.Status.PausedRepairs.IsEmpty()
}

func (r *CassandraClusterReconciler) pauseRunningRepairs(ctx context.Context, cc *dbv1alpha1.CassandraCluster) {
	reaperClient, err := r.reaperClientForCluster(ctx, cc)
	if err != nil {
		r.Log.Warnf("Can't pause repairs before the rolling update: %s", err)
		return
	}

	toPause, err := listRepairsToPause(ctx, reaperClient, pauseOtherSchedules(cc))
	if err != nil {
		r.Events.Warning(cc, events.EventRepairsPauseFailed,
			fmt.Sprintf("Can't list the repairs to pause before the rolling update: %s", err))
		return
	}
	if !toPause.IsEmpty() {
		r.recordAndPauseRepairs(ctx, cc, reaperClient, toPause)
	}
}

func (r *CassandraClusterReconciler) recordAndPauseRepairs(
	ctx context.Context, cc *dbv1alpha1.CassandraCluster,
	reaperClient reaper.ReaperClient, toPause dbv1alpha1.PausedRepairs,
) {
	existing := cc.Status.PausedRepairs
	if err := r.recordPausedRepairs(ctx, cc, mergePausedRepairs(existing, toPause)); err != nil {
		r.Events.Warning(cc, events.EventRepairsPauseFailed,
			fmt.Sprintf("Can't record the repairs to pause, leaving them running: %s", err))
		return
	}

	paused, err := pauseRepairs(ctx, reaperClient, toPause)
	if err != nil {
		r.trimPausedRepairs(ctx, cc, existing, paused, err)
	}
	if !paused.IsEmpty() {
		r.Events.Normal(cc, events.EventRepairsPaused, fmt.Sprintf(
			"Paused %d repair run(s) and %d repair schedule(s) for the rolling update",
			len(paused.RunIDs), len(paused.ScheduleIDs)))
	}
}

// trimPausedRepairs reports that some repairs could not be paused and cuts the record down to what is paused, so
// the resume neither waits for nor reports on the repairs that kept running.
func (r *CassandraClusterReconciler) trimPausedRepairs(
	ctx context.Context, cc *dbv1alpha1.CassandraCluster, existing, paused dbv1alpha1.PausedRepairs, pauseErr error,
) {
	r.Events.Warning(cc, events.EventRepairsPauseFailed,
		fmt.Sprintf("Some repairs could not be paused before the rolling update: %s", pauseErr))
	if err := r.recordPausedRepairs(ctx, cc, mergePausedRepairs(existing, paused)); err != nil {
		r.Log.Warnf("Can't cut the paused repairs record down to what is paused: %s", err)
	}
}

func listRepairsToPause(ctx context.Context, reaperClient reaper.ReaperClient, includeOtherSchedules bool) (dbv1alpha1.PausedRepairs, error) {
	runs, err := reaperClient.RepairRuns(ctx)
	if err != nil {
		return dbv1alpha1.PausedRepairs{}, errors.Wrap(err, "can't list repair runs")
	}
	schedules, err := reaperClient.RepairSchedules(ctx)
	if err != nil {
		return dbv1alpha1.PausedRepairs{}, errors.Wrap(err, "can't list repair schedules")
	}

	return selectRepairsToPause(runs, schedules, includeOtherSchedules), nil
}

// selectRepairsToPause picks what is running or active. Runs and schedules a user already paused are
// not picked, so they are never resumed by the operator. Schedules owned by someone else are picked only
// when includeOtherSchedules is set.
func selectRepairsToPause(
	runs []reaper.RepairRun, schedules []reaper.RepairSchedule, includeOtherSchedules bool,
) dbv1alpha1.PausedRepairs {
	toPause := dbv1alpha1.PausedRepairs{}
	for _, run := range runs {
		if run.State == reaper.RepairStateRunning {
			toPause.RunIDs = append(toPause.RunIDs, run.ID)
		}
	}
	for _, schedule := range schedules {
		ours := schedule.Owner == reaper.OwnerCassandraOperator
		if schedule.State == reaper.RepairScheduleStateActive && (ours || includeOtherSchedules) {
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

// pauseRepairs pauses everything it can. It returns what it did pause and the first error.
func pauseRepairs(
	ctx context.Context, reaperClient reaper.ReaperClient, toPause dbv1alpha1.PausedRepairs,
) (dbv1alpha1.PausedRepairs, error) {
	var firstErr error
	paused := dbv1alpha1.PausedRepairs{}
	for _, runID := range toPause.RunIDs {
		if err := reaperClient.PauseRepairRun(ctx, runID); err != nil {
			firstErr = firstError(firstErr, errors.Wrapf(err, "can't pause repair run %s", runID))
			continue
		}
		paused.RunIDs = append(paused.RunIDs, runID)
	}
	for _, scheduleID := range toPause.ScheduleIDs {
		if err := reaperClient.SetRepairScheduleState(ctx, scheduleID, false); err != nil {
			firstErr = firstError(firstErr, errors.Wrapf(err, "can't pause repair schedule %s", scheduleID))
			continue
		}
		paused.ScheduleIDs = append(paused.ScheduleIDs, scheduleID)
	}

	return paused, firstErr
}

func firstError(first, next error) error {
	if first != nil {
		return first
	}

	return next
}

// recordPausedRepairs stores what the operator paused in the CR status and updates cc to match. It patches only
// the status, so there is no read of a possibly stale cache and no resource version to conflict on. It patches a
// copy, because the response overwrites the object, and cc carries the defaults applied to the spec.
func (r *CassandraClusterReconciler) recordPausedRepairs(
	ctx context.Context, cc *dbv1alpha1.CassandraCluster, paused dbv1alpha1.PausedRepairs,
) error {
	updated := cc.DeepCopy()
	updated.Status.PausedRepairs = paused
	if err := r.Status().Patch(ctx, updated, client.MergeFrom(cc)); err != nil {
		return errors.Wrap(err, "can't update the paused repairs in the status")
	}

	cc.Status.PausedRepairs = paused
	cc.ResourceVersion = updated.ResourceVersion

	return nil
}

// resumeRepairsAfterRoll resumes what pauseRepairsBeforeRoll paused once every statefulset has
// finished rolling. While a roll is still in progress it asks for a requeue, which also keeps the
// reconcile from starting new repairs in the gap between two pods. A resume that fails does not
// fail the reconcile.
func (r *CassandraClusterReconciler) resumeRepairsAfterRoll(
	ctx context.Context, cc *dbv1alpha1.CassandraCluster, reaperClient reaper.ReaperClient,
) (ctrl.Result, error) {
	paused := cc.Status.PausedRepairs
	if paused.IsEmpty() {
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

	// Best effort, like the pause: the record stays, an event says what failed, and the next reconcile tries again.
	if err = r.resumeAndForget(ctx, cc, reaperClient, paused); err != nil {
		r.Log.Warnf("%s. Trying again on the next reconcile", err)
	}

	return ctrl.Result{}, nil
}

// resumeAndForget resumes the recorded repairs, then clears the record. The record stays if resuming fails.
func (r *CassandraClusterReconciler) resumeAndForget(
	ctx context.Context, cc *dbv1alpha1.CassandraCluster,
	reaperClient reaper.ReaperClient, paused dbv1alpha1.PausedRepairs,
) error {
	runsResumed, schedulesResumed, err := resumeRepairs(ctx, reaperClient, paused)
	if err != nil {
		r.Events.Warning(cc, events.EventRepairsResumeFailed,
			fmt.Sprintf("Can't resume the repairs paused for the rolling update, will try again: %s", err))
		return errors.Wrap(err, "can't resume the repairs paused for the rolling update")
	}
	if err = r.recordPausedRepairs(ctx, cc, dbv1alpha1.PausedRepairs{}); err != nil {
		return err
	}
	r.Events.Normal(cc, events.EventRepairsResumed, fmt.Sprintf(
		"Resumed %d of %d repair run(s) and %d of %d repair schedule(s) paused for the rolling update; "+
			"the others were resumed or deleted since",
		runsResumed, len(paused.RunIDs), schedulesResumed, len(paused.ScheduleIDs)))

	return nil
}

// resumeRepairs resumes the recorded runs and schedules that are still paused and returns how many of each it
// resumed. One that is gone, or that is in another state now, was dealt with by someone else (or by a Reaper
// re-initialization) and is left alone. A failure on one does not keep the others paused: it goes on and returns
// the first error, and the record stays so the next reconcile tries again.
func resumeRepairs(
	ctx context.Context, reaperClient reaper.ReaperClient, paused dbv1alpha1.PausedRepairs,
) (int, int, error) {
	runsResumed, runsErr := resumeRuns(ctx, reaperClient, paused.RunIDs)
	schedulesResumed, schedulesErr := resumeSchedules(ctx, reaperClient, paused.ScheduleIDs)

	return runsResumed, schedulesResumed, firstError(runsErr, schedulesErr)
}

func resumeRuns(ctx context.Context, reaperClient reaper.ReaperClient, runIDs []string) (int, error) {
	runs, err := reaperClient.RepairRuns(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "can't list repair runs")
	}
	resumed := 0
	var firstErr error
	for _, run := range runs {
		if run.State != reaper.RepairStatePaused || !slices.Contains(runIDs, run.ID) {
			continue
		}
		if err = reaperClient.ResumeRepairRun(ctx, run.ID); err != nil {
			firstErr = firstError(firstErr, errors.Wrapf(err, "can't resume repair run %s", run.ID))
			continue
		}
		resumed++
	}

	return resumed, firstErr
}

func resumeSchedules(ctx context.Context, reaperClient reaper.ReaperClient, scheduleIDs []string) (int, error) {
	schedules, err := reaperClient.RepairSchedules(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "can't list repair schedules")
	}
	resumed := 0
	var firstErr error
	for _, schedule := range schedules {
		if schedule.State != reaper.RepairScheduleStatePaused || !slices.Contains(scheduleIDs, schedule.ID) {
			continue
		}
		if err = reaperClient.SetRepairScheduleState(ctx, schedule.ID, true); err != nil {
			firstErr = firstError(firstErr, errors.Wrapf(err, "can't resume repair schedule %s", schedule.ID))
			continue
		}
		resumed++
	}

	return resumed, firstErr
}

func (r *CassandraClusterReconciler) statefulSetsRolled(
	ctx context.Context, cc *dbv1alpha1.CassandraCluster,
) (bool, error) {
	for _, dc := range cc.Spec.DCs {
		sts := &appsv1.StatefulSet{}
		key := types.NamespacedName{Name: names.DC(cc.Name, dc.Name), Namespace: cc.Namespace}
		err := r.Get(ctx, key, sts)
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

// pauseOtherSchedules reports whether schedules not owned by the operator are paused too. It is on unless
// the user turned it off.
func pauseOtherSchedules(cc *dbv1alpha1.CassandraCluster) bool {
	return cc.Spec.Reaper.PauseOtherSchedulesDuringRoll == nil || *cc.Spec.Reaper.PauseOtherSchedulesDuringRoll
}
