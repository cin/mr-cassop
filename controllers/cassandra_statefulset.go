package controllers

import (
	"context"
	"fmt"
	"strings"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/compare"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/util"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *CassandraClusterReconciler) reconcileDCStatefulSet(ctx context.Context, cc *dbv1alpha1.CassandraCluster, dc dbv1alpha1.DC, restartChecksum checksumContainer) error {
	var err error

	if cc.Spec.Encryption.Server.InternodeEncryption != dbv1alpha1.InternodeEncryptionNone {
		if _, err := r.reconcileNodeTLSSecret(ctx, cc, restartChecksum, serverNode); err != nil {
			return err
		}
	}

	clientTLSSecret := &v1.Secret{}
	if cc.Spec.Encryption.Client.Enabled {
		clientTLSSecret, err = r.reconcileNodeTLSSecret(ctx, cc, restartChecksum, clientNode)
		if err != nil {
			return err
		}
	}

	if cc.Spec.HostPort.Enabled && util.Contains(cc.Spec.HostPort.Ports, "cql") && !cc.Spec.Encryption.Client.Enabled {
		warnMsg := fmt.Sprintf("Exposing cql port (%d) as hostPort without client encryption enabled is not secure", dbv1alpha1.CqlPort)
		r.Events.Warning(cc, events.EventInsecureSetup, warnMsg)
		r.Log.Warn(warnMsg)
	}
	desiredSts := cassandraStatefulSet(cc, dc, restartChecksum, clientTLSSecret)

	if err = controllerutil.SetControllerReference(cc, desiredSts, r.Scheme); err != nil {
		return errors.Wrap(err, "Cannot set controller reference")
	}

	actualSts := &appsv1.StatefulSet{}
	err = r.Get(ctx, types.NamespacedName{Name: names.DC(cc.Name, dc.Name), Namespace: cc.Namespace}, actualSts)
	if err != nil && apierrors.IsNotFound(err) {
		r.Log.Infof("Creating cassandra statefulset for DC %q", dc.Name)
		err = r.Create(ctx, desiredSts)
		if err != nil {
			return errors.Wrap(err, "Failed to create statefulset")
		}
	} else if err != nil {
		return errors.Wrap(err, "Failed to get statefulset")
	} else {
		desiredSts.Annotations = actualSts.Annotations
		// the pod selector is immutable once set, so always enforce the same as existing
		desiredSts.Spec.Selector = actualSts.Spec.Selector
		desiredSts.Spec.Template.Labels = actualSts.Spec.Template.Labels
		// annotation can be used by things like `kubectl rollout sts restart` so don't overwrite it
		desiredSts.Spec.Template.Annotations = util.MergeMap(actualSts.Spec.Template.Annotations, desiredSts.Spec.Template.Annotations)
		// scaling is handled by the scaling logic
		desiredSts.Spec.Replicas = actualSts.Spec.Replicas
		// podManagementPolicy is immutable once the statefulset exists - migrateToOrderedReady
		// (below) is the only thing allowed to change it, via a delete+recreate, never a plain Update.
		desiredSts.Spec.PodManagementPolicy = actualSts.Spec.PodManagementPolicy
		if !compare.EqualStatefulSet(desiredSts, actualSts) {
			r.Log.Info("Updating cassandra statefulset")
			r.Log.Debug(compare.DiffStatefulSet(actualSts, desiredSts))
			actualSts.Spec = desiredSts.Spec
			actualSts.Labels = desiredSts.Labels
			if err = r.Update(ctx, actualSts); err != nil {
				return errors.Wrap(err, "failed to update statefulset")
			}
		} else {
			r.Log.Debugf("No updates to cassandra statefulset")
		}

		if err = r.migrateToOrderedReady(ctx, cc, dc, actualSts); err != nil {
			return err
		}
	}

	return nil
}

