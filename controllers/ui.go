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
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	meta := metav1.ObjectMeta{Name: names.UI(cc.Name), Namespace: cc.Namespace}
	if err := client.IgnoreNotFound(r.Delete(ctx, &appsv1.Deployment{ObjectMeta: meta})); err != nil {
		return errors.Wrap(err, "failed to delete ui deployment")
	}
	if err := client.IgnoreNotFound(r.Delete(ctx, &v1.Service{ObjectMeta: meta})); err != nil {
		return errors.Wrap(err, "failed to delete ui service")
	}
	return nil
}

func (r *CassandraClusterReconciler) reconcileUIDeployment(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	secretUID, err := r.uiCredentialsSecretUID(ctx, cc)
	if err != nil {
		return err
	}

	desired := desiredUIDeployment(cc, secretUID)
	if err := controllerutil.SetControllerReference(cc, desired, r.Scheme); err != nil {
		return errors.Wrap(err, "cannot set controller reference")
	}

	actual := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, actual)
	if apierrors.IsNotFound(err) {
		r.Log.Info("Creating ui deployment")
		return errors.Wrap(r.Create(ctx, desired), "failed to create deployment")
	}
	if err != nil {
		return errors.Wrap(err, "failed to get deployment")
	}
	return r.updateUIDeployment(ctx, desired, actual)
}

func (r *CassandraClusterReconciler) updateUIDeployment(ctx context.Context, desired, actual *appsv1.Deployment) error {
	desired.Annotations = actual.Annotations
	if compare.EqualDeployment(desired, actual) {
		r.Log.Debug("No updates to ui deployment")
		return nil
	}
	r.Log.Info("Updating ui deployment")
	r.Log.Debug(compare.DiffDeployment(actual, desired))
	actual.Spec = desired.Spec
	actual.Labels = desired.Labels
	return errors.Wrap(r.Update(ctx, actual), "failed to update deployment")
}

// uiCredentialsSecretAnnotation records the UID of the secret the UI mounted its credentials from. The
// operator replaces <cluster>-auth-active-admin (it's immutable) instead of updating it, and kubelet
// never refreshes a volume backed by a replaced secret, so a UI pod that started before the replacement
// keeps the old credentials forever. A new UID changes the pod template, which rolls the Deployment.
const uiCredentialsSecretAnnotation = "ui-credentials-secret-uid"

// uiCredentialsSecretUID returns the UID of the secret the UI mounts, or "" if it doesn't exist yet. The
// pod can't start without the secret, so there's nothing to roll until it's created.
func (r *CassandraClusterReconciler) uiCredentialsSecretUID(ctx context.Context, cc *dbv1alpha1.CassandraCluster) (types.UID, error) {
	secret := &v1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: proberAuthSecretName(cc), Namespace: cc.Namespace}, secret)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", errors.Wrap(err, "failed to get the secret the ui mounts its credentials from")
	}

	return secret.UID, nil
}

// desiredUIDeployment is a single Kubernetes manifest literal, left unsplit for readability.
func desiredUIDeployment(cc *dbv1alpha1.CassandraCluster, credentialsSecretUID types.UID) *appsv1.Deployment {
	uiLabels := labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentUI)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.UI(cc.Name),
			Namespace: cc.Namespace,
			Labels:    labels.CombinedComponentLabels(cc, dbv1alpha1.CassandraClusterComponentUI),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:                ptr.To[int32](1),
			Selector:                &metav1.LabelSelector{MatchLabels: uiLabels},
			Strategy:                uiDeploymentStrategy(),
			RevisionHistoryLimit:    ptr.To[int32](10),
			ProgressDeadlineSeconds: ptr.To[int32](uiProgressDeadlineSeconds),
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      uiLabels,
					Annotations: map[string]string{uiCredentialsSecretAnnotation: string(credentialsSecretUID)},
				},
				Spec: v1.PodSpec{
					Containers:                    []v1.Container{uiContainer(cc)},
					Volumes:                       []v1.Volume{uiProberCredentialsVolume(cc)},
					RestartPolicy:                 v1.RestartPolicyAlways,
					DNSPolicy:                     v1.DNSClusterFirst,
					TerminationGracePeriodSeconds: ptr.To[int64](uiTerminationGracePeriodSeconds),
					SecurityContext:               uiPodSecurityContext(),
					ImagePullSecrets:              imagePullSecrets(cc),
					Tolerations:                   cc.Spec.UI.Tolerations,
					NodeSelector:                  cc.Spec.UI.NodeSelector,
				},
			},
		},
	}
}

