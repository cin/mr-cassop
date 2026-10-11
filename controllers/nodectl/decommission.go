package nodectl

import (
	"context"
	"time"

	"github.com/cin/mr-cassop/controllers/nodectl/jolokia"
)

// decommissionTimeout is a safety net, not an expected duration: streaming a large node's data out
// can take many hours, and the call blocks until it's done.
const decommissionTimeout = 24 * time.Hour

func (n *client) Decommission(ctx context.Context, nodeIP string) error {
	req := jolokia.JMXRequest{
		Type:      jmxRequestTypeExec,
		Mbean:     mbeanCassandraDBStorageService,
		Operation: "decommission",
		// Cassandra 4.0+ added a "force" boolean parameter to StorageService.decommission();
		// false matches nodetool's default (non-forced) decommission behavior.
		Arguments: []string{"false"},
		Timeout:   decommissionTimeout,
	}

	_, err := n.jolokia.Post(ctx, req, nodeIP)
	return err
}