// migrateToOrderedReady flips a DC's statefulset from the fast-bootstrap ParallelPodManagement
// policy to OrderedReady once the DC has gone fully ready for the first time. It's a one-shot,
// idempotent migration: once the recreated statefulset reports OrderedReady, this is a no-op on
// every later reconcile (checked via sts.Spec.PodManagementPolicy itself - no separate status
// field needed).
//
// ParallelPodManagement lets Kubernetes replace every pod in the statefulset concurrently on any
// spec change (image bump, config change, ...); on an already-running, already-quorate cluster
// that can drop `system_auth` below LOCAL_QUORUM on every node at once, deadlocking the DC (#155).
// OrderedReady caps updates to one pod at a time - but it's immutable on an existing statefulset,
// so switching to it requires deleting the object and recreating it with the same spec except
// that one field. Deleting a statefulset normally cascades to delete the pods it owns, so this
// first strips its controller ownerReference from each of them (see orphanStatefulSetPods) -
// deliberately not `client.PropagationPolicy(metav1.DeletePropagationOrphan)`, since that instead
// relies on the garbage collector controller to strip those same ownerReferences asynchronously,
// which envtest doesn't run at all and even a real cluster's GC controller does on its own
// schedule - either way leaving the statefulset's name stuck "object is being deleted" for a
// window in which the recreate below would fail. Stripping ownerReferences synchronously here
// means nothing is left for any deletion to cascade to, so a plain Delete is immediate and safe.
func (r *CassandraClusterReconciler) migrateToOrderedReady(ctx context.Context, cc *dbv1alpha1.CassandraCluster, dc dbv1alpha1.DC, sts *appsv1.StatefulSet) error {
	if sts.Spec.PodManagementPolicy != appsv1.ParallelPodManagement {
		return nil // already migrated
	}

	if dc.Replicas == nil || *dc.Replicas == 0 || sts.Status.ReadyReplicas != *dc.Replicas {
		return nil // only migrate once the DC's initial (fast, parallel) bootstrap has completed
	}

	r.Log.Infof("DC %q of cluster %q is fully ready for the first time; migrating its statefulset "+
		"from Parallel to OrderedReady pod management so future updates replace one pod at a time "+
		"instead of all at once", dc.Name, cc.Name)

	if err := r.orphanStatefulSetPods(ctx, sts); err != nil {
		return errors.Wrap(err, "failed to detach statefulset's pods ahead of OrderedReady migration")
	}

	recreated := sts.DeepCopy()
	recreated.ResourceVersion = ""
	recreated.UID = ""
	recreated.CreationTimestamp = metav1.Time{}
	recreated.Spec.PodManagementPolicy = appsv1.OrderedReadyPodManagement

	if err := r.Delete(ctx, sts); err != nil {
		return errors.Wrap(err, "failed to delete statefulset for OrderedReady migration")
	}

	oldStatus := sts.Status
	if err := r.Create(ctx, recreated); err != nil {
		return errors.Wrap(err, "failed to recreate statefulset with OrderedReady pod management")
	}

	// Create ignores .status (it's a subresource), so the recreated object starts at zero
	// ReadyReplicas even though the pods it just adopted are already running and ready. Seed it
	// with the old object's status immediately so nothing (this cluster's own readiness check,
	// prober, ...) briefly sees the DC as unready right after a migration that shouldn't have
	// disturbed any pod. A real cluster's statefulset controller would recompute this from actual
	// pod state within moments regardless; this just closes that gap immediately instead of
	// waiting on it.
	recreated.Status = oldStatus
	if err := r.Status().Update(ctx, recreated); err != nil {
		return errors.Wrap(err, "failed to seed recreated statefulset's status")
	}

	return nil
}

// orphanStatefulSetPods removes sts's controller ownerReference from every pod it owns, via a
// merge patch scoped to just that field so it can't conflict with kubelet's own frequent status
// updates to the same pods. The pods are left running completely untouched - only their metadata
// changes - and get adopted back (via label selector match, not ownerReference) by the
// recreated statefulset moments later once migrateToOrderedReady creates it.
func (r *CassandraClusterReconciler) orphanStatefulSetPods(ctx context.Context, sts *appsv1.StatefulSet) error {
	pods := &v1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(sts.Namespace), client.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
		return errors.Wrap(err, "failed to list statefulset's pods")
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		remaining := make([]metav1.OwnerReference, 0, len(pod.OwnerReferences))
		for _, ref := range pod.OwnerReferences {
			if ref.UID != sts.UID {
				remaining = append(remaining, ref)
			}
		}
		if len(remaining) == len(pod.OwnerReferences) {
			continue // not owned by sts - nothing to strip
		}

		patch := client.MergeFrom(pod.DeepCopy())
		pod.OwnerReferences = remaining
		if err := r.Patch(ctx, pod, patch); err != nil {
			return errors.Wrapf(err, "failed to detach pod %q from statefulset", pod.Name)
		}
	}

	return nil
}

