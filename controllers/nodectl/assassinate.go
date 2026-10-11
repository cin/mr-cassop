package nodectl

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/cin/mr-cassop/controllers/nodectl/jolokia"
)

// assassinateTimeout leaves room for Cassandra's gossip settle delay (about 30s by default).
const assassinateTimeout = 2 * time.Minute

func (n *client) Assassinate(ctx context.Context, execNodeIP, assassinateNodeIP string) error {
	req := jolokia.JMXRequest{
		Type:      jmxRequestTypeExec,
		Mbean:     mbeanCassandraNetGossiper,
		Operation: "assassinateEndpoint",
		Arguments: []string{assassinateNodeIP},
		Timeout:   assassinateTimeout,
	}

	resp, err := n.jolokia.Post(ctx, req, execNodeIP)
	if err != nil {
		return err
	}

	if resp.Status != 200 {
		return errors.Errorf("unexpected status code: %d. Error: %s", resp.Status, resp.Error)
	}

	return nil
}
