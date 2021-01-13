package integration

import (
	"context"
	"fmt"
	"github.com/gocql/gocql"
	dbv1alpha1 "github.com/ibm/cassandra-operator/api/v1alpha1"
	"github.com/ibm/cassandra-operator/controllers/cql"
	"github.com/pkg/errors"
)

type proberMock struct {
	readyAllDCs bool
	ready       bool
	err         error
}

type cqlMock struct {
	keyspaces      []cql.Keyspace
	cassandraRoles []cql.Role
	err            error
}

type nodetoolMock struct {
	err error
}

type reaperMock struct {
	repairs       []dbv1alpha1.Repair
	isRunning     bool
	clusterExists bool
	err           error
}

func (r proberMock) Ready(ctx context.Context) (bool, error) {
	return r.ready, r.err
}

func (r proberMock) ReadyAllDCs(ctx context.Context) (bool, error) {
	return r.readyAllDCs, r.err
}

func (c *cqlMock) Query(stmt string, values ...interface{}) error {
	return c.err
}

func (c *cqlMock) GetKeyspacesInfo() ([]cql.Keyspace, error) {
	return c.keyspaces, c.err
}

func (c *cqlMock) GetRoles() ([]cql.Role, error) {
	return c.cassandraRoles, c.err
}

func (c *cqlMock) UpdateRole(role cql.Role) error {
	for i, cassandraRole := range c.cassandraRoles {
		if cassandraRole.Role == role.Role {
			c.cassandraRoles[i] = role
			return nil
		}
	}

	return gocql.ErrNotFound
}

func (c *cqlMock) CreateRole(role cql.Role) error {
	for _, cassandraRole := range c.cassandraRoles {
		if cassandraRole.Role == role.Role {
			return errors.New("role already exists")
		}
	}

	c.cassandraRoles = append(c.cassandraRoles, role)
	return c.err
}

func (c *cqlMock) UpdateRF(cc *dbv1alpha1.CassandraCluster) error {
	var systemAuthIndex *int
	for i, keyspace := range c.keyspaces {
		if keyspace.Name == "system_auth" {
			index := i
			systemAuthIndex = &index
			break
		}
	}

	var systemAuth cql.Keyspace
	rfs := map[string]string{}
	for _, dc := range cc.Spec.DCs {
		rfs[dc.Name] = fmt.Sprintf("%d", *dc.Replicas)
	}

	rfs["class"] = "org.apache.cassandra.locator.NetworkTopologyStrategy"

	systemAuth.Replication = rfs
	systemAuth.Name = "system_auth"

	if systemAuthIndex != nil {
		c.keyspaces[*systemAuthIndex] = systemAuth
	} else {
		c.keyspaces = append(c.keyspaces, systemAuth)
	}

	return c.err
}

func (n *nodetoolMock) RepairKeyspace(cc *dbv1alpha1.CassandraCluster, keyspace string) error {
	return n.err
}

func (r *reaperMock) IsRunning(ctx context.Context) (bool, error) {
	return r.isRunning, r.err
}
func (r *reaperMock) ClusterExists(ctx context.Context, name string) (bool, error) {
	return r.clusterExists, r.err
}
func (r *reaperMock) AddCluster(ctx context.Context, name, seed string) error {
	if !r.clusterExists {
		r.clusterExists = true
	}
	return r.err
}
func (r *reaperMock) ScheduleRepair(ctx context.Context, clusterName string, repair dbv1alpha1.Repair) error {
	r.repairs = append(r.repairs, repair)
	return r.err
}
