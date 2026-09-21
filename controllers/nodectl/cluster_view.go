package nodectl

import (
	"context"
	"encoding/json"

	"github.com/cin/mr-cassop/controllers/nodectl/jolokia"
)

type ClusterView struct {
	LiveNodes        []string `json:"LiveNodes"`
	LeavingNodes     []string `json:"LeavingNodes"`
	JoiningNodes     []string `json:"JoiningNodes"`
	UnreachableNodes []string `json:"UnreachableNodes"`
	MovingNodes      []string `json:"MovingNodes"`
}

// Contains reports whether nodeIP is still a member of the ring in this view, in any state.
// A node that's down is in UnreachableNodes, not LiveNodes; only a node that has left the ring
// (decommissioned or removed) is absent from every list.
func (v ClusterView) Contains(nodeIP string) bool {
	for _, nodes := range [][]string{v.LiveNodes, v.UnreachableNodes, v.LeavingNodes, v.JoiningNodes, v.MovingNodes} {
		for _, node := range nodes {
			if node == nodeIP {
				return true
			}
		}
	}
	return false
}

func (n *client) ClusterView(ctx context.Context, nodeIP string) (ClusterView, error) {
	req := jolokia.JMXRequest{
		Type:       jmxRequestTypeRead,
		Mbean:      mbeanCassandraDBStorageService,
		Attributes: []string{"LiveNodes", "LeavingNodes", "JoiningNodes", "UnreachableNodes", "MovingNodes"},
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
