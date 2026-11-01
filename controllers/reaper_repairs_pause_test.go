package controllers

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/reaper"
)

const testRetryDelay = 10 * time.Second

func pausedRepairs(runIDs, scheduleIDs []string) dbv1alpha1.PausedRepairs {
	return dbv1alpha1.PausedRepairs{RunIDs: runIDs, ScheduleIDs: scheduleIDs}
}

// newPauseTestReconciler returns a reconciler with a fake client holding cc, the Reaper credentials and
// any other objects, and the mocked Reaper client every reconcile helper talks to.
func newPauseTestReconciler(
	t *testing.T, cc *dbv1alpha1.CassandraCluster, objects ...client.Object,
) (*CassandraClusterReconciler, mockedClients) {
	t.Helper()

	reconciler, _, mocked := createMockedReconciler(t)
	reconciler.Cfg.RetryDelay = testRetryDelay
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: names.AdminAuthConfigSecret(cc.Name), Namespace: cc.Namespace},
		Data: map[string][]byte{
			dbv1alpha1.CassandraOperatorAdminRole:     []byte("admin"),
			dbv1alpha1.CassandraOperatorAdminPassword: []byte("password"),
		},
	}
	reconciler.Client = fake.NewClientBuilder().
		WithScheme(baseScheme).
		WithObjects(append([]client.Object{cc.DeepCopy(), secret}, objects...)...).
		WithStatusSubresource(&dbv1alpha1.CassandraCluster{}).
		Build()

	return reconciler, mocked
}

func storedPausedRepairs(
	t *testing.T, reconciler *CassandraClusterReconciler, cc *dbv1alpha1.CassandraCluster,
) dbv1alpha1.PausedRepairs {
	t.Helper()
	stored := &dbv1alpha1.CassandraCluster{}
	err := reconciler.Get(context.Background(), types.NamespacedName{Name: cc.Name, Namespace: cc.Namespace}, stored)
	NewWithT(t).Expect(err).NotTo(HaveOccurred())

	return stored.Status.PausedRepairs
}

func pauseTestCluster() *dbv1alpha1.CassandraCluster {
	cc := baseCC.DeepCopy()
	initializeReconciler(cc) // applies the CR defaults
	cc.Spec.Reaper.PauseRepairsDuringRoll = true

	return cc
}

func TestSelectRepairsToPause(t *testing.T) {
	asserts := NewWithT(t)

	runs := []reaper.RepairRun{
		{ID: "running", State: reaper.RepairStateRunning},
		{ID: "paused-by-user", State: reaper.RepairStatePaused},
		{ID: "not-started", State: "NOT_STARTED"},
		{ID: "done", State: "DONE"},
		{ID: "also-running", State: reaper.RepairStateRunning},
	}
	schedules := []reaper.RepairSchedule{
		{ID: "active", State: reaper.RepairScheduleStateActive},
		{ID: "paused-by-user", State: reaper.RepairScheduleStatePaused},
	}

	expected := pausedRepairs([]string{"running", "also-running"}, []string{"active"})
	asserts.Expect(selectRepairsToPause(runs, schedules)).To(Equal(expected))
	asserts.Expect(selectRepairsToPause(nil, nil)).To(Equal(dbv1alpha1.PausedRepairs{}))
}

func TestMergePausedRepairs(t *testing.T) {
	asserts := NewWithT(t)

	existing := pausedRepairs([]string{"a", "b"}, []string{"s1"})
	added := pausedRepairs([]string{"b", "c"}, []string{"s1", "s2"})
	merged := mergePausedRepairs(existing, added)

	asserts.Expect(merged).To(Equal(pausedRepairs([]string{"a", "b", "c"}, []string{"s1", "s2"})))
}

