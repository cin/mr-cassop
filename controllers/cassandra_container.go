package controllers

import (
	"fmt"
	"strings"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/names"
	"github.com/cin/mr-cassop/controllers/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func cassandraContainer(cc *dbv1alpha1.CassandraCluster, dc dbv1alpha1.DC, restartChecksum checksumContainer, clientTLSSecret *v1.Secret) v1.Container {
	container := v1.Container{
		Name:            "cassandra",
		Image:           cc.Spec.Cassandra.Image,
		ImagePullPolicy: cc.Spec.Cassandra.ImagePullPolicy,
		Env: []v1.EnvVar{
			{
				Name: "POD_NAME",
				ValueFrom: &v1.EnvVarSource{
					FieldRef: &v1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.name"},
				},
			},
			{
				Name: "POD_UID",
				ValueFrom: &v1.EnvVarSource{
					FieldRef: &v1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.uid"},
				},
			},
			{
				Name: "POD_IP",
				ValueFrom: &v1.EnvVarSource{
					FieldRef: &v1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "status.podIP"},
				},
			},
			{
				Name:  "LOG_LEVEL",
				Value: cc.Spec.Cassandra.LogLevel,
			},
			{
				Name:  "CASSANDRA_CLUSTER_NAME",
				Value: cc.Name,
			},
			{
				Name:  "CASSANDRA_DC",
				Value: dc.Name,
			},
			{
				Name:  "CASSANDRA_LISTEN_ADDRESS",
				Value: "$(POD_IP)",
			},
			{
				Name:  "POD_RESTART_CHECKSUM", //used to force the statefulset to restart pods on some changes that need Cassandra restart
				Value: restartChecksum.checksum(),
			},
		},
		Args: []string{
			"bash",
			"-c",
			getCassandraRunCommand(cc, clientTLSSecret),
		},
		Resources:    cc.Spec.Cassandra.Resources,
		StartupProbe: cassandraStartupProbe(),
		LivenessProbe: &v1.Probe{
			ProbeHandler: v1.ProbeHandler{
				TCPSocket: &v1.TCPSocketAction{
					Port: intstr.FromString("cql"),
				},
			},
			InitialDelaySeconds: 60,
			TimeoutSeconds:      20,
			PeriodSeconds:       20,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		ReadinessProbe: &v1.Probe{
			ProbeHandler: v1.ProbeHandler{
				Exec: &v1.ExecAction{
					Command: []string{
						"bash",
						"-c",
						"source /etc/pods-config/${POD_NAME}_${POD_UID}.sh && " +
							fmt.Sprintf("http --check-status --timeout 2 GET %s/healthz/$CASSANDRA_BROADCAST_ADDRESS", names.ProberService(cc.Name)),
					},
				},
			},
			TimeoutSeconds:      20,
			PeriodSeconds:       10,
			SuccessThreshold:    2,
			FailureThreshold:    3,
			InitialDelaySeconds: 40,
		},
		Lifecycle: &v1.Lifecycle{
			PreStop: &v1.LifecycleHandler{
				Exec: &v1.ExecAction{
					Command: []string{
						"bash",
						"-c",
						"source /home/cassandra/.bashrc && nodetool drain",
					},
				},
			},
		},
		VolumeMounts: []v1.VolumeMount{
			cassandraDCConfigVolumeMount(),
			cassandraDataVolumeMount(),
			podsConfigVolumeMount(),
			authVolumeMount(),
		},
		Ports:                    cassandraContainerPorts(cc),
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: v1.TerminationMessageReadFile,
	}

	if cc.Spec.Cassandra.Monitoring.Enabled {
		if cc.Spec.Cassandra.Monitoring.Agent == dbv1alpha1.CassandraAgentTlp {
			container.VolumeMounts = append(container.VolumeMounts, prometheusVolumeMount())
		}
		container.Env = append(container.Env, v1.EnvVar{
			Name:  "JVM_EXTRA_OPTS",
			Value: getJavaAgent(cc.Spec.Cassandra.Monitoring.Agent),
		})
	}

	if cc.Spec.Cassandra.Persistence.Enabled && cc.Spec.Cassandra.Persistence.CommitLogVolume {
		container.VolumeMounts = append(container.VolumeMounts, commitLogVolumeMount())
	}

	if cc.Spec.Encryption.Server.InternodeEncryption != dbv1alpha1.InternodeEncryptionNone {
		container.VolumeMounts = append(container.VolumeMounts, cassandraServerTLSVolumeMount())
	}

	tlsArg := ""
	if cc.Spec.Encryption.Client.Enabled {
		container.VolumeMounts = append(container.VolumeMounts, cassandraClientTLSVolumeMount())
		tlsArg = "--ssl"
	}
	container.Env = append(container.Env, v1.EnvVar{
		Name:  "TLS_ARG",
		Value: tlsArg,
	})

	return container
}