// The API server fills in these Deployment fields (and the strategy and container termination message
// fields) when they're left unset. The desired object sets the same values, so a reconcile of an unchanged
// cluster finds no diff and issues no Update (#218).
const (
	uiTerminationGracePeriodSeconds = 30
	uiProgressDeadlineSeconds       = 600
)

// uiDeploymentStrategy is the API server's default rolling update. For the single replica a 25% maxUnavailable
// rounds down to 0, so the new pod is ready before the old one stops.
func uiDeploymentStrategy() appsv1.DeploymentStrategy {
	percent25 := intstr.FromString("25%")
	return appsv1.DeploymentStrategy{
		Type:          appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &percent25, MaxSurge: &percent25},
	}
}

// uiNonRootUID is the distroless "nonroot" user the UI image runs as (ui/Dockerfile).
const uiNonRootUID = 65532

func uiPodSecurityContext() *v1.PodSecurityContext {
	return &v1.PodSecurityContext{
		RunAsNonRoot:   ptr.To(true),
		RunAsUser:      ptr.To[int64](uiNonRootUID),
		RunAsGroup:     ptr.To[int64](uiNonRootUID),
		SeccompProfile: &v1.SeccompProfile{Type: v1.SeccompProfileTypeRuntimeDefault},
	}
}

func uiContainerSecurityContext() *v1.SecurityContext {
	return &v1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &v1.Capabilities{Drop: []v1.Capability{"ALL"}},
	}
}

func (r *CassandraClusterReconciler) reconcileUIService(ctx context.Context, cc *dbv1alpha1.CassandraCluster) error {
	desired := desiredUIService(cc)
	if err := controllerutil.SetControllerReference(cc, desired, r.Scheme); err != nil {
		return errors.Wrap(err, "cannot set controller reference")
	}

	actual := &v1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, actual)
	if apierrors.IsNotFound(err) {
		r.Log.Info("Creating ui service")
		return errors.Wrap(r.Create(ctx, desired), "failed to create service")
	}
	if err != nil {
		return errors.Wrap(err, "failed to get service")
	}
	return r.updateUIService(ctx, desired, actual)
}

func (r *CassandraClusterReconciler) updateUIService(ctx context.Context, desired, actual *v1.Service) error {
	// ClusterIP is immutable once created, so always enforce the same as existing
	desired.Spec.ClusterIP = actual.Spec.ClusterIP
	desired.Spec.ClusterIPs = actual.Spec.ClusterIPs
	desired.Spec.IPFamilies = actual.Spec.IPFamilies
	desired.Spec.IPFamilyPolicy = actual.Spec.IPFamilyPolicy
	desired.Spec.InternalTrafficPolicy = actual.Spec.InternalTrafficPolicy
	if compare.EqualService(desired, actual) {
		r.Log.Debug("No updates to ui service")
		return nil
	}
	r.Log.Info("Updating ui service")
	r.Log.Debug(compare.DiffService(actual, desired))
	actual.Spec = desired.Spec
	actual.Labels = desired.Labels
	actual.Annotations = desired.Annotations
	return errors.Wrap(r.Update(ctx, actual), "failed to update service")
}

func desiredUIService(cc *dbv1alpha1.CassandraCluster) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.UI(cc.Name),
			Labels:    labels.CombinedComponentLabels(cc, dbv1alpha1.CassandraClusterComponentUI),
			Namespace: cc.Namespace,
		},
		Spec: v1.ServiceSpec{
			// ClusterIP only - this is an internal diagnostic tool with real (if gated) write and
			// destructive operations, not something to expose publicly by default. Reach it via
			// `kubectl port-forward`, same as prober/reaper today.
			Type: v1.ServiceTypeClusterIP,
			// Must match the Pods' own labels (ComponentLabels), not CombinedComponentLabels - the
			// latter also inherits the CR's own metadata.labels, which the Pods never get, so the
			// Service would select zero endpoints as soon as the CR has any labels set.
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
}

const (
	uiProberCredentialsVolumeName = "prober-credentials"
	uiProberCredentialsDir        = "/etc/prober-credentials"
)

// uiProberCredentialsVolume mounts the secret prober validates its HTTP Basic Auth against. The UI
// re-reads the files on every request, so an in-place update of a mutable secret (the user's admin
// secret with local-file JMX auth) reaches it without a pod restart. The operator's own
// <cluster>-auth-active-admin is immutable and gets replaced instead, which kubelet never picks up;
// uiCredentialsSecretAnnotation rolls the pod in that case.
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
		SecurityContext:          uiContainerSecurityContext(),
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: v1.TerminationMessageReadFile,
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
