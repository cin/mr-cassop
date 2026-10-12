package jobs

import (
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

const progressLogInterval = 10 * time.Second

// JobManager runs named background jobs, one at a time per name. A job is registered while it runs and
// dropped as soon as it finishes, whatever the outcome, so a failed job can be started again. Callers
// decide what to do next from the state of the system, not from the job's result.
type JobManager struct {
	sync.Mutex
	running   map[string]struct{}
	log       *zap.SugaredLogger
	reconcile chan event.GenericEvent
}

func NewJobManager(reconcileChan chan event.GenericEvent, logr *zap.SugaredLogger) *JobManager {
	return &JobManager{
		running:   make(map[string]struct{}),
		reconcile: reconcileChan,
		log:       logr,
	}
}

// Run starts f in the background under name. It fails if a job with that name is still running.
// When f returns, the job is unregistered and notifyObj is queued for reconciliation.
func (j *JobManager) Run(name string, notifyObj client.Object, f func() error) error {
	if !j.register(name) {
		return errors.Errorf("job %s already exists", name)
	}

	j.log.Infof("starting job %s", name)
	go j.run(name, notifyObj, f)

	return nil
}

func (j *JobManager) register(name string) bool {
	j.Lock()
	defer j.Unlock()
	if _, running := j.running[name]; running {
		return false
	}
	j.running[name] = struct{}{}

	return true
}

func (j *JobManager) unregister(name string) {
	j.Lock()
	defer j.Unlock()
	delete(j.running, name)
}

func (j *JobManager) run(name string, notifyObj client.Object, f func() error) {
	start := time.Now()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		// unregister before signalling, so the reconcile this triggers doesn't still see the job running
		defer j.unregister(name)
		if err := f(); err != nil {
			j.log.Warnf("job %s failed: %s", name, err)
		}
	}()

	ticker := time.NewTicker(progressLogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-finished:
			j.log.Infof("job %s finished in %s", name, time.Since(start))
			j.reconcile <- event.GenericEvent{Object: notifyObj}
			return
		case <-ticker.C:
			j.log.Infof("job %s is running. Elapsed time: %s", name, time.Since(start))
		}
	}
}

// IsRunning reports whether a job with this name has been started and hasn't finished yet.
func (j *JobManager) IsRunning(name string) bool {
	j.Lock()
	defer j.Unlock()
	_, running := j.running[name]

	return running
}