func cassandraStatefulSet(cc *dbv1alpha1.CassandraCluster, dc dbv1alpha1.DC, restartChecksum checksumContainer, clientTLSSecret *v1.Secret) *appsv1.StatefulSet {
	stsLabels := labels.CombinedComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra)
	stsLabels = labels.WithDCLabel(stsLabels, dc.Name)
	if cc.Spec.Cassandra.Monitoring.Agent == dbv1alpha1.CassandraAgentTlp {
		stsLabels["environment"] = cc.Namespace
		stsLabels["datacenter"] = dc.Name
		stsLabels["app.kubernetes.io/name"] = cc.Name
	}
	desiredSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.DC(cc.Name, dc.Name),
			Namespace: cc.Namespace,
			Labels:    stsLabels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName:         names.DCService(cc.Name, dc.Name),
			Replicas:            dc.Replicas,
			Selector:            &metav1.LabelSelector{MatchLabels: stsLabels},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				// RollingUpdate is deliberately left nil: the API server only defaults partition and
				// maxUnavailable when it's set, and maxUnavailable's default depends on the
				// MaxUnavailableStatefulSet feature gate (on in 1.35.0-1.35.3 and 1.37+, off otherwise).
				// Leaving it nil keeps desiredSts equal to what's read back on every k8s version, and
				// keeps rollouts at one Cassandra pod at a time.
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
			RevisionHistoryLimit: ptr.To[int32](10),
			// Set explicitly to the API server's default (Retain/Retain) so desiredSts matches what's
			// read back; leaving it nil caused a spurious diff on every reconcile.
			// WhenDeleted: Retain keeps data volumes if the statefulset is deleted.
			// WhenScaled: Retain is NOT a data-safety measure for Cassandra: the operator only scales
			// down after the node is decommissioned, and a decommissioned node's data is marked
			// DECOMMISSIONED. If the statefulset later scales back up, the new pod reattaches that
			// PVC and Cassandra refuses to start unless cassandra.override_decommission is set.
			// WhenScaled: Delete is the correct setting and is left as a follow-up change.
			PersistentVolumeClaimRetentionPolicy: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
				WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: stsLabels,
					Annotations: map[string]string{
						// https://kubernetes.io/docs/reference/labels-annotations-taints/#kubectl-kubernetes-io-default-container
						"kubectl.kubernetes.io/default-container": "cassandra",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						cassandraContainer(cc, dc, restartChecksum, clientTLSSecret),
						icarusContainer(cc),
					},
					InitContainers: []v1.Container{
						privilegedInitContainer(cc),
						maintenanceContainer(cc),
						initContainer(cc),
					},
					ImagePullSecrets: imagePullSecrets(cc),
					Volumes: []v1.Volume{
						maintenanceVolume(cc),
						cassandraConfigVolume(cc),
						podsConfigVolume(cc),
						authVolume(cc),
					},
					ServiceAccountName:            names.CassandraServiceAccount(cc.Name),
					Affinity:                      dc.Affinity,
					Tolerations:                   dc.Tolerations,
					RestartPolicy:                 v1.RestartPolicyAlways,
					TerminationGracePeriodSeconds: cc.Spec.Cassandra.TerminationGracePeriodSeconds,
					DNSPolicy:                     v1.DNSClusterFirst,
					SecurityContext:               &v1.PodSecurityContext{},
				},
			},
		},
	}

	if cc.Spec.Cassandra.Monitoring.Enabled {
		if cc.Spec.Cassandra.Monitoring.Agent == dbv1alpha1.CassandraAgentTlp {
			desiredSts.Spec.Template.Spec.Volumes = append(desiredSts.Spec.Template.Spec.Volumes, prometheusVolume(cc))
		}
	}

	if cc.Spec.Cassandra.Persistence.Enabled {
		desiredSts.Spec.VolumeClaimTemplates = cassandraVolumeClaims(cc)
	} else {
		desiredSts.Spec.Template.Spec.Volumes = append(desiredSts.Spec.Template.Spec.Volumes, emptyDirDataVolume())
	}

	if cc.Spec.Encryption.Server.InternodeEncryption != dbv1alpha1.InternodeEncryptionNone {
		desiredSts.Spec.Template.Spec.Volumes = append(desiredSts.Spec.Template.Spec.Volumes, cassandraServerTLSVolume(cc))
	}

	if cc.Spec.Encryption.Client.Enabled {
		desiredSts.Spec.Template.Spec.Volumes = append(desiredSts.Spec.Template.Spec.Volumes, cassandraClientTLSVolume(cc))
	}

	if cc.Spec.TopologySpreadByZone != nil && *cc.Spec.TopologySpreadByZone {
		desiredSts.Spec.Template.Spec.TopologySpreadConstraints = []v1.TopologySpreadConstraint{
			{
				MaxSkew:           1,
				TopologyKey:       v1.LabelTopologyZone,
				WhenUnsatisfiable: v1.ScheduleAnyway,
				LabelSelector:     metav1.SetAsLabelSelector(labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra)),
			},
		}
	}

	return desiredSts
}

