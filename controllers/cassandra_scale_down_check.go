package controllers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/ptr"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/cql"
	"github.com/cin/mr-cassop/controllers/events"

	"github.com/pkg/errors"
)

const (
	simpleStrategyRFOption  = "replication_factor"
	simpleStrategyClassName = "SimpleStrategy"
)

var errScaleDownBlocked = errors.New("scale-down blocked by keyspace replication")

// checkScaleDownNotBlockedByReplication returns errScaleDownBlocked, after emitting a warning event that
// names the keyspaces, when Cassandra would refuse to decommission a node of the DC. Since CASSANDRA-12510
// a non-forced decommission fails unless the DC has more nodes than every keyspace's replication factor
// there. Starting it anyway only leaves the node in DECOMMISSION_FAILED, so the operator checks first.
// A DC that's being removed altogether is skipped: its keyspaces are handled by handleDCsDecommission.
func (r *CassandraClusterReconciler) checkScaleDownNotBlockedByReplication(cc *dbv1alpha1.CassandraCluster, sts appsv1.StatefulSet, adminRole, adminPassword string) error {
	dc := sts.Labels[dbv1alpha1.CassandraClusterDC]
	if _, dcStays := dcsMap(cc)[dc]; !dcStays {
		return nil
	}

	cqlClient, err := r.CqlClient(newCassandraConfig(cc, adminRole, adminPassword, r.Log))
	if err != nil {
		return errors.Wrap(err, "can't create cql client to check keyspace replication before scaling down")
	}
	defer cqlClient.CloseSession()

	keyspaces, err := cqlClient.GetKeyspacesInfo()
	if err != nil {
		return errors.Wrap(err, "can't get keyspace info to check replication before scaling down")
	}

	nodes := int(ptr.Deref(sts.Spec.Replicas, 1))
	blockers := keyspacesBlockingScaleDown(keyspaces, dc, nodes)
	if len(blockers) == 0 {
		return nil
	}

	msg := fmt.Sprintf("Can't scale DC %s down from %d to %d nodes: Cassandra refuses to decommission a node unless the DC has more nodes than "+
		"each keyspace's replication factor in it. Blocked by: %s. Lower their replication factor in %s to %d or less "+
		"(ALTER KEYSPACE ... WITH replication = ...), and the scale-down continues on its own",
		dc, nodes, nodes-1, strings.Join(blockers, ", "), dc, nodes-1)
	r.Events.Warning(cc, events.EventScaleDownBlocked, msg)
	r.Log.Warn(msg)

	return errScaleDownBlocked
}

// keyspacesBlockingScaleDown lists, as "name (RF n)", the keyspaces whose replication factor in dc is at
// least the node count, which is when Cassandra refuses to decommission one of the nodes.
func keyspacesBlockingScaleDown(keyspaces []cql.Keyspace, dc string, nodes int) []string {
	var blockers []string
	for _, keyspace := range keyspaces {
		if rf := replicationFactorInDC(keyspace.Replication, dc); rf > 0 && rf >= nodes {
			blockers = append(blockers, fmt.Sprintf("%s (RF %d)", keyspace.Name, rf))
		}
	}
	sort.Strings(blockers)

	return blockers
}

// replicationFactorInDC returns the number of replicas a keyspace keeps in dc, or 0 if it keeps none there
// (or isn't replicated at all, like LocalStrategy keyspaces). NetworkTopologyStrategy options look like
// "3" or, with transient replication, "3/1" where the first number counts all replicas.
func replicationFactorInDC(replication map[string]string, dc string) int {
	option := replication[dc]
	if strings.HasSuffix(replication["class"], simpleStrategyClassName) {
		option = replication[simpleStrategyRFOption]
	}

	allReplicas, _, _ := strings.Cut(option, "/")
	rf, err := strconv.Atoi(allReplicas)
	if err != nil {
		return 0
	}

	return rf
}
