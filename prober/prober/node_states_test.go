package prober

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/cin/mr-cassop/prober/jolokia"

	"github.com/onsi/gomega"
	"go.uber.org/zap"
)

type jolokiaMock struct {
	nodeStates         map[string]jolokia.CassandraResponse
	stats              map[string]map[string]jolokia.StatsResult // ip -> stat name -> result
	tables             map[string][]string                       // ip -> keyspace.table list
	setSettingsErrs    map[string]map[string]string              // ip -> label -> error message
	setSettingsCalls   []setSettingsCall
	username, password string
}

type setSettingsCall struct {
	ip      string
	changes map[string]string
}

func (j *jolokiaMock) SetAuth(username, password string) {
	j.username = username
	j.password = password
}

func (j *jolokiaMock) CassandraNodeState(ip string) (jolokia.CassandraResponse, error) {
	resp, nodeFound := j.nodeStates[ip]
	if !nodeFound {
		return jolokia.CassandraResponse{}, fmt.Errorf("node %s not found", ip)
	}
	return resp, nil
}

func (j *jolokiaMock) RunStat(name, ip, table, arg string) (jolokia.StatsResult, error) {
	byName, found := j.stats[ip]
	if !found {
		return jolokia.StatsResult{}, fmt.Errorf("no mocked stats for ip %s", ip)
	}
	result, found := byName[name]
	if !found {
		return jolokia.StatsResult{}, fmt.Errorf("no mocked stat %q for ip %s", name, ip)
	}
	return result, nil
}

func (j *jolokiaMock) ListTables(ip string) ([]string, error) {
	return j.tables[ip], nil
}

func (j *jolokiaMock) SetSettings(ip string, changes map[string]string) (map[string]string, error) {
	j.setSettingsCalls = append(j.setSettingsCalls, setSettingsCall{ip: ip, changes: changes})
	return j.setSettingsErrs[ip], nil
}

func endpointState(ip, state string) jolokia.EndpointState {
	return jolokia.EndpointState{
		Status:      state,
		DC:          "dc1",
		Rack:        "rack1",
		Internal_IP: ip,
		RPC_Address: ip,
	}
}

func cassandraResponse(endpoints map[string]string) jolokia.CassandraNodeState {
	cassResp := jolokia.CassandraNodeState{
		SimpleStates:      map[string]string{},
		AllEndpointStates: map[string]jolokia.EndpointState{},
	}

	for ip, cassandraNodeState := range endpoints {
		cassResp.SimpleStates["/"+ip] = cassandraNodeState
		cassResp.AllEndpointStates["/"+ip] = endpointState(ip, cassandraNodeState)
	}
	return cassResp
}

// leftNodesResponse is one live node plus two peers gossip reports as LEFT: one the way a node
// reports a peer (STATUS_WITH_PORT only) and one with the legacy STATUS.
func leftNodesResponse() jolokia.CassandraNodeState {
	resp := cassandraResponse(map[string]string{"10.12.13.43": "UP"})
	resp.SimpleStates["/10.12.13.46"] = "DOWN"
	resp.SimpleStates["/10.12.13.47"] = "DOWN"
	resp.AllEndpointStates["/10.12.13.46"] = jolokia.EndpointState{DC: "dc1", Status_With_Port: "LEFT,-1041975228702506363,1791764308170"}
	resp.AllEndpointStates["/10.12.13.47"] = jolokia.EndpointState{DC: "dc1", Status: "removed,6731e175,1791763657601"}
	return resp
}

func TestEndpointLeft(t *testing.T) {
	asserts := gomega.NewWithT(t)
	asserts.Expect(endpointLeft(jolokia.EndpointState{Status_With_Port: "LEFT,-1041975228702506363,1791764308170"})).To(gomega.BeTrue())
	asserts.Expect(endpointLeft(jolokia.EndpointState{Status: "left,-1,2"})).To(gomega.BeTrue())
	asserts.Expect(endpointLeft(jolokia.EndpointState{Status: "removed,abc,2"})).To(gomega.BeTrue())
	for _, live := range []string{"", "NORMAL,-1591302511779084373", "LEAVING,-1", "BOOT,-1", "shutdown,true"} {
		asserts.Expect(endpointLeft(jolokia.EndpointState{Status: live, Status_With_Port: live})).To(gomega.BeFalse(), live)
	}
}