func cassandraVolumeClaims(cc *dbv1alpha1.CassandraCluster) []v1.PersistentVolumeClaim {
	pvcLabels := labels.ComponentLabels(cc, dbv1alpha1.CassandraClusterComponentCassandra)
	if cc.Spec.Cassandra.Persistence.Labels != nil {
		pvcLabels = util.MergeMap(cc.Spec.Cassandra.Persistence.Labels, pvcLabels)
	}
	volumeClaims := []v1.PersistentVolumeClaim{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "data",
				Labels:      pvcLabels,
				Annotations: cc.Spec.Cassandra.Persistence.Annotations,
			},
			Spec: cc.Spec.Cassandra.Persistence.DataVolumeClaimSpec,
		},
	}

	if cc.Spec.Cassandra.Persistence.CommitLogVolume {
		volumeClaims = append(volumeClaims, v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "commitlog",
				Labels:      pvcLabels,
				Annotations: cc.Spec.Cassandra.Persistence.Annotations,
			},
			Spec: cc.Spec.Cassandra.Persistence.CommitLogVolumeClaimSpec,
		})
	}

	return volumeClaims
}

func imagePullSecrets(cc *dbv1alpha1.CassandraCluster) []v1.LocalObjectReference {
	return []v1.LocalObjectReference{
		{
			Name: cc.Spec.ImagePullSecretName,
		},
	}
}

func emptyDirDataVolume() v1.Volume {
	return v1.Volume{
		Name: "data",
		VolumeSource: v1.VolumeSource{
			EmptyDir: &v1.EmptyDirVolumeSource{},
		},
	}
}

func commitLogVolumeMount() v1.VolumeMount {
	return v1.VolumeMount{
		Name:      "commitlog",
		MountPath: cassandraCommitLogDir,
	}
}

func maintenanceVolume(cc *dbv1alpha1.CassandraCluster) v1.Volume {
	return v1.Volume{
		Name: "maintenance-config",
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{
					Name: names.MaintenanceConfigMap(cc.Name),
				},
				DefaultMode: ptr.To[int32](0700),
			},
		},
	}
}

func podsConfigVolume(cc *dbv1alpha1.CassandraCluster) v1.Volume {
	return v1.Volume{
		Name: "pods-config",
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{
					Name: names.PodsConfigConfigmap(cc.Name),
				},
				DefaultMode: ptr.To[int32](v1.ConfigMapVolumeSourceDefaultMode),
			},
		},
	}
}

