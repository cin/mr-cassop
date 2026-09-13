package nodectl

import (
	"context"

	"github.com/cin/mr-cassop/controllers/nodectl/jolokia"
)

// ReloadSeeds reloads the target node's seed list from its configured seed provider,
// re-resolving any hostname-based seeds. Cassandra's internode messaging only resolves
// seed hostnames once at daemon startup, so a node whose peer's IP later changes (e.g.
// a StatefulSet pod recreated after the sole seed's IP changed) never notices on its own -
// this is the live, no-restart-required fix for that.
func (n *client) ReloadSeeds(ctx context.Context, nodeIP string) error {
	req := jolokia.JMXRequest{
		Type:      jmxRequestTypeExec,
		Mbean:     mbeanCassandraNetGossiper,
		Operation: "reloadSeeds",
	}

	_, err := n.jolokia.Post(ctx, req, nodeIP)
	return err
}