func TestUpdateNodeStates(t *testing.T) {
	asserts := gomega.NewWithT(t)
	successJMXResponse := jolokia.Response{
		Status: http.StatusOK,
	}
	testCases := []struct {
		name          string
		initialState  state
		expectedState state
		nodeStates    map[string]jolokia.CassandraResponse
	}{
		{
			name: "new ready nodes were registered",
			initialState: state{
				nodes: map[string]nodeState{
					"/10.12.13.43": {},
					"/10.12.13.44": {},
					"/10.12.13.45": {},
				},
				podIPs: map[string]string{
					"/10.12.13.43": "172.143.32.1",
					"/10.12.13.44": "172.143.32.2",
					"/10.12.13.45": "172.143.32.3",
				},
				dcs: []dc{
					{
						Name:     "dc1",
						Replicas: 3,
					},
				},
			},
			expectedState: state{
				podIPs: map[string]string{
					"/10.12.13.43": "172.143.32.1",
					"/10.12.13.44": "172.143.32.2",
					"/10.12.13.45": "172.143.32.3",
				},
				nodes: map[string]nodeState{
					"/10.12.13.43": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP", "/10.12.13.46": "UP"},
						EndpointState: endpointState("10.12.13.43", "UP"),
					},
					"/10.12.13.44": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP", "/10.12.13.46": "UP"},
						EndpointState: endpointState("10.12.13.44", "UP"),
					},
					"/10.12.13.45": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP", "/10.12.13.46": "UP"},
						EndpointState: endpointState("10.12.13.45", "UP"),
					},
					"/10.12.13.46": {
						EndpointState: endpointState("10.12.13.46", "UP"),
					},
				},
				dcs: []dc{
					{
						Name:     "dc1",
						Replicas: 3,
					},
				},
			},
			nodeStates: map[string]jolokia.CassandraResponse{
				"172.143.32.1": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "UP",
						"10.12.13.45": "UP",
						"10.12.13.46": "UP",
					}),
				},
				"172.143.32.2": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "UP",
						"10.12.13.45": "UP",
						"10.12.13.46": "UP",
					}),
				},
				"172.143.32.3": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "UP",
						"10.12.13.45": "UP",
						"10.12.13.46": "UP",
					}),
				},
			},
		},
		{
			name: "nodes that left the ring are not discovered",
			initialState: state{
				nodes:  map[string]nodeState{"/10.12.13.43": {}},
				podIPs: map[string]string{"/10.12.13.43": "172.143.32.1"},
				dcs:    []dc{{Name: "dc1", Replicas: 1}},
			},
			expectedState: state{
				podIPs: map[string]string{"/10.12.13.43": "172.143.32.1"},
				nodes: map[string]nodeState{
					"/10.12.13.43": {
						SimpleStates:  map[string]string{"/10.12.13.43": "UP", "/10.12.13.46": "DOWN", "/10.12.13.47": "DOWN"},
						EndpointState: endpointState("10.12.13.43", "UP"),
					},
				},
				dcs: []dc{{Name: "dc1", Replicas: 1}},
			},
			nodeStates: map[string]jolokia.CassandraResponse{
				"172.143.32.1": {
					Response: successJMXResponse,
					Value:    leftNodesResponse(),
				},
			},
		},
		{
			name: "0 discovered nodes",
			initialState: state{
				nodes:  map[string]nodeState{},
				dcs:    []dc{},
				podIPs: map[string]string{},
			},
			expectedState: state{
				nodes:  map[string]nodeState{},
				dcs:    []dc{},
				podIPs: map[string]string{},
			},
			nodeStates: map[string]jolokia.CassandraResponse{},
		},
		{
			name: "node removed",
			initialState: state{
				nodes: map[string]nodeState{
					"/10.12.13.43": {},
					"/10.12.13.44": {},
					"/10.12.13.45": {},
					"/10.12.13.46": {}, // doesn't exist anymore
				},
				dcs: []dc{
					{
						Name:     "dc1",
						Replicas: 3,
					},
				},
				podIPs: map[string]string{
					"/10.12.13.43": "172.16.16.43",
					"/10.12.13.44": "172.16.16.44",
					"/10.12.13.45": "172.16.16.45",
					"/10.12.13.46": "172.16.16.46",
				},
			},
			expectedState: state{
				nodes: map[string]nodeState{
					"/10.12.13.43": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP"},
						EndpointState: endpointState("10.12.13.43", "UP"),
					},
					"/10.12.13.44": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP"},
						EndpointState: endpointState("10.12.13.44", "UP"),
					},
					"/10.12.13.45": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP"},
						EndpointState: endpointState("10.12.13.45", "UP"),
					},
				},
				dcs: []dc{
					{
						Name:     "dc1",
						Replicas: 3,
					},
				},
				podIPs: map[string]string{
					"/10.12.13.43": "172.16.16.43",
					"/10.12.13.44": "172.16.16.44",
					"/10.12.13.45": "172.16.16.45",
					"/10.12.13.46": "172.16.16.46",
				},
			},
			nodeStates: map[string]jolokia.CassandraResponse{
				"172.16.16.43": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "UP",
						"10.12.13.45": "UP",
					}),
				},
				"172.16.16.44": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "UP",
						"10.12.13.45": "UP",
					}),
				},
				"172.16.16.45": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "UP",
						"10.12.13.45": "UP",
					}),
				},
			},
		},
		{
			name: "node becomes unready",
			initialState: state{
				nodes: map[string]nodeState{
					"/10.12.13.43": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP"},
						EndpointState: endpointState("10.12.13.43", "UP"),
					},
					"/10.12.13.44": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP"},
						EndpointState: endpointState("10.12.13.44", "UP"),
					},
					"/10.12.13.45": {
						SimpleStates:  map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP"},
						EndpointState: endpointState("10.12.13.45", "UP"),
					},
				},
				dcs: []dc{
					{
						Name:     "dc1",
						Replicas: 3,
					},
				},
				podIPs: map[string]string{
					"/10.12.13.43": "172.16.16.43",
					"/10.12.13.44": "172.16.16.44",
					"/10.12.13.45": "172.16.16.45",
				},
			},
			expectedState: state{
				nodes: map[string]nodeState{
					"/10.12.13.43": {
						SimpleStates: map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "DOWN"},
						EndpointState: jolokia.EndpointState{
							Status:      "UP",
							DC:          "dc1",
							Rack:        "rack1",
							Internal_IP: "10.12.13.43",
							RPC_Address: "10.12.13.43",
						},
					},
					"/10.12.13.44": {
						SimpleStates: map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "UP"},
						EndpointState: jolokia.EndpointState{
							Status:      "UP",
							DC:          "dc1",
							Rack:        "rack1",
							Internal_IP: "10.12.13.44",
							RPC_Address: "10.12.13.44",
						},
					},
					"/10.12.13.45": {
						SimpleStates: map[string]string{"/10.12.13.45": "UP", "/10.12.13.43": "UP", "/10.12.13.44": "DOWN"},
						EndpointState: jolokia.EndpointState{
							Status:      "UP",
							DC:          "dc1",
							Rack:        "rack1",
							Internal_IP: "10.12.13.45",
							RPC_Address: "10.12.13.45",
						},
					},
				},
				dcs: []dc{
					{
						Name:     "dc1",
						Replicas: 3,
					},
				},
				podIPs: map[string]string{
					"/10.12.13.43": "172.16.16.43",
					"/10.12.13.44": "172.16.16.44",
					"/10.12.13.45": "172.16.16.45",
				},
			},
			nodeStates: map[string]jolokia.CassandraResponse{
				"172.16.16.43": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "DOWN",
						"10.12.13.45": "UP",
					}),
				},
				"172.16.16.44": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "UP",
						"10.12.13.45": "UP",
					}),
				},
				"172.16.16.45": {
					Response: successJMXResponse,
					Value: cassandraResponse(map[string]string{
						"10.12.13.43": "UP",
						"10.12.13.44": "DOWN",
						"10.12.13.45": "UP",
					}),
				},
			},
		},
	}

	for _, testCase := range testCases {
		testProber := &Prober{
			auth:  UserAuth{},
			log:   zap.NewNop().Sugar(),
			state: testCase.initialState,
			jolokia: &jolokiaMock{
				nodeStates: testCase.nodeStates,
			},
		}

		testProber.updateNodeStates()
		asserts.Expect(testProber.state).To(gomega.Equal(testCase.expectedState), cmp.Diff(testCase.expectedState, testProber.state, cmp.Options{cmp.AllowUnexported(state{})}))
	}
}

