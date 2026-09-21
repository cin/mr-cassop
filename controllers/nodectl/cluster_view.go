package nodectl

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cin/mr-cassop/controllers/nodectl/jolokia"
)

type ClusterView struct {
	LiveNodes        []string `json:"LiveNodes"`
	LeavingNodes     []string `json:"LeavingNodes"`
	JoiningNodes     []string `json:"JoiningNodes"`
	UnreachableNodes []string `json:"UnreachableNodes"`
	MovingNodes      []string `json:"MovingNodes"`
	// EndpointToHostId is the token-owning endpoints from token metadata, keyed by IP.
	EndpointToHostId map[string]string `json:"EndpointToHostId"`
}

// ErrNoTokenMetadata means a view has no token-owning endpoints at all, which never happens on a
// working node, so ring membership can't be judged from it.
var ErrNoTokenMetadata = errors.New("cluster view has no token metadata")

// OwnsTokens reports whether nodeIP still owns tokens in this view. That's true for a node that's
// up, down, joining or leaving, and false only once it has left the ring (decommissioned or
// removed). The gossip lists can't tell those apart: a node that left keeps showing up in
// UnreachableNodes, with gossip status LEFT, for up to three days.
func (v ClusterView) OwnsTokens(nodeIP string) (bool, error) {
	if len(v.EndpointToHostId) == 0 {
		return false, ErrNoTokenMetadata
	}
	_, owns := v.EndpointToHostId[nodeIP]
	return owns, nil
}

func (n *client) ClusterView(ctx context.Context, nodeIP string) (ClusterView, error) {
	req := jolokia.JMXRequest{
		Type:       jmxRequestTypeRead,
		Mbean:      mbeanCassandraDBStorageService,
		Attributes: []string{"LiveNodes", "LeavingNodes", "JoiningNodes", "UnreachableNodes", "MovingNodes", "EndpointToHostId"},
	}

	resp, err := n.jolokia.Post(ctx, req, nodeIP)
	if err != nil {
		return ClusterView{}, err
	}
	view := ClusterView{}
	err = json.Unmarshal(resp.Value, &view)
	if err != nil {
		return ClusterView{}, err
	}

	return view, nil
}