func authVolume(cc *dbv1alpha1.CassandraCluster) v1.Volume {
	items := []v1.KeyToPath{
		{
			Key:  dbv1alpha1.CassandraOperatorAdminRole,
			Path: dbv1alpha1.CassandraOperatorAdminRole,
		},
		{
			Key:  dbv1alpha1.CassandraOperatorAdminPassword,
			Path: dbv1alpha1.CassandraOperatorAdminPassword,
		},
		{
			Key:  "icarus-jmx",
			Path: "icarus-jmx",
		},
	}

	if cc.Spec.Encryption.Client.Enabled {
		items = append(items,
			v1.KeyToPath{
				Key:  "nodetool-ssl.properties",
				Path: "nodetool-ssl.properties",
			},
			v1.KeyToPath{
				Key:  "cqlshrc",
				Path: "cqlshrc",
			},
		)
	}

	volume := v1.Volume{
		Name: "auth-config",
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: names.AdminAuthConfigSecret(cc.Name),
				Items:      items,

				DefaultMode: ptr.To[int32](v1.SecretVolumeSourceDefaultMode),
			},
		},
	}

	if cc.Spec.JMXAuth == jmxAuthenticationLocalFiles {
		volume.VolumeSource.Secret.Items = append(volume.VolumeSource.Secret.Items, v1.KeyToPath{
			Key:  "jmxremote.password",
			Path: "jmxremote.password",
		})
		volume.VolumeSource.Secret.Items = append(volume.VolumeSource.Secret.Items, v1.KeyToPath{
			Key:  "jmxremote.access",
			Path: "jmxremote.access",
		})
	}

	return volume
}

func cassandraServerTLSVolume(cc *dbv1alpha1.CassandraCluster) v1.Volume {
	return v1.Volume{
		Name: cassandraServerTLSVolumeName,
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: cc.Spec.Encryption.Server.NodeTLSSecret.Name,
				Items: []v1.KeyToPath{
					{
						Key:  cc.Spec.Encryption.Server.NodeTLSSecret.KeystoreFileKey,
						Path: cc.Spec.Encryption.Server.NodeTLSSecret.KeystoreFileKey,
					},
					{
						Key:  cc.Spec.Encryption.Server.NodeTLSSecret.TruststoreFileKey,
						Path: cc.Spec.Encryption.Server.NodeTLSSecret.TruststoreFileKey,
					},
				},
				DefaultMode: ptr.To[int32](v1.SecretVolumeSourceDefaultMode),
			},
		},
	}
}

func cassandraClientTLSVolume(cc *dbv1alpha1.CassandraCluster) v1.Volume {
	return v1.Volume{
		Name: cassandraClientTLSVolumeName,
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: cc.Spec.Encryption.Client.NodeTLSSecret.Name,
				Items: []v1.KeyToPath{
					{
						Key:  cc.Spec.Encryption.Client.NodeTLSSecret.CACrtFileKey,
						Path: cc.Spec.Encryption.Client.NodeTLSSecret.CACrtFileKey,
					},
					{
						Key:  cc.Spec.Encryption.Client.NodeTLSSecret.CrtFileKey,
						Path: cc.Spec.Encryption.Client.NodeTLSSecret.CrtFileKey,
					},
					{
						Key:  cc.Spec.Encryption.Client.NodeTLSSecret.FileKey,
						Path: cc.Spec.Encryption.Client.NodeTLSSecret.FileKey,
					},
					{
						Key:  cc.Spec.Encryption.Client.NodeTLSSecret.KeystoreFileKey,
						Path: cc.Spec.Encryption.Client.NodeTLSSecret.KeystoreFileKey,
					},
					{
						Key:  cc.Spec.Encryption.Client.NodeTLSSecret.TruststoreFileKey,
						Path: cc.Spec.Encryption.Client.NodeTLSSecret.TruststoreFileKey,
					},
				},
				DefaultMode: ptr.To[int32](v1.SecretVolumeSourceDefaultMode),
			},
		},
	}
}

func tlsJVMArgs(cc *dbv1alpha1.CassandraCluster, clientTLSSecret *v1.Secret) string {
	return fmt.Sprintf("-Djavax.net.ssl.keyStore=%s/%s -Djavax.net.ssl.keyStorePassword=%s -Djavax.net.ssl.trustStore=%s/%s -Djavax.net.ssl.trustStorePassword=%s",
		cassandraClientTLSDir, cc.Spec.Encryption.Client.NodeTLSSecret.KeystoreFileKey, strings.TrimRight(string(clientTLSSecret.Data[cc.Spec.Encryption.Client.NodeTLSSecret.KeystorePasswordKey]), "\r\n"),
		cassandraClientTLSDir, cc.Spec.Encryption.Client.NodeTLSSecret.TruststoreFileKey, strings.TrimRight(string(clientTLSSecret.Data[cc.Spec.Encryption.Client.NodeTLSSecret.TruststorePasswordKey]), "\r\n"))
}
