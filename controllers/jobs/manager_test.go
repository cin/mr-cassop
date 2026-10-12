package jobs

import (
	"testing"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	. "github.com/onsi/gomega"
)

const jobName = "job-a"

func newTestManager() (*JobManager, chan event.GenericEvent) {
	reconcile := make(chan event.GenericEvent, 10)
	return NewJobManager(reconcile, zap.NewNop().Sugar()), reconcile
}

func TestRunRegistersJobWhileRunning(t *testing.T) {
	asserts := NewWithT(t)
	m, reconcile := newTestManager()
	release := make(chan struct{})

	asserts.Expect(m.Run(jobName, &corev1.Pod{}, func() error { <-release; return nil })).To(Succeed())
	asserts.Expect(m.IsRunning(jobName)).To(BeTrue())
	asserts.Expect(m.IsRunning("other")).To(BeFalse())

	close(release)
	asserts.Eventually(reconcile, time.Second).Should(Receive())
	asserts.Expect(m.IsRunning(jobName)).To(BeFalse(), "the reconcile event must not arrive while the job still looks running")
}

func TestRunRejectsDuplicateWhileRunning(t *testing.T) {
	asserts := NewWithT(t)
	m, reconcile := newTestManager()
	release := make(chan struct{})

	asserts.Expect(m.Run(jobName, &corev1.Pod{}, func() error { <-release; return nil })).To(Succeed())
	err := m.Run(jobName, &corev1.Pod{}, func() error { return nil })
	asserts.Expect(err).To(MatchError("job job-a already exists"))

	close(release)
	asserts.Eventually(reconcile, time.Second).Should(Receive())
	asserts.Consistently(reconcile, 100*time.Millisecond).ShouldNot(Receive(), "the rejected duplicate must not have run")
}

func TestRunAllowsRestartAfterFinish(t *testing.T) {
	tests := []struct {
		name   string
		result error
	}{
		{name: "after success", result: nil},
		{name: "after failure", result: errors.New("refused")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asserts := NewWithT(t)
			m, reconcile := newTestManager()

			asserts.Expect(m.Run(jobName, &corev1.Pod{}, func() error { return tt.result })).To(Succeed())
			asserts.Eventually(reconcile, time.Second).Should(Receive())

			asserts.Expect(m.Run(jobName, &corev1.Pod{}, func() error { return nil })).To(Succeed())
			asserts.Eventually(reconcile, time.Second).Should(Receive())
		})
	}
}
