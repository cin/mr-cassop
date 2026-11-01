package integration

import (
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/reaper"
)

var _ = Describe("repairs during a rolling update", func() {
	newCluster := func(pauseRepairs bool) *v1alpha1.CassandraCluster {
		return &v1alpha1.CassandraCluster{
			ObjectMeta: cassandraObjectMeta,
			Spec: v1alpha1.CassandraClusterSpec{
				DCs:                 []v1alpha1.DC{{Name: "dc1", Replicas: proto.Int32(3)}},
				ImagePullSecretName: "pull-secret-name",
				AdminRoleSecretName: "admin-role",
				Reaper:              &v1alpha1.Reaper{PauseRepairsDuringRoll: pauseRepairs},
			},
		}
	}

	runState := func(runID string) func() string {
		return func() string {
			runs, err := mockReaperClient.RepairRuns(ctx)
			Expect(err).NotTo(HaveOccurred())
			for _, run := range runs {
				if run.ID == runID {
					return run.State
				}
			}
			return "not found"
		}
	}
	scheduleState := func(scheduleID string) func() string {
		return func() string {
			schedules, err := mockReaperClient.RepairSchedules(ctx)
			Expect(err).NotTo(HaveOccurred())
			for _, schedule := range schedules {
				if schedule.ID == scheduleID {
					return schedule.State
				}
			}
			return "not found"
		}
	}
	pausedInStatus := func(cc *v1alpha1.CassandraCluster) func() v1alpha1.PausedRepairs {
		return func() v1alpha1.PausedRepairs {
			actual := &v1alpha1.CassandraCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cc.Name, Namespace: cc.Namespace}, actual)).To(Succeed())
			return actual.Status.PausedRepairs
		}
	}
	// The operator under test has no statefulset controller to report that the pods rolled, so the test does.
	markStatefulSetRolled := func(cc *v1alpha1.CassandraCluster) {
		Eventually(func() error { // eventually so we handle conflict errors
			sts := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: names.DC(cc.Name, "dc1"), Namespace: cc.Namespace}, sts); err != nil {
				return err
			}
			sts.Status.ObservedGeneration = sts.Generation
			sts.Status.CurrentRevision = "rolled"
			sts.Status.UpdateRevision = "rolled"
			sts.Status.UpdatedReplicas = *sts.Spec.Replicas
			sts.Status.ReadyReplicas = *sts.Spec.Replicas
			return k8sClient.Status().Update(ctx, sts)
		}, longTimeout, mediumRetry).Should(Succeed())
	}
	changePodTemplate := func(cc *v1alpha1.CassandraCluster) {
		Eventually(func() error {
			actual := &v1alpha1.CassandraCluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: cc.Name, Namespace: cc.Namespace}, actual); err != nil {
				return err
			}
			actual.Spec.Cassandra = &v1alpha1.Cassandra{LogLevel: "debug"}
			return k8sClient.Update(ctx, actual)
		}, longTimeout, mediumRetry).Should(Succeed())
	}
	givenRunningRepairs := func() {
		mockReaperClient.isRunning = true
		mockReaperClient.repairRuns = []reaper.RepairRun{
			{ID: "run-1", State: reaper.RepairStateRunning, Owner: "someone"},
			{ID: "run-paused-by-user", State: reaper.RepairStatePaused, Owner: "someone"},
		}
		mockReaperClient.repairSchedules = []reaper.RepairSchedule{
			{ID: "schedule-1", State: reaper.RepairScheduleStateActive, Owner: "someone"},
		}
	}

	Context("when the option is on", func() {
		It("pauses the running repairs before the pods roll and resumes only those once the statefulset has rolled", func() {
			cc := newCluster(true)
			givenRunningRepairs()
			createReadyCluster(cc)

			changePodTemplate(cc)

			By("pausing the runs and schedules and recording them in the status")
			Eventually(runState("run-1"), longTimeout, mediumRetry).Should(Equal(reaper.RepairStatePaused))
			Eventually(scheduleState("schedule-1"), longTimeout, mediumRetry).Should(Equal(reaper.RepairScheduleStatePaused))
			Eventually(pausedInStatus(cc), longTimeout, mediumRetry).Should(Equal(v1alpha1.PausedRepairs{
				RunIDs:      []string{"run-1"},
				ScheduleIDs: []string{"schedule-1"},
			}))

			By("keeping them paused while the statefulset is still rolling")
			Consistently(runState("run-1"), shortTimeout, mediumRetry).Should(Equal(reaper.RepairStatePaused))

			By("resuming only what the operator paused once the statefulset has rolled")
			markStatefulSetRolled(cc)
			Eventually(runState("run-1"), longTimeout, mediumRetry).Should(Equal(reaper.RepairStateRunning))
			Eventually(scheduleState("schedule-1"), longTimeout, mediumRetry).Should(Equal(reaper.RepairScheduleStateActive))
			Eventually(pausedInStatus(cc), longTimeout, mediumRetry).Should(Equal(v1alpha1.PausedRepairs{}))
			Expect(runState("run-paused-by-user")()).To(Equal(reaper.RepairStatePaused))
		})
	})

	Context("when the option is off", func() {
		It("leaves the repairs running during a rolling update", func() {
			cc := newCluster(false)
			givenRunningRepairs()
			createReadyCluster(cc)

			changePodTemplate(cc)

			Eventually(func() int {
				sts := &appsv1.StatefulSet{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: names.DC(cc.Name, "dc1"), Namespace: cc.Namespace}, sts)).To(Succeed())
				return len(sts.Spec.Template.Spec.Containers)
			}, longTimeout, mediumRetry).Should(BeNumerically(">", 0))
			Consistently(runState("run-1"), shortTimeout, mediumRetry).Should(Equal(reaper.RepairStateRunning))
			Expect(scheduleState("schedule-1")()).To(Equal(reaper.RepairScheduleStateActive))
			Expect(pausedInStatus(cc)()).To(Equal(v1alpha1.PausedRepairs{}))
		})
	})
})
