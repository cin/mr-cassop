package nodectl

import (
	"context"

	"github.com/cin/mr-cassop/controllers/nodectl/jolokia"
)

func (n *client) Decommission(ctx context.Context, nodeIP string) error {
	req := jolokia.JMXRequest{
		Type:      jmxRequestTypeExec,
		Mbean:     mbeanCassandraDBStorageService,
		Operation: "decommission",
		// Cassandra 4.0+ added a "force" boolean parameter to StorageService.decommission();
		// false matches nodetool's default (non-forced) decommission behavior.
		Arguments: []string{"false"},
	}

	_, err := n.jolokia.Post(ctx, req, nodeIP)
	return err
}