func TestStatefulSetRolled(t *testing.T) {
	rolled := func() *appsv1.StatefulSet {
		return &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Generation: 4},
			Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To[int32](3)},
			Status: appsv1.StatefulSetStatus{
				ObservedGeneration: 4, CurrentRevision: "rev-2", UpdateRevision: "rev-2", UpdatedReplicas: 3, ReadyReplicas: 3,
			},
		}
	}
	tests := []struct {
		name     string
		change   func(sts *appsv1.StatefulSet)
		expected bool
	}{
		{name: "nothing to roll", change: func(sts *appsv1.StatefulSet) {}, expected: true},
		{
			name:   "spec not observed yet",
			change: func(sts *appsv1.StatefulSet) { sts.Status.ObservedGeneration = 3 },
		},
		{
			name:   "new revision not current yet",
			change: func(sts *appsv1.StatefulSet) { sts.Status.CurrentRevision = "rev-1" },
		},
		{
			name:   "some pods not updated",
			change: func(sts *appsv1.StatefulSet) { sts.Status.UpdatedReplicas = 2 },
		},
		{name: "a pod is not ready", change: func(sts *appsv1.StatefulSet) { sts.Status.ReadyReplicas = 2 }, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sts := rolled()
			tc.change(sts)

			NewWithT(t).Expect(statefulSetRolled(sts)).To(Equal(tc.expected))
		})
	}
}

func TestPauseRepairsBeforeRoll(t *testing.T) {
	runs := []reaper.RepairRun{
		{ID: "run-a", State: reaper.RepairStateRunning},
		{ID: "run-user", State: reaper.RepairStatePaused},
		{ID: "run-done", State: "DONE"},
	}
	schedules := []reaper.RepairSchedule{
		{ID: "sched-1", State: reaper.RepairScheduleStateActive},
		{ID: "sched-user", State: reaper.RepairScheduleStatePaused},
	}

	t.Run("pauses what is running and records it before pausing", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules, nil)
		mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-a").DoAndReturn(func(ctx context.Context, id string) error {
			expected := pausedRepairs([]string{"run-a"}, []string{"sched-1"})
			asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(expected))
			return nil
		})
		mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", false).Return(nil)

		paused := reconciler.pauseRepairsBeforeRoll(context.Background(), cc)

		asserts.Expect(paused).To(BeTrue())
		asserts.Expect(cc.Status.PausedRepairs).To(Equal(pausedRepairs([]string{"run-a"}, []string{"sched-1"})))
	})

	t.Run("keeps what an earlier roll paused", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		cc.Status.PausedRepairs = pausedRepairs([]string{"run-earlier"}, nil)
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(nil, nil)
		mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-a").Return(nil)

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeTrue())

		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(pausedRepairs([]string{"run-earlier", "run-a"}, nil)))
	})

	t.Run("records only what it paused when a pause call fails", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules, nil)
		mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-a").Return(errors.New("reaper is down"))
		mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", false).Return(nil)

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeTrue())

		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(pausedRepairs(nil, []string{"sched-1"})),
			"the run that kept running is not recorded, so the resume does not wait for it")
		asserts.Expect(cc.Status.PausedRepairs).To(Equal(pausedRepairs(nil, []string{"sched-1"})))
	})

	t.Run("keeps what an earlier roll paused when every pause call fails", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		cc.Status.PausedRepairs = pausedRepairs([]string{"run-earlier"}, nil)
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules, nil)
		mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-a").Return(errors.New("reaper is down"))
		mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", false).Return(errors.New("reaper is down"))

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeTrue())
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(pausedRepairs([]string{"run-earlier"}, nil)))
	})

	t.Run("asks for no stop when every pause call fails and nothing is recorded", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules, nil)
		mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-a").Return(errors.New("reaper is down"))
		mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", false).Return(errors.New("reaper is down"))

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeFalse())
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(dbv1alpha1.PausedRepairs{}))
	})

	t.Run("does nothing when nothing is running", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs[1:], nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules[1:], nil)

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeFalse())
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(dbv1alpha1.PausedRepairs{}))
	})

	t.Run("still reports repairs as paused when an earlier attempt recorded them and nothing is new", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		cc.Status.PausedRepairs = pausedRepairs([]string{"run-earlier"}, nil)
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return([]reaper.RepairRun{
			{ID: "run-earlier", State: reaper.RepairStatePaused},
		}, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(nil, nil)

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeTrue(),
			"the reconcile must stop after the statefulset update or it could resume them at once")
	})

	t.Run("reports repairs as paused when reaper can't be listed but a record exists", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		cc.Status.PausedRepairs = pausedRepairs([]string{"run-earlier"}, nil)
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(nil, errors.New("reaper is down"))

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeTrue())
	})

	t.Run("does not block the roll when reaper can't be listed", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		reconciler, mocked := newPauseTestReconciler(t, cc)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(nil, errors.New("reaper is down"))

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeFalse())
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(dbv1alpha1.PausedRepairs{}))
	})

	t.Run("does not block the roll without reaper credentials", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		reconciler, _ := newPauseTestReconciler(t, cc)
		asserts.Expect(reconciler.Delete(context.Background(), &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: names.AdminAuthConfigSecret(cc.Name), Namespace: cc.Namespace},
		})).To(Succeed())

		asserts.Expect(reconciler.pauseRepairsBeforeRoll(context.Background(), cc)).To(BeFalse())
	})
}

