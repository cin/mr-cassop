package controllers

import (
	"context"
	"fmt"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/compare"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// reconcileUI creates/updates the optional nodetool-style diagnostic UI (docs/docs/nodetool-ui.md)
// for this cluster, or leaves it untouched if spec.ui.enabled is false. It's reconciled alongside
// prober - it only ever proxies HTTP calls to prober's in-cluster service, so it doesn't need to
// wait on cluster readiness the way Cassandra-dependent components do.
func (r *CassandraClusterReconciler) reconcileUI(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	if !cc.Spec.UI.Enabled {
		return r.cleanupUI(ctx, cc)
	}

	if cc.Spec.UI.Image == "" {
		warnMsg := "spec.ui.enabled is true but no UI image is configured (neither spec.ui.image nor the " +
			"operator's DEFAULT_UI_IMAGE) - skipping until one is set"
		r.Events.Warning(cc, events.EventUIImageMissing, warnMsg)
		r.Log.Warn(warnMsg)
		return nil
	}

	if err := r.reconcileUIDeployment(ctx, cc); err != nil {
		return errors.Wrap(err, "failed to reconcile ui deployment")
	}

	if err := r.reconcileUIService(ctx, cc); err != nil {
		return errors.Wrap(err, "failed to reconcile ui service")
	}

	return nil
}

// cleanupUI deletes the UI Deployment/Service left over from spec.ui.enabled having previously been
// true, mirroring cleanupNetworkPolicies' delete-on-disable behavior for NetworkPolicies.Enabled.
func (r *CassandraClusterReconciler) cleanupUI(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: names.UIDeployment(cc.Name), Namespace: cc.Namespace}, deployment)
	if err == nil {
		if err = r.Delete(ctx, deployment); err != nil {
			return errors.Wrap(err, "failed to delete ui deployment")
		}
	} else if !apierrors.IsNotFound(err) {
		return errors.Wrap(err, "failed to get ui deployment")
	}

	service := &v1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: names.UIService(cc.Name), Namespace: cc.Namespace}, service)
	if err == nil {
		if err = r.Delete(ctx, service); err != nil {
			return errors.Wrap(err, "failed to delete ui service")
		}
	} else if !apierrors.IsNotFound(err) {
		return errors.Wrap(err, "failed to get ui service")
	}

	return nil
}

func (r *CassandraClusterReconciler) reconcileUIDeployment(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	uiLabels := labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentUI)
	desiredDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.UIDeployment(cc.Name),
			Namespace: cc.Namespace,
			Labels:    labels.CombinedComponentLabels(cc, dbv1alpha1.CassandraClusterComponentUI),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{
				MatchLabels: uiLabels,
			},
			RevisionHistoryLimit: ptr.To[int32](10),
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: uiLabels,
				},
				Spec: v1.PodSpec{
					Containers:       []v1.Container{uiContainer(cc)},
					Volumes:          []v1.Volume{uiProberCredentialsVolume(cc)},
					RestartPolicy:    v1.RestartPolicyAlways,
					DNSPolicy:        v1.DNSClusterFirst,
					SecurityContext:  &v1.PodSecurityContext{},
					ImagePullSecrets: imagePullSecrets(cc),
					Tolerations:      cc.Spec.UI.Tolerations,
					NodeSelector:     cc.Spec.UI.NodeSelector,
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(cc, desiredDeployment, r.Scheme); err != nil {
		return errors.Wrap(err, "Cannot set controller reference")
	}

	actualDeployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: names.UIDeployment(cc.Name), Namespace: cc.Namespace}, actualDeployment)
	if err != nil && apierrors.IsNotFound(err) {
		r.Log.Info("Creating ui deployment")
		if err = r.Create(ctx, desiredDeployment); err != nil {
			return errors.Wrap(err, "Failed to create deployment")
		}
	} else if err != nil {
		return errors.Wrap(err, "Failed to get deployment")
	} else {
		desiredDeployment.Annotations = actualDeployment.Annotations
		if !compare.EqualDeployment(desiredDeployment, actualDeployment) {
			r.Log.Info("Updating ui deployment")
			r.Log.Debug(compare.DiffDeployment(actualDeployment, desiredDeployment))
			actualDeployment.Spec = desiredDeployment.Spec
			actualDeployment.Labels = desiredDeployment.Labels
			if err = r.Update(ctx, actualDeployment); err != nil {
				return errors.Wrap(err, "failed to update deployment")
			}
		} else {
			r.Log.Debugf("No updates to ui deployment")
		}
	}

	return nil
}

