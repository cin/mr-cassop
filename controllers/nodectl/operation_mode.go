package nodectl

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"

	"github.com/cin/mr-cassop/controllers/nodectl/jolokia"
)

type OperationMode string

const (
	NodeOperationModeStarting       OperationMode = "STARTING"
	NodeOperationModeNormal         OperationMode = "NORMAL"
	NodeOperationModeJoining        OperationMode = "JOINING"
	NodeOperationModeLeaving        OperationMode = "LEAVING"
	NodeOperationModeDecommissioned OperationMode = "DECOMMISSIONED"
	NodeOperationModeMoving         OperationMode = "MOVING"
	NodeOperationModeDraining       OperationMode = "DRAINING"
	NodeOperationModeDrained        OperationMode = "DRAINED"
	// DECOMMISSION_FAILED and JOINING_FAILED are in Cassandra 5.0's StorageService.Mode; a decommission refused by the replication factor check leaves the node in DECOMMISSION_FAILED (seen on 5.0.9)
	NodeOperationModeDecommissionFailed OperationMode = "DECOMMISSION_FAILED"
	NodeOperationModeJoiningFailed      OperationMode = "JOINING_FAILED"
)

var allowedOpModes = map[OperationMode]bool{
	NodeOperationModeStarting:           true,
	NodeOperationModeNormal:             true,
	NodeOperationModeJoining:            true,
	NodeOperationModeLeaving:            true,
	NodeOperationModeDecommissioned:     true,
	NodeOperationModeMoving:             true,
	NodeOperationModeDraining:           true,
	NodeOperationModeDrained:            true,
	NodeOperationModeDecommissionFailed: true,
	NodeOperationModeJoiningFailed:      true,
}

func (n *client) OperationMode(ctx context.Context, nodeIP string) (OperationMode, error) {
	req := jolokia.JMXRequest{
		Type:       jmxRequestTypeRead,
		Mbean:      mbeanCassandraDBStorageService,
		Attributes: []string{"OperationMode"},
	}

	resp, err := n.jolokia.Post(ctx, req, nodeIP)
	if err != nil {
		return "", err
	}

	opModeResponse := make(map[string]string)
	err = json.Unmarshal(resp.Value, &opModeResponse)
	if err != nil {
		return "", err
	}

	opModeStr, exists := opModeResponse["OperationMode"]
	if !exists {
		return "", errors.Errorf("couldn't find operation mode field, raw response: %s", string(resp.Value))
	}
	opMode := OperationMode(opModeStr)

	if !allowedOpModes[opMode] {
		return "", errors.Errorf("unknown operation mode %s", opMode)
	}

	return opMode, nil
}
