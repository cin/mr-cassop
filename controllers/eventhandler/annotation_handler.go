package eventhandler

import (
	"context"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func NewAnnotationEventHandler() *AnnotationEventHandler {
	return &AnnotationEventHandler{}
}

type AnnotationEventHandler struct{}

// Create implements handler.TypedEventHandler
func (h *AnnotationEventHandler) Create(ctx context.Context, e event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// No-op for create events
}

// Delete implements handler.TypedEventHandler
func (h *AnnotationEventHandler) Delete(ctx context.Context, e event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// No-op for delete events
}

// Update implements handler.TypedEventHandler
func (h *AnnotationEventHandler) Update(ctx context.Context, e event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	annotations := e.ObjectNew.GetAnnotations()
	if len(annotations) == 0 {
		return
	}

	ccInstanceName := annotations[v1alpha1.CassandraClusterInstance]
	if ccInstanceName == "" {
		return
	}
	reconcileRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: ccInstanceName, Namespace: e.ObjectNew.GetNamespace()}}
	q.Add(reconcileRequest)
}

// Generic implements handler.TypedEventHandler
func (h *AnnotationEventHandler) Generic(ctx context.Context, e event.TypedGenericEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	annotations := e.Object.GetAnnotations()
	if len(annotations) == 0 {
		return
	}

	ccInstanceName := annotations[v1alpha1.CassandraClusterInstance]
	if ccInstanceName == "" {
		return
	}

	reconcileRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: ccInstanceName, Namespace: e.Object.GetNamespace()}}
	q.Add(reconcileRequest)
}
