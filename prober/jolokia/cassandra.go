package jolokia

import (
	"fmt"
	"regexp"

	"sigs.k8s.io/yaml"
)

var (
	// regexIndexStatusLine drops the entire INDEX_STATUS line. Every other
	// app-state's value is a scalar followed by throwaway comma-separated
	// metadata (a version number, a token), which regexEndpointStatesRemovals
	// below cleans up. INDEX_STATUS -- new in Cassandra 5.0 -- is the one
	// exception: its value is itself a JSON object with its own embedded
	// commas and braces, e.g.
	// 	INDEX_STATUS:321:{"system":{"PaxosUncommittedIndex":3},"reaper":{"state2i":3}}
	// regexEndpointStatesRemovals' "drop everything after the first comma on
	// the line" rule truncates that into an unbalanced `{...` (verified live
	// against a 5.0.9 cluster: "yaml: line N: did not find expected ',' or
	// '}'" for every node, since AllEndpointStates -- and so this parse --
	// runs for every node polled, not just ones with an index). Nothing in
	// EndpointState models INDEX_STATUS, so the whole line is dropped
	// up front rather than taught to the other two regexes.
	regexIndexStatusLine = regexp.MustCompile(`(?m)^\s*INDEX_STATUS:\d+:.*\n?`)
	// regexEndpointStatesRemovals matches digits before a colon OR anything after a comma:
	// 	RACK:10:rack1 -> `10:`
	// 	STATUS:22:NORMAL,-2918089050085335913 -> `,-2918089050085335913`, `22:`
	regexEndpointStatesRemovals = regexp.MustCompile(`\d+:|,.+`)
	// regexEndpointStatesToYaml matches ips that start with a slash OR colons:
	// 	/10.244.0.22 -> `/10.244.0.22` // match group $1
	// 	RACK:rack1 -> `:`
	regexEndpointStatesToYamlColon = regexp.MustCompile(`(/\S+)|:`)
)

type CassandraNodeState struct {
	// SimpleStates maps node IPs to a status of either "UP" or "DOWN".
	SimpleStates map[string]string
	// AllEndpointStates maps node IPs to a struct with the extended states defined in EndpointState.
	AllEndpointStates AllEndpointStates
	// TokenToEndpointMap maps each owned token (as a signed int64 string) to the IP that owns it,
	// as reported by org.apache.cassandra.db:type=StorageService. It's cluster-wide, not per-node --
	// every node returns the same map. Best-effort: left nil if the read failed, so callers must not
	// assume it's populated.
	TokenToEndpointMap map[string]string
}

// EndpointState of useful properties of a node's state
type EndpointState struct {
	Status, DC, Rack, Internal_IP, RPC_Address string
	Load, Host_ID, Release_Version             string
	// Tokens are this node's owned tokens (signed int64 strings), derived from TokenToEndpointMap.
	// With vnodes, a node typically owns many (e.g. 16 by default) non-contiguous tokens.
	OwnedTokens []string
	// Status_With_Port is gossip's newer STATUS_WITH_PORT app-state, which
	// replaced STATUS in Cassandra 4.0+. Verified live against a 4.1
	// cluster: polling a node directly, its AllEndpointStates entry for
	// *itself* still carries the legacy STATUS (kept for backward compat),
	// but its entries for every *peer* only carry STATUS_WITH_PORT --
	// Status is empty for every non-self endpoint. Callers that need a
	// peer's status (gossipinfo) must fall back to this field; callers that
	// only ever look at a node's self-reported state (readiness checks)
	// aren't affected and don't need to change.
	Status_With_Port string
}

// AllEndpointStates implements UnmarshalText to transform the Cassandra MBean to a Go struct.
type AllEndpointStates map[string]EndpointState

// UnmarshalText interprets the MBean AllEndpointStates and unmarshalls it into a map[string]string.
// Parameter raw is a []byte that expects the following format for a map of known Endpoints:
//
//	`"\/10.244.0.5\n  generation:1615484112\n  heartbeat:147677\n  STATUS:17:NORMAL,-1068096267908218392\n`
func (e *AllEndpointStates) UnmarshalText(raw []byte) error {
	// AllEndpointStates can be converted into valid yaml with a few regex operations
	data := string(raw)
	data = regexIndexStatusLine.ReplaceAllString(data, "")
	data = regexEndpointStatesRemovals.ReplaceAllString(data, "")
	data = regexEndpointStatesToYamlColon.ReplaceAllString(data, "$1: ")

	var states map[string]EndpointState
	if err := yaml.Unmarshal([]byte(data), &states); err != nil {
		return fmt.Errorf("%w -- raw: %q", err, string(raw))
	}

	*e = states
	return nil
}
