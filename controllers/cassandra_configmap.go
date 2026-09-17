package controllers

import (
	"context"
	"fmt"
	"strings"

	"github.com/cin/mr-cassop/controllers/events"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/labels"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/util"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"
)

const (
	authReadConsistencyLevelKey       = "auth_read_consistency_level"
	temporaryAuthReadConsistencyLevel = "LOCAL_ONE"
)

func (r *CassandraClusterReconciler) reconcileCassandraConfigMap(ctx context.Context, cc *v1alpha1.CassandraCluster, restartChecksum checksumContainer) error {
	operatorCM, err := r.getConfigMap(ctx, names.OperatorCassandraConfigCM(), r.Cfg.Namespace)
	if err != nil {
		return err
	}

	desiredCM := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.ConfigMap(cc.Name),
			Namespace: cc.Namespace,
			Labels:    labels.CombinedComponentLabels(cc, v1alpha1.CassandraClusterComponentCassandra),
		},
	}

	data := util.MergeMap(make(map[string]string), operatorCM.Data)

	cassandraYaml := make(map[string]interface{})
	err = yaml.Unmarshal([]byte(data["cassandra.yaml"]), &cassandraYaml)
	if err != nil {
		return errors.Wrap(err, "can't unmarshal 'cassandra.yaml'")
	}

	if err = r.applyTemporaryAuthRelaxation(ctx, cc, cassandraYaml); err != nil {
		return err
	}

	// override user provided configs
	if len(cc.Spec.Cassandra.ConfigOverrides) > 0 {
		overrides := make(map[string]interface{})
		err = yaml.Unmarshal([]byte(cc.Spec.Cassandra.ConfigOverrides), &overrides)
		if err != nil {
			errMsg := fmt.Sprintf("Invalid Cassandra configs. Not valid YAML: %s", err.Error())
			r.Log.Warn(errMsg)
			r.Events.Warning(cc, events.CassandraConfigInvalid, errMsg)
		} else {
			for key, value := range overrides {
				cassandraYaml[key] = value
			}
		}
	}

	if cc.Spec.Cassandra.Persistence.Enabled && cc.Spec.Cassandra.Persistence.CommitLogVolume {
		cassandraYaml["commitlog_directory"] = cassandraCommitLogDir
	}

	if cc.Spec.Encryption.Server.InternodeEncryption != v1alpha1.InternodeEncryptionNone {
		encryptionOptions := make(map[string]interface{})
		serverTLSSecret, err := r.getSecret(ctx, cc.Spec.Encryption.Server.NodeTLSSecret.Name, cc.Namespace)
		if err != nil {
			return err
		}

		encryptionOptions["internode_encryption"] = cc.Spec.Encryption.Server.InternodeEncryption
		encryptionOptions["require_client_auth"] = cc.Spec.Encryption.Server.RequireClientAuth
		encryptionOptions["require_endpoint_verification"] = cc.Spec.Encryption.Server.RequireEndpointVerification
		encryptionOptions["keystore"] = fmt.Sprintf("%s/%s", cassandraServerTLSDir, cc.Spec.Encryption.Server.NodeTLSSecret.KeystoreFileKey)
		encryptionOptions["keystore_password"] = strings.TrimRight(string(serverTLSSecret.Data[cc.Spec.Encryption.Server.NodeTLSSecret.KeystorePasswordKey]), "\r\n")
		encryptionOptions["truststore"] = fmt.Sprintf("%s/%s", cassandraServerTLSDir, cc.Spec.Encryption.Server.NodeTLSSecret.TruststoreFileKey)
		encryptionOptions["truststore_password"] = strings.TrimRight(string(serverTLSSecret.Data[cc.Spec.Encryption.Server.NodeTLSSecret.TruststorePasswordKey]), "\r\n")
		encryptionOptions["protocol"] = cc.Spec.Encryption.Server.Protocol
		encryptionOptions["algorithm"] = cc.Spec.Encryption.Server.Algorithm
		encryptionOptions["store_type"] = cc.Spec.Encryption.Server.StoreType
		encryptionOptions["cipher_suites"] = cc.Spec.Encryption.Server.CipherSuites
		cassandraYaml["server_encryption_options"] = encryptionOptions
	}

	if cc.Spec.Encryption.Client.Enabled {

		encryptionOptions := make(map[string]interface{})

		clientTLSSecret, err := r.getSecret(ctx, cc.Spec.Encryption.Client.NodeTLSSecret.Name, cc.Namespace)
		if err != nil {
			return err
		}

		encryptionOptions["enabled"] = cc.Spec.Encryption.Client.Enabled
		encryptionOptions["optional"] = cc.Spec.Encryption.Client.Optional
		encryptionOptions["require_client_auth"] = cc.Spec.Encryption.Client.RequireClientAuth
		encryptionOptions["keystore"] = fmt.Sprintf("%s/%s", cassandraClientTLSDir, cc.Spec.Encryption.Client.NodeTLSSecret.KeystoreFileKey)
		encryptionOptions["keystore_password"] = strings.TrimRight(string(clientTLSSecret.Data[cc.Spec.Encryption.Client.NodeTLSSecret.KeystorePasswordKey]), "\r\n")
		encryptionOptions["truststore"] = fmt.Sprintf("%s/%s", cassandraClientTLSDir, cc.Spec.Encryption.Client.NodeTLSSecret.TruststoreFileKey)
		encryptionOptions["truststore_password"] = strings.TrimRight(string(clientTLSSecret.Data[cc.Spec.Encryption.Client.NodeTLSSecret.TruststorePasswordKey]), "\r\n")
		encryptionOptions["protocol"] = cc.Spec.Encryption.Client.Protocol
		encryptionOptions["algorithm"] = cc.Spec.Encryption.Client.Algorithm
		encryptionOptions["store_type"] = cc.Spec.Encryption.Client.StoreType
		encryptionOptions["cipher_suites"] = cc.Spec.Encryption.Client.CipherSuites
		cassandraYaml["client_encryption_options"] = encryptionOptions
	}

	cassandraYamlBytes, err := yaml.Marshal(cassandraYaml)
	if err != nil {
		return errors.Wrap(err, "can't marshal 'cassandra.yaml'")
	}

	restartChecksum["cassandra.yaml"] = string(cassandraYamlBytes) //to restart cassandra pods on change
	data["cassandra.yaml"] = string(cassandraYamlBytes)

	data["jvm.options"] = "### OVERRIDES PROVIDED BY THE USER\n\n"
	if len(cc.Spec.Cassandra.JVMOptions) > 0 {
		data["jvm.options"] += strings.Join(cc.Spec.Cassandra.JVMOptions, "\n")
		data["jvm.options"] += "\n"
	}
	restartChecksum["jvm.options"] = data["jvm.options"] //to restart cassandra pods on change

	desiredCM.Data = data

	if err := controllerutil.SetControllerReference(cc, desiredCM, r.Scheme); err != nil {
		return errors.Wrap(err, "Cannot set controller reference")
	}

	return r.reconcileConfigMap(ctx, desiredCM)
}

