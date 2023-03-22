package cassandrabackup

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/config"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/icarus"
	"github.com/cin/mr-cassop/controllers/names"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// CassandraBackupReconciler reconciles a CassandraCluster object
type CassandraBackupReconciler struct {
	client.Client
	Log          *zap.SugaredLogger
	Scheme       *runtime.Scheme
	Cfg          config.Config
	Events       *events.EventRecorder
	IcarusClient func(coordinatorPodURL string) icarus.Icarus
}

// +kubebuilder:rbac:groups=db.ibm.com,resources=cassandrabackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db.ibm.com,resources=cassandrabackups/status,verbs=get;update;patch

func (r *CassandraBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cb := &v1alpha1.CassandraBackup{}
	err := r.Get(ctx, req.NamespacedName, cb)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if cb.Status.State == icarus.StateCompleted {
		r.Log.Debugf("Backup %v is compeleted", cb.Name)
		return ctrl.Result{}, nil
	}

	cc := &v1alpha1.CassandraCluster{}
	err = r.Get(ctx, types.NamespacedName{Name: cb.Spec.CassandraCluster, Namespace: cb.Namespace}, cc)
	if err != nil {
		if kerrors.IsNotFound(err) {
			errMsg := fmt.Sprintf("Failed to create backup for cluster %q. Cluster not found.", cb.Spec.CassandraCluster)
			r.Log.Warn(errMsg)
			r.Events.Warning(cb, events.EventCassandraClusterNotFound, errMsg)
			return ctrl.Result{RequeueAfter: r.Cfg.RetryDelay}, nil
		}
		return ctrl.Result{}, err
	}

	if !cc.Status.Ready {
		r.Log.Warnf("CassandraCluster %s/%s is not ready. Not starting backup, trying again in %s...", cc.Namespace, cc.Name, r.Cfg.RetryDelay)
		return ctrl.Result{RequeueAfter: r.Cfg.RetryDelay}, nil
	}

	storageCredentials := &v1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: cb.Spec.SecretName, Namespace: cb.Namespace}, storageCredentials)
	if err != nil {
		if kerrors.IsNotFound(err) {
			errMsg := fmt.Sprintf("Failed to create backup for cluster %q. Storage credentials secret %q not found.", cb.Spec.CassandraCluster, cb.Spec.SecretName)
			r.Log.Warn(errMsg)
			r.Events.Warning(cb, events.EventStorageCredentialsSecretNotFound, errMsg)
			return ctrl.Result{RequeueAfter: r.Cfg.RetryDelay}, nil
		}

		return ctrl.Result{}, err
	}

	err = v1alpha1.ValidateStorageSecret(r.Log, storageCredentials, cb.StorageProvider())
	if err != nil {
		errMsg := fmt.Sprintf("Storage credentials secret %q is invalid: %s", cb.Spec.SecretName, err.Error())
		r.Log.Warn(errMsg)
		r.Events.Warning(cb, events.EventStorageCredentialsSecretNotFound, errMsg)
		return ctrl.Result{RequeueAfter: r.Cfg.RetryDelay}, nil
	}

	dcName := cc.Spec.DCs[0].Name
	svc := names.DC(cc.Name, dcName)
	//always use the same pod as the coordinator as only that pod has the global request info
	coordinatorPodURL := fmt.Sprintf("http://%s-0.%s.%s.svc.cluster.local:%d", svc, svc, cc.Namespace, v1alpha1.IcarusPort)

	ic := r.IcarusClient(coordinatorPodURL)

	res, err := r.reconcileBackup(ctx, ic, cb, cc)
	if err != nil {
		if statusErr, ok := errors.Cause(err).(*kerrors.StatusError); ok && statusErr.ErrStatus.Reason == metav1.StatusReasonConflict {
			r.Log.Info("Conflict occurred. Retrying...", zap.Error(err))
			return ctrl.Result{Requeue: true}, nil //retry but do not treat conflicts as errors
		}

		r.Log.Errorf("%+v", err)
		return ctrl.Result{}, err
	}

	return res, nil
}

func SetupCassandraBackupReconciler(r reconcile.Reconciler, mgr manager.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("cassandrabackup").
		For(&v1alpha1.CassandraBackup{})

	return builder.Complete(r)
}