func rolledStatefulSet(cc *dbv1alpha1.CassandraCluster, dc dbv1alpha1.DC, rolled bool) *appsv1.StatefulSet {
	replicas := *dc.Replicas
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: names.DC(cc.Name, dc.Name), Namespace: cc.Namespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		Status: appsv1.StatefulSetStatus{
			CurrentRevision: "rev-1", UpdateRevision: "rev-1", UpdatedReplicas: replicas, ReadyReplicas: replicas,
		},
	}
	if !rolled {
		sts.Status.UpdateRevision = "rev-2"
		sts.Status.UpdatedReplicas = 1
	}

	return sts
}

func TestResumeRepairsAfterRoll(t *testing.T) {
	recorded := pausedRepairs([]string{"run-a", "run-resumed-already"}, []string{"sched-1"})
	runs := []reaper.RepairRun{
		{ID: "run-a", State: reaper.RepairStatePaused},
		{ID: "run-user", State: reaper.RepairStatePaused},
		{ID: "run-resumed-already", State: reaper.RepairStateRunning},
	}
	schedules := []reaper.RepairSchedule{
		{ID: "sched-1", State: reaper.RepairScheduleStatePaused},
		{ID: "sched-user", State: reaper.RepairScheduleStatePaused},
	}

	newReconciler := func(
		t *testing.T, rolled bool,
	) (*CassandraClusterReconciler, mockedClients, *dbv1alpha1.CassandraCluster) {
		cc := pauseTestCluster()
		cc.Status.PausedRepairs = recorded
		objects := []client.Object{}
		for _, dc := range cc.Spec.DCs {
			objects = append(objects, rolledStatefulSet(cc, dc, rolled))
		}
		reconciler, mocked := newPauseTestReconciler(t, cc, objects...)
		// the stored CR must hold the record too: the fake client keeps the status of the copy it was given
		asserts := NewWithT(t)
		asserts.Expect(reconciler.recordPausedRepairs(context.Background(), cc, recorded)).To(Succeed())

		return reconciler, mocked, cc
	}

	t.Run("resumes only what it paused and is still paused", func(t *testing.T) {
		asserts := NewWithT(t)
		reconciler, mocked, cc := newReconciler(t, true)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules, nil)
		mocked.reaper.EXPECT().ResumeRepairRun(gomock.Any(), "run-a").Return(nil)
		mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", true).Return(nil)

		result, err := reconciler.resumeRepairsAfterRoll(context.Background(), cc, mocked.reaper)

		asserts.Expect(err).NotTo(HaveOccurred())
		asserts.Expect(result.RequeueAfter).To(BeZero())
		asserts.Expect(cc.Status.PausedRepairs).To(Equal(dbv1alpha1.PausedRepairs{}))
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(dbv1alpha1.PausedRepairs{}))
	})

	t.Run("waits while a statefulset is still rolling", func(t *testing.T) {
		asserts := NewWithT(t)
		reconciler, mocked, cc := newReconciler(t, false)

		result, err := reconciler.resumeRepairsAfterRoll(context.Background(), cc, mocked.reaper)

		asserts.Expect(err).NotTo(HaveOccurred())
		asserts.Expect(result.RequeueAfter).To(Equal(testRetryDelay))
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(recorded))
	})

	t.Run("does nothing when it paused nothing", func(t *testing.T) {
		asserts := NewWithT(t)
		cc := pauseTestCluster()
		reconciler, mocked := newPauseTestReconciler(t, cc)

		result, err := reconciler.resumeRepairsAfterRoll(context.Background(), cc, mocked.reaper)

		asserts.Expect(err).NotTo(HaveOccurred())
		asserts.Expect(result.RequeueAfter).To(BeZero())
	})

	t.Run("keeps the record but not fail the reconcile when resuming fails, and resumes the schedule", func(t *testing.T) {
		asserts := NewWithT(t)
		reconciler, mocked, cc := newReconciler(t, true)
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules, nil)
		mocked.reaper.EXPECT().ResumeRepairRun(gomock.Any(), "run-a").Return(errors.New("reaper is down"))
		mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", true).Return(nil)

		_, err := reconciler.resumeRepairsAfterRoll(context.Background(), cc, mocked.reaper)

		asserts.Expect(err).NotTo(HaveOccurred(), "a failed resume is reported, not returned")
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(recorded))
	})

	t.Run("resumes even when the option was switched off meanwhile", func(t *testing.T) {
		asserts := NewWithT(t)
		reconciler, mocked, cc := newReconciler(t, true)
		cc.Spec.Reaper.PauseRepairsDuringRoll = false
		mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(runs, nil)
		mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(schedules, nil)
		mocked.reaper.EXPECT().ResumeRepairRun(gomock.Any(), "run-a").Return(nil)
		mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", true).Return(nil)

		_, err := reconciler.resumeRepairsAfterRoll(context.Background(), cc, mocked.reaper)

		asserts.Expect(err).NotTo(HaveOccurred())
		asserts.Expect(storedPausedRepairs(t, reconciler, cc)).To(Equal(dbv1alpha1.PausedRepairs{}))
	})
}