// applyTemporaryAuthRelaxation temporarily sets auth_read_consistency_level to a level
// achievable by a single node (LOCAL_ONE) while this cluster is bootstrapping against
// Cassandra data PVCs that already existed (see createClusterAdminSecrets). Without this,
// the vendored default (effectively Cassandra's own QUORUM/LOCAL_QUORUM default) can never be
// satisfied until multiple nodes are up, but those other nodes are themselves waiting on the
// first node's readiness probe to pass first - a permanent deadlock (issue #150).
//
// It must run before user configOverrides are merged in, so a user's own explicit
// auth_read_consistency_level is never masked, and it self-clears once every DC is ready so the
// relaxation never outlives the one-time bootstrap it exists for.
func (r *CassandraClusterReconciler) applyTemporaryAuthRelaxation(ctx context.Context, cc *v1alpha1.CassandraCluster, cassandraYaml map[string]interface{}) error {
	if !cc.Status.RecreatedFromExistingPVCs {
		return nil
	}

	unready, err := r.unreadyDCs(ctx, cc)
	if err != nil {
		return errors.Wrap(err, "failed to check DC readiness for temporary auth relaxation")
	}

	if len(unready) > 0 {
		r.Log.Infof("Cluster is bootstrapping against pre-existing PVCs and DCs %q are not ready yet; "+
			"temporarily setting %s to %s", unready, authReadConsistencyLevelKey, temporaryAuthReadConsistencyLevel)
		cassandraYaml[authReadConsistencyLevelKey] = temporaryAuthReadConsistencyLevel
		return nil
	}

	r.Log.Info("Cluster bootstrapped from pre-existing PVCs is now fully ready; " +
		"no longer relaxing auth_read_consistency_level")
	cc.Status.RecreatedFromExistingPVCs = false
	return nil
}

func cassandraConfigVolume(cc *v1alpha1.CassandraCluster) v1.Volume {
	return v1.Volume{
		Name: "config",
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{
					Name: names.ConfigMap(cc.Name),
				},
				DefaultMode: ptr.To[int32](v1.ConfigMapVolumeSourceDefaultMode),
			},
		},
	}
}

func cassandraDCConfigVolumeMount() v1.VolumeMount {
	return v1.VolumeMount{
		Name:      "config",
		MountPath: "/etc/cassandra-configmaps",
	}
}