const (
	// A node that is replacing another one or bootstrapping does not open CQL until it has joined the
	// ring, which takes minutes plus the time to stream its data. Kubernetes holds the liveness and
	// readiness probes until the startup probe succeeds, so the node isn't killed while it joins (#229).
	// The budget (period * failure threshold) is the longest a node may take to open CQL.
	startupProbePeriodSeconds    = 10
	startupProbeTimeoutSeconds   = 5
	startupProbeFailureThreshold = 360
)

func cassandraStartupProbe() *v1.Probe {
	return &v1.Probe{
		ProbeHandler: v1.ProbeHandler{
			TCPSocket: &v1.TCPSocketAction{
				Port: intstr.FromString("cql"),
			},
		},
		PeriodSeconds:    startupProbePeriodSeconds,
		TimeoutSeconds:   startupProbeTimeoutSeconds,
		SuccessThreshold: 1,
		FailureThreshold: startupProbeFailureThreshold,
	}
}

func cassandraContainerPorts(cc *dbv1alpha1.CassandraCluster) []v1.ContainerPort {
	containerPorts := []v1.ContainerPort{
		{
			Name:          "intra",
			ContainerPort: dbv1alpha1.IntraPort,
			Protocol:      v1.ProtocolTCP,
			HostPort:      0,
		},
		{
			Name:          "tls",
			ContainerPort: dbv1alpha1.TlsPort,
			Protocol:      v1.ProtocolTCP,
			HostPort:      0,
		},
		{
			Name:          "jmx",
			ContainerPort: dbv1alpha1.JmxPort,
			Protocol:      v1.ProtocolTCP,
			HostPort:      0,
		},
		{
			Name:          "cql",
			ContainerPort: dbv1alpha1.CqlPort,
			Protocol:      v1.ProtocolTCP,
			HostPort:      0,
		},
	}

	if cc.Spec.HostPort.Enabled {
		portsToExpose := cc.Spec.HostPort.Ports
		// jmx port is required to make jolokia work
		// intra needed to allow cassandra nodes to talk to each other
		portsToExpose = append(portsToExpose, "jmx", "intra")

		if cc.Spec.Encryption.Server.InternodeEncryption != dbv1alpha1.InternodeEncryptionNone {
			portsToExpose = append(portsToExpose, "tls")
		}

		for i, port := range containerPorts {
			if util.Contains(portsToExpose, containerPorts[i].Name) {
				containerPorts[i].HostPort = port.ContainerPort
			}
		}
	}

	return containerPorts
}

// replaceAddressScript sets replace_address for the Cassandra command when the pod has a previous IP
// and its data volume holds no completed bootstrap. replace_address_first_boot is only meaningful then:
// with a completed bootstrap Cassandra ignores it (CASSANDRA-7356) and handles an IP change itself via
// the node's host ID, and a node asked to replace its own live address can crash-loop (#160).
// system.local sstables exist before a bootstrap completes, so the check reads the recorded state: the
// bootstrapped cell with the newest timestamp across all of them, since an older sstable can still hold
// COMPLETED after the state changed (e.g. DECOMMISSIONED) until compaction merges them.
// A failing sstabledump counts as not bootstrapped, which keeps the replace.
const replaceAddressScript = `bootstrap_completed() {
  local sstable cell value ts nanos newest_nanos=0 newest_value=""
  for sstable in /var/lib/cassandra/data/system/local-*/*-Data.db; do
    [[ -e "${sstable}" ]] || continue
    cell=$("${CASSANDRA_HOME}/tools/bin/sstabledump" "${sstable}" 2>/dev/null | grep '"name" : "bootstrapped"')
    value=$(sed -n 's/.*"value" : "\([A-Z_]*\)".*/\1/p' <<< "${cell}")
    ts=$(sed -n 's/.*"tstamp" : "\([^"]*\)".*/\1/p' <<< "${cell}")
    [[ -n "${ts}" ]] || continue
    nanos=$(date -u -d "${ts}" +%s%N 2>/dev/null) || continue
    if (( nanos > newest_nanos )); then
      newest_nanos=${nanos}
      newest_value=${value}
    fi
  done
  [[ "${newest_value}" == "COMPLETED" ]]
}
replace_address=""
old_ip=$CASSANDRA_NODE_PREVIOUS_IP
if [[ "${old_ip}" == "" ]]; then
  echo not using replace address since the node IP hasn\'t changed
elif bootstrap_completed; then
  echo node already bootstrapped - not adding replace address
else
  replace_address="-Dcassandra.replace_address_first_boot=${old_ip}"
  echo replacing old Cassandra node - adding arg $replace_address
fi`