func TestUpdateStatefulSetPausesRepairsForARoll(t *testing.T) {
	newCluster := func(pauseRepairs bool) *dbv1alpha1.CassandraCluster {
		cc := pauseTestCluster()
		cc.Spec.Reaper.PauseRepairsDuringRoll = pauseRepairs

		return cc
	}
	actualStatefulSet := func(cc *dbv1alpha1.CassandraCluster) *appsv1.StatefulSet {
		return cassandraStatefulSet(cc, cc.Spec.DCs[0], checksumContainer{}, &v1.Secret{})
	}
	changedTemplate := func(cc *dbv1alpha1.CassandraCluster) *appsv1.StatefulSet {
		changed := cc.DeepCopy()
		changed.Spec.Cassandra.LogLevel = "debug"

		return cassandraStatefulSet(changed, changed.Spec.DCs[0], checksumContainer{}, &v1.Secret{})
	}
	changedLabelsOnly := func(cc *dbv1alpha1.CassandraCluster) *appsv1.StatefulSet {
		sts := actualStatefulSet(cc)
		sts.Labels = map[string]string{"extra": "label"}

		return sts
	}

	tests := []struct {
		name         string
		pauseRepairs bool
		desired      func(cc *dbv1alpha1.CassandraCluster) *appsv1.StatefulSet
		expectPause  bool
	}{
		{name: "pod template changed", pauseRepairs: true, desired: changedTemplate, expectPause: true},
		{name: "option is off", pauseRepairs: false, desired: changedTemplate, expectPause: false},
		{name: "pods are not restarted", pauseRepairs: true, desired: changedLabelsOnly, expectPause: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			asserts := NewWithT(t)
			cc := newCluster(tc.pauseRepairs)
			sts := actualStatefulSet(cc)
			sts.Namespace = cc.Namespace
			reconciler, mocked := newPauseTestReconciler(t, cc, sts)
			if tc.expectPause {
				running := []reaper.RepairRun{{ID: "run-a", State: reaper.RepairStateRunning}}
				mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return(running, nil)
				mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return(nil, nil)
				mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-a").Return(nil)
			}
			actual := &appsv1.StatefulSet{}
			asserts.Expect(reconciler.Get(context.Background(), client.ObjectKeyFromObject(sts), actual)).To(Succeed())
			desired := tc.desired(cc)

			paused, err := reconciler.updateStatefulSet(context.Background(), cc, actual, desired)

			asserts.Expect(err).NotTo(HaveOccurred())
			asserts.Expect(paused).To(Equal(tc.expectPause))
			updated := &appsv1.StatefulSet{}
			asserts.Expect(reconciler.Get(context.Background(), client.ObjectKeyFromObject(sts), updated)).To(Succeed())
			asserts.Expect(updated.Labels).To(Equal(desired.Labels),
				"the statefulset is updated whether or not repairs were paused")
		})
	}
}

