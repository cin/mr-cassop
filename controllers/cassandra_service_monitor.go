package controllers

import (
	"context"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/compare"
	"github.com/cin/mr-cassop/controllers/labels"
	v1 "k8s.io/api/core/v1"

	"github.com/pkg/errors"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Prometheus RelabelConfig: https://github.com/prometheus-operator/prometheus-operator/blob/main/Documentation/api.md#relabelconfig
type relabelConfig struct {
	SourceLabels []interface{} `json:"sourceLabels"`
	Separator    string        `json:"separator"`
	TargetLabel  string        `json:"targetLabel"`
	Regex        string        `json:"regex"`
	Modulus      uint64        `json:"modulus"`
	Replacement  string        `json:"replacement"`
	Action       string        `json:"action"`
}

var serviceMonitorGVK = schema.GroupVersionKind{
	Group:   "monitoring.coreos.com",
	Version: "v1",
	Kind:    "ServiceMonitor",
}

var serviceMonitorListGVK = schema.GroupVersionKind{
	Group:   "monitoring.coreos.com",
	Version: "v1",
	Kind:    "ServiceMonitorList",
}

func (r *CassandraClusterReconciler) reconcileCassandraServiceMonitor(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	if err := r.cleanupOldCassandraServiceMonitors(ctx, cc); err != nil {
		return errors.WithStack(err)
	}

	if !cc.Spec.Cassandra.Monitoring.Enabled || !cc.Spec.Cassandra.Monitoring.ServiceMonitor.Enabled {
		return nil
	}

	if cc.Spec.Cassandra.Monitoring.ServiceMonitor.Namespace != "" {
		installationNamespace := cc.Spec.Cassandra.Monitoring.ServiceMonitor.Namespace
		if err := r.Get(ctx, types.NamespacedName{Name: installationNamespace}, &v1.Namespace{}); err != nil {
			if kerrors.IsNotFound(err) {
				r.Log.Warnf("Can't install ServiceMonitor. Namespace %q doesn't exist", installationNamespace)
				return nil
			}
			return errors.WithStack(err)
		}
	}

	serviceMonitor, err := r.createCassandraServiceMonitor(cc)
	if err != nil {
		return err
	}

	if err = r.applyServiceMonitor(ctx, serviceMonitor); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (r *CassandraClusterReconciler) applyServiceMonitor(ctx context.Context, desiredSM *unstructured.Unstructured) error {
	actualSM := &unstructured.Unstructured{}
	actualSM.SetGroupVersionKind(serviceMonitorGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: desiredSM.GetNamespace(), Name: desiredSM.GetName()}, actualSM)
	if err != nil && kerrors.IsNotFound(err) {
		r.Log.Infof("Creating ServiceMonitor %s/%s", desiredSM.GetNamespace(), desiredSM.GetName())
		if err = r.Create(ctx, desiredSM); err != nil {
			return errors.Wrap(err, "Unable to create ServiceMonitor")
		}
	} else if _, ok := errors.Cause(err).(*meta.NoKindMatchError); ok {
		r.Log.Warn("ServiceMonitor can't be installed. Prometheus operator needs to be installed first")
		return nil
	} else if err != nil {
		return errors.Wrap(err, "Could not get ServiceMonitor")
	} else if !compare.EqualServiceMonitor(actualSM, desiredSM) {
		r.Log.Infof("Updating ServiceMonitor %s/%s", actualSM.GetNamespace(), actualSM.GetName())
		r.Log.Debug(compare.DiffServiceMonitor(actualSM, desiredSM))
		actualSM.Object["spec"] = desiredSM.Object["spec"]
		actualSM.SetLabels(desiredSM.GetLabels())
		if err = r.Update(ctx, actualSM); err != nil {
			return errors.Wrap(err, "Unable to update ServiceMonitor")
		}
	} else {
		r.Log.Debugw("No updates for ServiceMonitor")
	}
	return nil
}

// Deletes ServiceMonitors that are no longer needed. For example if the namespace is changed, the resource from the previous namespace should be deleted.
// If the ServiceMonitor installation is disabled through the spec, we also need to delete the ServiceMonitor.
func (r *CassandraClusterReconciler) cleanupOldCassandraServiceMonitors(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	if !cc.Spec.Cassandra.Monitoring.Enabled || !cc.Spec.Cassandra.Monitoring.ServiceMonitor.Enabled {
		if err := r.removeServiceMonitors(ctx, cc, dbv1alpha1.CassandraClusterComponentCassandra); err != nil {
			return errors.WithStack(err)
		}
		return nil
	}

	serviceMonitorNamespace := cc.Namespace
	if cc.Spec.Cassandra.Monitoring.ServiceMonitor.Namespace != "" {
		serviceMonitorNamespace = cc.Spec.Cassandra.Monitoring.ServiceMonitor.Namespace
	}

	if err := r.removeOldServiceMonitors(ctx, cc, dbv1alpha1.CassandraClusterComponentCassandra, serviceMonitorNamespace); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (r *CassandraClusterReconciler) removeOldServiceMonitors(ctx context.Context, cc *dbv1alpha1.CassandraCluster, label, serviceMonitorNamespace string) error {
	serviceMonitorList := &unstructured.UnstructuredList{}
	serviceMonitorList.SetGroupVersionKind(serviceMonitorListGVK)
	err := r.List(ctx, serviceMonitorList, client.MatchingLabels(labels.CombinedComponentLabels(cc, label)))
	if err != nil {
		if _, ok := errors.Cause(err).(*meta.NoKindMatchError); ok {
			return nil
		}
		return errors.WithStack(err)
	}
	for _, item := range serviceMonitorList.Items {
		if item.GetNamespace() != serviceMonitorNamespace {
			r.Log.Infof("Deleting ServiceMonitor %s/%s", item.GetNamespace(), item.GetName())
			if err = r.Delete(ctx, &item); err != nil {
				return errors.WithStack(err)
			}
		}
	}
	return nil
}

func (r *CassandraClusterReconciler) removeServiceMonitors(ctx context.Context, cc *dbv1alpha1.CassandraCluster, label string) error {
	serviceMonitorList := &unstructured.UnstructuredList{}
	serviceMonitorList.SetGroupVersionKind(serviceMonitorListGVK)
	err := r.List(ctx, serviceMonitorList, client.MatchingLabels(labels.CombinedComponentLabels(cc, label)))
	if err != nil {
		if _, ok := errors.Cause(err).(*meta.NoKindMatchError); ok {
			return nil
		}
		return errors.WithStack(err)
	}

	for _, item := range serviceMonitorList.Items {
		r.Log.Infow("Deleting ServiceMonitor %s/%s", item.GetNamespace(), item.GetName())
		if err = r.Delete(ctx, &item); err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

func (r *CassandraClusterReconciler) createCassandraServiceMonitor(cc *dbv1alpha1.CassandraCluster) (*unstructured.Unstructured, error) {
	serviceMonitor := &unstructured.Unstructured{}
	serviceMonitor.SetGroupVersionKind(serviceMonitorGVK)
	serviceMonitor.SetName(cc.Name)
	serviceMonitor.SetLabels(labels.CombinedComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra))

	serviceMonitor.SetNamespace(cc.Namespace)
	if cc.Spec.Cassandra.Monitoring.ServiceMonitor.Namespace != "" {
		serviceMonitor.SetNamespace(cc.Spec.Cassandra.Monitoring.ServiceMonitor.Namespace)
	}

	additionalLabels := cc.Spec.Cassandra.Monitoring.ServiceMonitor.Labels
	if len(additionalLabels) > 0 {
		newLabels := serviceMonitor.GetLabels()
		for key, value := range additionalLabels {
			newLabels[key] = value
		}
		serviceMonitor.SetLabels(newLabels)
	}

	matchLabels := map[string]interface{}{}
	componentLabels := labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra)
	for key, value := range componentLabels {
		matchLabels[key] = value
	}
	serviceMonitor.Object["spec"] = map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": matchLabels,
		},
		"endpoints": []interface{}{
			map[string]interface{}{
				"port":     "agent",
				"interval": cc.Spec.Cassandra.Monitoring.ServiceMonitor.ScrapeInterval,
			},
		},
		"namespaceSelector": map[string]interface{}{
			"matchNames": []interface{}{cc.Namespace},
		},
	}
	relabelings := getCassandraServiceMonitorRelabelings(cc)
	if len(relabelings) > 0 {
		serviceMonitor.Object["spec"].(map[string]interface{})["endpoints"].([]interface{})[0].(map[string]interface{})["relabelings"] = relabelings
	}

	// Set reference only if the ServiceMonitor is installed in the same namespace. It can't be set cross namespace.
	if cc.Spec.Cassandra.Monitoring.ServiceMonitor.Namespace == "" {
		err := controllerutil.SetControllerReference(cc, serviceMonitor, r.Scheme)
		if err != nil {
			return nil, errors.WithStack(err)
		}
	}

	return serviceMonitor, nil
}