func getCassandraRunCommand(cc *dbv1alpha1.CassandraCluster, clientTLSSecret *v1.Secret) string {
	var args []string
	if cc.Spec.Cassandra.PurgeGossip != nil && *cc.Spec.Cassandra.PurgeGossip {
		args = append(args, "rm -rf /var/lib/cassandra/data/system/peers*")
	}

	args = append(args,
		"echo \"prefer_local=true\" >> $CASSANDRA_CONF/cassandra-rackdc.properties",
		"cp /etc/cassandra-configmaps/* $CASSANDRA_CONF/",
		"cat /etc/cassandra-configmaps/jvm.options >> $CASSANDRA_CONF/jvm-server.options",
		"source /etc/pods-config/${POD_NAME}_${POD_UID}.sh",
		replaceAddressScript,
	)

	if cc.Spec.Encryption.Client.Enabled {
		args = append(args,
			"mkdir -p /home/cassandra/.cassandra/",
			"cp /etc/cassandra-auth-config/nodetool-ssl.properties /home/cassandra/.cassandra/",
			"cp /etc/cassandra-auth-config/cqlshrc /home/cassandra/.cassandra/")
	}

	cassandraRunCommand := []string{
		"/usr/local/bin/docker-entrypoint.sh -f -R",
		fmt.Sprintf("-Dcassandra.jmx.remote.port=%d", dbv1alpha1.JmxPort),
		fmt.Sprintf("-Dcom.sun.management.jmxremote.rmi.port=%d", dbv1alpha1.JmxPort),
		"-Djava.rmi.server.hostname=$POD_IP",
		"-Dcom.sun.management.jmxremote.authenticate=true",
		"-Dcassandra.storagedir=/var/lib/cassandra",
		"${replace_address}",
	}

	if cc.Spec.JMXAuth == jmxAuthenticationLocalFiles {
		cassandraRunCommand = append(cassandraRunCommand,
			"-Dcom.sun.management.jmxremote.password.file=/etc/cassandra-auth-config/jmxremote.password",
			"-Dcom.sun.management.jmxremote.access.file=/etc/cassandra-auth-config/jmxremote.access",
		)
	}

	if cc.Spec.JMXAuth == jmxAuthenticationInternal {
		cassandraRunCommand = append(cassandraRunCommand,
			"-Dcassandra.jmx.remote.login.config=CassandraLogin",
			"-Djava.security.auth.login.config=$CASSANDRA_HOME/conf/cassandra-jaas.config",
			"-Dcassandra.jmx.authorizer=org.apache.cassandra.auth.jmx.AuthorizationProxy",
		)
	}

	if cc.Spec.Encryption.Client.Enabled {
		cassandraRunCommand = append(cassandraRunCommand,
			"-Dcom.sun.management.jmxremote.ssl=true",
			fmt.Sprintf("-Dcom.sun.management.jmxremote.ssl.need.client.auth=%v", *cc.Spec.Encryption.Client.RequireClientAuth),
			"-Dcom.sun.management.jmxremote.registry.ssl=true",
			tlsJVMArgs(cc, clientTLSSecret),
		)
	}

	args = append(args, strings.Join(cassandraRunCommand, " "))

	return strings.Join(args, "\n")
}

func getJavaAgent(agent string) string {
	javaAgent := ""
	if agent == dbv1alpha1.CassandraAgentTlp {
		javaAgent = fmt.Sprintf("-javaagent:/prometheus/jmx_prometheus_javaagent.jar=%d:/prometheus/prometheus.yaml", dbv1alpha1.TlpPort)
	}
	return javaAgent
}

func cassandraDataVolumeMount() v1.VolumeMount {
	return v1.VolumeMount{
		Name:      "data",
		MountPath: "/var/lib/cassandra",
	}
}

func podsConfigVolumeMount() v1.VolumeMount {
	return v1.VolumeMount{
		Name:      "pods-config",
		MountPath: "/etc/pods-config",
	}
}

func authVolumeMount() v1.VolumeMount {
	return v1.VolumeMount{
		Name:      "auth-config",
		MountPath: "/etc/cassandra-auth-config/",
	}
}

func cassandraServerTLSVolumeMount() v1.VolumeMount {
	return v1.VolumeMount{
		Name:      cassandraServerTLSVolumeName,
		MountPath: cassandraServerTLSDir,
	}
}

func cassandraClientTLSVolumeMount() v1.VolumeMount {
	return v1.VolumeMount{
		Name:      cassandraClientTLSVolumeName,
		MountPath: cassandraClientTLSDir,
	}
}