func TestResumeRepairsCountsWhatItResumed(t *testing.T) {
	asserts := NewWithT(t)
	_, _, mocked := createMockedReconciler(t)
	recorded := pausedRepairs(
		[]string{"run-a", "run-deleted", "run-resumed-already"}, []string{"sched-1", "sched-deleted"},
	)
	mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return([]reaper.RepairRun{
		{ID: "run-a", State: reaper.RepairStatePaused},
		{ID: "run-resumed-already", State: reaper.RepairStateRunning},
		{ID: "run-user", State: reaper.RepairStatePaused},
	}, nil)
	mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return([]reaper.RepairSchedule{
		{ID: "sched-1", State: reaper.RepairScheduleStatePaused},
		{ID: "sched-user", State: reaper.RepairScheduleStatePaused},
	}, nil)
	mocked.reaper.EXPECT().ResumeRepairRun(gomock.Any(), "run-a").Return(nil)
	mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", true).Return(nil)

	runs, schedules, err := resumeRepairs(context.Background(), mocked.reaper, recorded)

	asserts.Expect(err).NotTo(HaveOccurred())
	asserts.Expect(runs).To(Equal(1))
	asserts.Expect(schedules).To(Equal(1))
}

func TestPauseRepairsReportsWhatItPaused(t *testing.T) {
	asserts := NewWithT(t)
	_, _, mocked := createMockedReconciler(t)
	mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-a").Return(errors.New("reaper is down"))
	mocked.reaper.EXPECT().PauseRepairRun(gomock.Any(), "run-b").Return(nil)
	mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", false).Return(errors.New("reaper is down"))
	mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-2", false).Return(nil)

	paused, err := pauseRepairs(context.Background(), mocked.reaper,
		pausedRepairs([]string{"run-a", "run-b"}, []string{"sched-1", "sched-2"}))

	asserts.Expect(err).To(MatchError(ContainSubstring("run-a")), "the first error is the one returned")
	asserts.Expect(paused).To(Equal(pausedRepairs([]string{"run-b"}, []string{"sched-2"})))
}

func TestResumeRepairsGoesOnAfterAFailure(t *testing.T) {
	asserts := NewWithT(t)
	_, _, mocked := createMockedReconciler(t)
	recorded := pausedRepairs([]string{"run-a", "run-b"}, []string{"sched-1"})
	mocked.reaper.EXPECT().RepairRuns(gomock.Any()).Return([]reaper.RepairRun{
		{ID: "run-a", State: reaper.RepairStatePaused},
		{ID: "run-b", State: reaper.RepairStatePaused},
	}, nil)
	mocked.reaper.EXPECT().RepairSchedules(gomock.Any()).Return([]reaper.RepairSchedule{
		{ID: "sched-1", State: reaper.RepairScheduleStatePaused},
	}, nil)
	mocked.reaper.EXPECT().ResumeRepairRun(gomock.Any(), "run-a").Return(errors.New("gone wrong"))
	mocked.reaper.EXPECT().ResumeRepairRun(gomock.Any(), "run-b").Return(nil)
	mocked.reaper.EXPECT().SetRepairScheduleState(gomock.Any(), "sched-1", true).Return(nil)

	runs, schedules, err := resumeRepairs(context.Background(), mocked.reaper, recorded)

	asserts.Expect(err).To(MatchError(ContainSubstring("run-a")))
	asserts.Expect(runs).To(Equal(1))
	asserts.Expect(schedules).To(Equal(1))
}