func (r *CassandraClusterReconciler) reconcileUIService(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	desiredService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.UIService(cc.Name),
			Labels:    labels.CombinedComponentLabels(cc, dbv1alpha1.CassandraClusterComponentUI),
			Namespace: cc.Namespace,
		},
		Spec: v1.ServiceSpec{
			// ClusterIP only - this is an internal diagnostic tool with real (if gated) write and
			// destructive operations, not something to expose publicly by default. Reach it via
			// `kubectl port-forward`, same as prober/reaper today.
			Type: v1.ServiceTypeClusterIP,
			// Must match the Pods' own labels (ComponentLabels, set on the Deployment's pod
			// template below), not CombinedComponentLabels - the latter also inherits the
			// CassandraCluster CR's own metadata.labels, which the Pods never get, so using it
			// here would under-select down to zero endpoints as soon as the CR itself has any
			// labels set.
			Selector: labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentUI),
			Ports: []v1.ServicePort{
				{
					Port:       dbv1alpha1.UIContainerPort,
					Name:       "ui",
					TargetPort: intstr.FromString("ui"),
					Protocol:   v1.ProtocolTCP,
				},
			},
			SessionAffinity: v1.ServiceAffinityNone,
		},
	}

	if err := controllerutil.SetControllerReference(cc, desiredService, r.Scheme); err != nil {
		return errors.Wrap(err, "Cannot set controller reference")
	}

	actualService := &v1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: names.UIService(cc.Name), Namespace: cc.Namespace}, actualService)
	if err != nil && apierrors.IsNotFound(err) {
		r.Log.Info("Creating ui service")
		if err = r.Create(ctx, desiredService); err != nil {
			return errors.Wrap(err, "Failed to create service")
		}
	} else if err != nil {
		return errors.Wrap(err, "Failed to get service")
	} else {
		// ClusterIP is immutable once created, so always enforce the same as existing
		desiredService.Spec.ClusterIP = actualService.Spec.ClusterIP
		desiredService.Spec.ClusterIPs = actualService.Spec.ClusterIPs
		desiredService.Spec.IPFamilies = actualService.Spec.IPFamilies
		desiredService.Spec.IPFamilyPolicy = actualService.Spec.IPFamilyPolicy
		desiredService.Spec.InternalTrafficPolicy = actualService.Spec.InternalTrafficPolicy
		if !compare.EqualService(desiredService, actualService) {
			r.Log.Info("Updating ui service")
			r.Log.Debugf(compare.DiffService(actualService, desiredService))
			actualService.Spec = desiredService.Spec
			actualService.Labels = desiredService.Labels
			actualService.Annotations = desiredService.Annotations
			if err = r.Update(ctx, actualService); err != nil {
				return errors.Wrap(err, "failed to update service")
			}
		} else {
			r.Log.Debugf("No updates to ui service")
		}
	}

	return nil
}

const (
	uiProberCredentialsVolumeName = "prober-credentials"
	uiProberCredentialsDir        = "/etc/prober-credentials"
)

// uiProberCredentialsVolume mounts the secret prober validates its HTTP Basic Auth against. It's a
// volume rather than secretKeyRef env vars because the operator rotates this secret's password in
// place, and kubelet refreshes a secret volume's files (env vars are fixed at container start). The
// UI re-reads the files on every request, so a rotation reaches it without a pod restart.
func uiProberCredentialsVolume(cc *dbv1alpha1.CassandraCluster) v1.Volume {
	return v1.Volume{
		Name: uiProberCredentialsVolumeName,
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: proberAuthSecretName(cc),
				Items: []v1.KeyToPath{
					{Key: dbv1alpha1.CassandraOperatorAdminRole, Path: dbv1alpha1.CassandraOperatorAdminRole},
					{Key: dbv1alpha1.CassandraOperatorAdminPassword, Path: dbv1alpha1.CassandraOperatorAdminPassword},
				},
				// Set explicitly to the server default so desired matches what's read back.
				DefaultMode: ptr.To[int32](v1.SecretVolumeSourceDefaultMode),
			},
		},
	}
}

func uiContainer(cc *dbv1alpha1.CassandraCluster) v1.Container {
	return v1.Container{
		Name:            "ui",
		Image:           cc.Spec.UI.Image,
		ImagePullPolicy: cc.Spec.UI.ImagePullPolicy,
		Resources:       cc.Spec.UI.Resources,
		Env: []v1.EnvVar{
			{Name: "PROBER_ENV_NAME", Value: cc.Name},
			{Name: "PROBER_URL", Value: proberURL(cc).String()},
			{Name: "PROBER_CREDENTIALS_DIR", Value: uiProberCredentialsDir},
			{Name: "LISTEN_ADDR", Value: fmt.Sprintf(":%d", dbv1alpha1.UIContainerPort)},
		},
		VolumeMounts: []v1.VolumeMount{
			{Name: uiProberCredentialsVolumeName, MountPath: uiProberCredentialsDir, ReadOnly: true},
		},
		Ports: []v1.ContainerPort{
			{
				Name:          "ui",
				ContainerPort: dbv1alpha1.UIContainerPort,
				Protocol:      v1.ProtocolTCP,
			},
		},
		ReadinessProbe: &v1.Probe{
			ProbeHandler: v1.ProbeHandler{
				HTTPGet: &v1.HTTPGetAction{
					Port:   intstr.FromString("ui"),
					Path:   "/api/environments",
					Scheme: v1.URISchemeHTTP,
				},
			},
			TimeoutSeconds:   1,
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		},
	}
}