func createRelabeling(config relabelConfig) map[string]interface{} {
	relabeling := map[string]interface{}{}
	if len(config.SourceLabels) > 0 {
		relabeling["sourceLabels"] = config.SourceLabels
	}
	if config.Separator != "" {
		relabeling["separator"] = config.Separator
	}
	if config.TargetLabel != "" {
		relabeling["targetLabel"] = config.TargetLabel
	}
	if config.Regex != "" {
		relabeling["regex"] = config.Regex
	}
	if config.Replacement != "" {
		relabeling["replacement"] = config.Replacement
	}
	if config.Action != "" {
		relabeling["action"] = config.Action
	}
	if config.Modulus != 0 {
		relabeling["modulus"] = config.Modulus
	}
	return relabeling
}

func getCassandraServiceMonitorRelabelings(cc *dbv1alpha1.CassandraCluster) []interface{} {
	var relabelings []interface{}
	if cc.Spec.Cassandra.Monitoring.Agent == dbv1alpha1.CassandraAgentTlp {
		relabelings = []interface{}{
			createRelabeling(relabelConfig{
				Regex:  "__meta_kubernetes_pod_label_(.+)",
				Action: "labelmap",
			}),
			createRelabeling(relabelConfig{
				Action:       "replace",
				TargetLabel:  "host",
				SourceLabels: []interface{}{"__meta_kubernetes_pod_name"},
			}),
		}
	}
	return relabelings
}