func TestIsNodeReady(t *testing.T) {
	asserts := gomega.NewWithT(t)
	const joining, peer = "/10.12.13.44", "/10.12.13.43"

	tests := []struct {
		name        string
		joiningNode nodeState
		ready       bool
	}{
		{
			name: "NORMAL node seen as up by peers",
			joiningNode: nodeState{
				SimpleStates:  map[string]string{joining: "UP", peer: "UP"},
				EndpointState: jolokia.EndpointState{Status: "NORMAL"},
			},
			ready: true,
		},
		{
			name: "joining node is gossip-UP but not NORMAL",
			joiningNode: nodeState{
				SimpleStates:  map[string]string{joining: "UP", peer: "UP"},
				EndpointState: jolokia.EndpointState{Status: "BOOT"},
			},
			ready: false,
		},
		{
			// what a joining node actually reports on Cassandra 5.0: peers see it UP but its own STATUS is empty
			name: "joining node with empty own status",
			joiningNode: nodeState{
				SimpleStates: map[string]string{joining: "UP", peer: "UP"},
			},
			ready: false,
		},
		{
			name:        "node not polled yet has no status",
			joiningNode: nodeState{},
			ready:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &Prober{log: zap.NewNop().Sugar(), state: state{nodes: map[string]nodeState{
				peer: {
					SimpleStates:  map[string]string{peer: "UP", joining: "UP"},
					EndpointState: jolokia.EndpointState{Status: "NORMAL"},
				},
				joining: test.joiningNode,
			}}}
			ready, _ := p.isNodeReady(joining)
			asserts.Expect(ready).To(gomega.Equal(test.ready))
		})
	}
}
