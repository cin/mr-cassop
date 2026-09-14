package jolokia

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// StatsResult is the one generic wire shape every "get" tool returns: a set
// of labeled groups, each a list of label/value rows. One shape for every
// stat command means adding a new one is "write a fetch function and a
// catalog entry," not "invent a new Go type, a new route, and a new frontend
// formatter every time" -- which is how NodeInfo/DescribeCluster/
// CompactionStats were each built before this existed.
type StatsResult struct {
	Groups []StatGroup `json:"groups"`
}

type StatGroup struct {
	Name string    `json:"name"`
	Rows []StatRow `json:"rows"`
}

type StatRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func row(label string, value any) StatRow {
	return StatRow{Label: label, Value: fmt.Sprint(value)}
}

func listValue(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// StatDef is one catalog entry: a name the frontend/API refers to it by, a
// display label for its button, and the fetch logic itself. Fetch always
// makes a fresh, on-demand JMX call -- these back user-triggered actions
// where a real round trip is the expected behavior, never the poll cache.
//
// Deliberately read-only for now: every Fetch here is a JMX *read*. The
// operator's nodectl package (root module) already has real write/exec
// operations (Decommission, Assassinate); wiring "set" params into this UI
// needs its own confirmation/safety design before it belongs here. When that
// lands, the natural extension is a parallel Writable bool + Set func on
// StatDef, gated behind that design -- not bolted on ad hoc.
type StatDef struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Diffable marks a stat whose rows are meaningful to compare node-to-node
	// (config/setting reads, where every node "should" agree) rather than
	// runtime counters that are expected to vary (load, GC counts, timings).
	// The frontend uses this to decide whether to highlight rows that differ
	// across a multi-node run -- see fetchSettings, the first and so far only
	// entry that sets it.
	Diffable bool `json:"diffable"`
	// Category groups related entries for the frontend's category selector
	// (Status/Compaction/Performance/Tables/Settings) so the tool list/wheel
	// stay navigable as the catalog grows, rather than one long flat list.
	// Purely a presentation grouping -- RunStat dispatches by Name alone.
	Category string `json:"category"`
	// RequiresTable marks a stat whose Fetch needs a "keyspace.table" target
	// (cfstats/cfhistograms) rather than being runnable against a node on its
	// own -- exposed via /tools so the frontend knows to prompt for one
	// before running it. This is *why* cfstats/tablehistograms couldn't
	// previously be "just add a Catalog entry" like everything else: unlike
	// every other command here, a per-table metric set isn't bounded by
	// node/pool/request-type cardinality, it's bounded by how many tables
	// exist -- fine once scoped to one target table, not fine fetched for
	// all of them on every click (see the old "deliberately not implemented"
	// note this StatDef shape replaces).
	RequiresTable bool                                            `json:"requiresTable"`
	Fetch         func(j *Client, ip string) (StatsResult, error) `json:"-"`
	// FetchTable is Fetch's table-scoped counterpart, used instead of Fetch
	// when RequiresTable is true. The two are kept as separate fields rather
	// than adding a table parameter to Fetch itself so every other, simpler
	// entry doesn't have to carry an always-empty parameter.
	FetchTable func(j *Client, ip, table string) (StatsResult, error) `json:"-"`
	// Wheel marks a curated "most used" subset for the frontend's middle-click
	// radial wheel, independent of Category (which still drives the full
	// category-filtered button list). The wheel's own radius grows with its
	// item count (see ui/static/index.html's wheelRadius), so as the catalog
	// grew past ~20 entries it stopped being usable for a quick gesture --
	// deliberately opt-in per entry rather than "every catalog entry is on
	// the wheel" going forward. Only a handful of frequent, fast, node-scoped
	// reads should set this; leave new entries (especially RequiresTable
	// ones, which need a table prompt anyway) off the wheel by default.
	Wheel bool `json:"wheel"`
	// Confirm marks a stat the frontend must confirm() before running --
	// for anything that isn't a pure read (see fetchReloadSeeds, the first
	// entry to set this). Deliberately separate from Wheel: a Confirm entry
	// should generally also leave Wheel unset, since a drag-and-release
	// gesture is the wrong interaction for something that needs a conscious
	// confirmation step.
	Confirm bool `json:"confirm"`
	// SingleTarget marks a stat that only makes sense (or is only safe)
	// against exactly one node at a time -- the frontend refuses to run it
	// against a multi-selection. reloadSeeds is node-local gossip state, not
	// a cluster-wide operation, so "run against every selected node" reads
	// as one intentional action per node, not one bulk action -- forcing the
	// operator to target nodes one at a time here is deliberate friction.
	SingleTarget bool `json:"singleTarget"`
	// Danger marks a stat as belonging in the frontend's red "Danger Zone"
	// section -- cluster-membership/lifecycle operations (decommission,
	// assassinate, drain, ...) that are hard or impossible to reverse if run
	// by mistake, unlike everything else in this catalog (a bounded config
	// write, a cache clear, a gossip reload). The frontend gates a Danger
	// entry behind typing its own label back, not just confirm() -- the same
	// bar a irreversible delete gets elsewhere. Every Danger entry should
	// also set Confirm and, in effectively every case so far, SingleTarget:
	// these are one-node-at-a-time operations, not bulk actions.
	Danger bool `json:"danger"`
	// RequiresArg marks a stat that needs one free-text argument neither a
	// node nor a keyspace.table target covers -- an endpoint address to
	// assassinate, a host ID to remove, a token to move to. Deliberately
	// reuses RequiresTable's shape (one extra string, threaded through the
	// same RunStat/route plumbing as a distinct arg param) rather than
	// inventing a bespoke mechanism per command; the frontend collects it
	// with a plain prompt() instead of the Tables-panel picker RequiresTable
	// gets, since there's no finite list to choose from.
	RequiresArg bool `json:"requiresArg"`
	// ArgLabel is the prompt() text shown when collecting RequiresArg's
	// value -- specific enough that the operator knows what to type without
	// leaving this page to check nodetool's own help text.
	ArgLabel string `json:"argLabel"`
	// FetchArg is Fetch's RequiresArg-scoped counterpart, parallel to
	// FetchTable.
	FetchArg func(j *Client, ip, arg string) (StatsResult, error) `json:"-"`
}

// wheelTools is the curated "most used" subset of the catalog below that
// also sets Wheel: true -- roughly the original, pre-growth catalog (see
// wheelRadius' own comment on why a fixed 90px radius stopped being enough
// once the read-only catalog passed a dozen entries, and Wheel's own comment
// on why growth from here on should default to the category list, not this).
var Catalog = []StatDef{
	{Name: "info", Label: "Info", Category: "Status", Fetch: fetchInfo, Wheel: true},
	// Diffable: a node's SchemaVersion disagreeing with the rest of the
	// cluster is a real, actionable problem (a pending/failed schema push),
	// not expected variance -- worth the same red-highlight treatment as
	// fetchSettings gets, and free to add since it's just this one flag.
	{Name: "describecluster", Label: "Describe", Category: "Status", Fetch: fetchDescribeCluster, Diffable: true, Wheel: true},
	{Name: "version", Label: "Version", Category: "Status", Fetch: fetchVersion, Diffable: true},
	{Name: "gossipinfo", Label: "Gossip Info", Category: "Status", Fetch: fetchGossipInfo},
	{Name: "clientstats", Label: "Client Stats", Category: "Performance", Fetch: fetchClientStats},
	{Name: "compactionstats", Label: "Compactions", Category: "Compaction", Fetch: fetchCompactionStats, Wheel: true},
	{Name: "tpstats", Label: "TP Stats", Category: "Performance", Fetch: fetchTPStats, Wheel: true},
	{Name: "netstats", Label: "Net Stats", Category: "Performance", Fetch: fetchNetStats, Wheel: true},
	{Name: "gcstats", Label: "GC Stats", Category: "Performance", Fetch: fetchGCStats},
	{Name: "proxyhistograms", Label: "Proxy Histograms", Category: "Performance", Fetch: fetchProxyHistograms},
	{Name: "cachestats", Label: "Cache Stats", Category: "Performance", Fetch: fetchCacheStats, Wheel: true},
	{Name: "settings", Label: "Settings (get*)", Category: "Settings", Fetch: fetchSettings, Diffable: true, Wheel: true},
	// Diffable for the same reason as describecluster above: gossip/native
	// transport/incremental backups should normally be uniformly enabled (or
	// uniformly disabled during planned maintenance) across a healthy
	// cluster -- one node quietly disagreeing is exactly the kind of thing
	// this pane exists to surface.
	{Name: "statusflags", Label: "Status Flags", Category: "Status", Fetch: fetchStatusFlags, Diffable: true},
	{Name: "compactionhistory", Label: "Compaction History", Category: "Compaction", Fetch: fetchCompactionHistory},
	{Name: "getlogginglevels", Label: "Logging Levels", Category: "Status", Fetch: fetchLoggingLevels},
	// The scoping decision the old comment here asked for: a required
	// keyspace.table target (RequiresTable), not pagination or a top-N-by-
	// size default -- see StatDef.RequiresTable's doc comment for why.
	{Name: "cfstats", Label: "Table Stats", Category: "Tables", RequiresTable: true, FetchTable: fetchCfStats},
	{Name: "cfhistograms", Label: "Table Histograms", Category: "Tables", RequiresTable: true, FetchTable: fetchTableHistograms},
	{Name: "toppartitions", Label: "Top Partitions", Category: "Tables", RequiresTable: true, FetchTable: fetchTopPartitions},
	// describering/effectiveownership are keyspace-scoped, not table-scoped
	// like everything else RequiresTable -- see fetchDescribeRing's doc
	// comment for why they reuse RequiresTable/FetchTable anyway rather than
	// adding a parallel RequiresKeyspace scoping.
	{Name: "describering", Label: "Describe Ring", Category: "Tables", RequiresTable: true, FetchTable: fetchDescribeRing},
	{Name: "effectiveownership", Label: "Effective Ownership", Category: "Tables", RequiresTable: true, FetchTable: fetchEffectiveOwnership},
	// reloadseeds is the catalog's first non-read-only entry -- see
	// StatDef.Confirm/SingleTarget's doc comments for why it needs both.
	// "Actions" is a new category rather than folding it into "Status" so
	// the side panel visibly separates it from every read-only button.
	{Name: "reloadseeds", Label: "Reload Seeds", Category: "Actions", Fetch: fetchReloadSeeds, Confirm: true, SingleTarget: true},

	// getseeds/failuredetector/listpendinghints: read-only, node-scoped,
	// no new plumbing beyond an attribute read -- the "cheap wins" flagged
	// in the nodetool coverage matrix.
	{Name: "getseeds", Label: "Seeds", Category: "Status", Fetch: fetchSeeds},
	{Name: "failuredetector", Label: "Failure Detector", Category: "Status", Fetch: fetchFailureDetector},
	{Name: "listpendinghints", Label: "Pending Hints", Category: "Status", Fetch: fetchPendingHints},

	// invalidate*cache: mutating like reloadseeds (Confirm), but -- unlike
	// reloadseeds' gossip-state change -- invalidating a cache has no
	// cross-node coordination concern, so running it against several
	// selected nodes at once (e.g. "clear the row cache everywhere after a
	// schema change") is a normal, intentional use -- no SingleTarget.
	{Name: "invalidatekeycache", Label: "Invalidate Key Cache", Category: "Actions", Fetch: fetchInvalidateKeyCache, Confirm: true},
	{Name: "invalidaterowcache", Label: "Invalidate Row Cache", Category: "Actions", Fetch: fetchInvalidateRowCache, Confirm: true},
	{Name: "invalidatecountercache", Label: "Invalidate Counter Cache", Category: "Actions", Fetch: fetchInvalidateCounterCache, Confirm: true},
	{Name: "invalidatecredentialscache", Label: "Invalidate Credentials Cache", Category: "Actions", Fetch: fetchInvalidateCredentialsCache, Confirm: true},
	{Name: "invalidatepermissionscache", Label: "Invalidate Permissions Cache", Category: "Actions", Fetch: fetchInvalidatePermissionsCache, Confirm: true},
	{Name: "invalidaterolescache", Label: "Invalidate Roles Cache", Category: "Actions", Fetch: fetchInvalidateRolesCache, Confirm: true},
	{Name: "invalidatejmxpermissionscache", Label: "Invalidate JMX Permissions Cache", Category: "Actions", Fetch: fetchInvalidateJmxPermissionsCache, Confirm: true},
	{Name: "invalidatenetworkpermissionscache", Label: "Invalidate Network Permissions Cache", Category: "Actions", Fetch: fetchInvalidateNetworkPermissionsCache, Confirm: true},

	// getremovalstatus is the one read-only entry in this group -- the
	// current state of any in-progress node removal. Everything else below
	// is Danger: true, gated behind the frontend's typed-confirmation Danger
	// Zone (see StatDef.Danger's doc comment), not just a plain confirm().
	{Name: "getremovalstatus", Label: "Removal Status", Category: "Status", Fetch: fetchRemovalStatus},

	// decommission/drain/stopdaemon/forceremovecompletion operate on the
	// node the JMX call itself targets -- no extra argument needed, same
	// shape as every plain Fetch entry, just gated by Danger/Confirm/
	// SingleTarget instead of left open.
	{Name: "decommission", Label: "Decommission", Category: "Danger", Fetch: fetchDecommission, Danger: true, Confirm: true, SingleTarget: true},
	{Name: "drain", Label: "Drain", Category: "Danger", Fetch: fetchDrain, Danger: true, Confirm: true, SingleTarget: true},
	{Name: "stopdaemon", Label: "Stop Daemon", Category: "Danger", Fetch: fetchStopDaemon, Danger: true, Confirm: true, SingleTarget: true},
	{Name: "forceremovecompletion", Label: "Force Remove Completion", Category: "Danger", Fetch: fetchForceRemoveCompletion, Danger: true, Confirm: true, SingleTarget: true},

	// assassinate/removenode/move each need one free-text argument identifying
	// a *different* node (or, for move, a token) than the one the JMX call
	// itself runs against -- RequiresArg, collected via prompt() rather than
	// the Tables-panel picker RequiresTable gets, since there's no finite
	// list to choose from here.
	{Name: "assassinate", Label: "Assassinate Endpoint", Category: "Danger", RequiresArg: true,
		ArgLabel: "Endpoint address to assassinate (e.g. 10.0.0.5) -- this permanently removes it from gossip with no re-replication",
		FetchArg: fetchAssassinate, Danger: true, Confirm: true, SingleTarget: true},
	{Name: "removenode", Label: "Remove Node", Category: "Danger", RequiresArg: true,
		ArgLabel: "Host ID of the down node to remove from the ring (see Info's Host ID row on that node, if still reachable)",
		FetchArg: fetchRemoveNode, Danger: true, Confirm: true, SingleTarget: true},
	{Name: "move", Label: "Move", Category: "Danger", RequiresArg: true,
		ArgLabel: "New token to move this node to",
		FetchArg: fetchMove, Danger: true, Confirm: true, SingleTarget: true},
}

// ListStats returns every catalog entry's name/label, so the frontend can
// build its tool buttons from the server's own catalog instead of a
// hand-maintained JS list that can drift out of sync with what's actually
// implemented.
func ListStats() []StatDef {
	return Catalog
}

// RunStat looks up a catalog entry by name and runs it, dispatching to
// FetchTable instead of Fetch when the entry is RequiresTable (table is
// ignored otherwise). Returns an error if no such stat is registered, or if
// a RequiresTable stat is run without one.
func RunStat(j *Client, name, ip, table, arg string) (StatsResult, error) {
	for _, def := range Catalog {
		if def.Name != name {
			continue
		}
		if def.RequiresTable {
			if table == "" {
				return StatsResult{}, fmt.Errorf("stat %q requires a table parameter (?table=keyspace.table)", name)
			}
			return def.FetchTable(j, ip, table)
		}
		if def.RequiresArg {
			if arg == "" {
				return StatsResult{}, fmt.Errorf("stat %q requires an arg parameter (?arg=...)", name)
			}
			return def.FetchArg(j, ip, arg)
		}
		return def.Fetch(j, ip)
	}
	return StatsResult{}, fmt.Errorf("unknown stat %q", name)
}

// fetchInfo matches `nodetool status`'s per-node operational view: current
// mode plus live/leaving/joining/unreachable/moving node lists.
func fetchInfo(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.db:type=StorageService",
		"OperationMode,LiveNodes,LeavingNodes,JoiningNodes,UnreachableNodes,MovingNodes")
	if err != nil {
		return StatsResult{}, err
	}
	var info struct {
		OperationMode    string
		LiveNodes        []string
		LeavingNodes     []string
		JoiningNodes     []string
		UnreachableNodes []string
		MovingNodes      []string
	}
	if err := json.Unmarshal(value, &info); err != nil {
		return StatsResult{}, err
	}

	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{
		row("Operation mode", info.OperationMode),
		row("Live nodes", listValue(info.LiveNodes)),
		row("Unreachable", listValue(info.UnreachableNodes)),
		row("Joining", listValue(info.JoiningNodes)),
		row("Leaving", listValue(info.LeavingNodes)),
		row("Moving", listValue(info.MovingNodes)),
	}}}}, nil
}

// fetchDescribeCluster matches `nodetool describecluster`/`nodetool version`.
func fetchDescribeCluster(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.db:type=StorageService",
		"ClusterName,PartitionerName,SchemaVersion,ReleaseVersion")
	if err != nil {
		return StatsResult{}, err
	}
	var desc struct {
		ClusterName     string
		PartitionerName string
		SchemaVersion   string
		ReleaseVersion  string
	}
	if err := json.Unmarshal(value, &desc); err != nil {
		return StatsResult{}, err
	}

	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{
		row("Cluster name", desc.ClusterName),
		row("Partitioner", desc.PartitionerName),
		row("Schema version", desc.SchemaVersion),
		row("Release version", desc.ReleaseVersion),
	}}}}, nil
}

// fetchVersion matches `nodetool version`: just the running Cassandra
// release, for the common one-line "what version is this node on" check.
// describecluster above already includes this same attribute plus
// cluster-wide context (name/partitioner/schema version) -- this doesn't add
// new data, just exposes it under nodetool's own command name too.
func fetchVersion(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.db:type=StorageService", "ReleaseVersion")
	if err != nil {
		return StatsResult{}, err
	}
	var releaseVersion string
	if err := json.Unmarshal(value, &releaseVersion); err != nil {
		return StatsResult{}, err
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{
		row("Release version", releaseVersion),
	}}}}, nil
}

// fetchGossipInfo matches `nodetool gossipinfo`: every endpoint's current
// gossip-reported state, one group per endpoint IP so a disagreement (e.g.
// one node still reporting a peer DOWN after the rest have converged) is
// visible at a glance rather than needing a per-node diff. Reuses
// CassandraNodeState/AllEndpointStates -- the same gossip parsing Prober's
// own readiness checks already depend on (see prober/node_states.go) --
// rather than re-deriving gossip parsing here.
func fetchGossipInfo(j *Client, ip string) (StatsResult, error) {
	state, err := j.CassandraNodeState(ip)
	if err != nil {
		return StatsResult{}, err
	}
	if state.Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("gossip read failed: %s", state.Error)
	}

	peerIPs := make([]string, 0, len(state.Value.AllEndpointStates))
	for peerIP := range state.Value.AllEndpointStates {
		peerIPs = append(peerIPs, peerIP)
	}
	sort.Strings(peerIPs)

	groups := make([]StatGroup, 0, len(peerIPs))
	for _, peerIP := range peerIPs {
		es := state.Value.AllEndpointStates[peerIP]
		status := es.Status
		if status == "" {
			// Every non-self endpoint on Cassandra 4.0+ only carries the
			// newer STATUS_WITH_PORT app-state -- see EndpointState's doc
			// comment. Internal_IP/RPC_Address have the same gap (their
			// _AND_PORT replacements exist in raw gossip too) but aren't
			// worth adding here: the group name below is already this
			// endpoint's IP, so a redundant address row isn't worth the
			// extra parsing risk (those fields' values contain a literal
			// ":<port>", which the shared regex-based gossip parser isn't
			// verified to round-trip safely).
			status = es.Status_With_Port
		}
		groups = append(groups, StatGroup{
			Name: strings.TrimPrefix(peerIP, "/"),
			Rows: []StatRow{
				row("Status", status),
				row("DC", es.DC),
				row("Rack", es.Rack),
				row("Load", es.Load),
				row("Host ID", es.Host_ID),
				row("Release version", es.Release_Version),
			},
		})
	}
	return StatsResult{Groups: groups}, nil
}

// fetchClientStats matches `nodetool clientstats`'s summary line: the count
// of currently connected native-protocol clients. Deliberately not the
// per-connection --all listing -- that needs a JMX *operation* invocation
// (a call shape nothing in this client makes yet; every other fetch here is
// a plain attribute read) and isn't worth adding without live-verifying its
// exact operation name/return shape first, the same bar every other fetcher
// in this file was held to.
func fetchClientStats(j *Client, ip string) (StatsResult, error) {
	const mbean = "org.apache.cassandra.metrics:type=Client,name=connectedNativeClients"
	gauges, err := j.bulkReadGauges(ip, []string{mbean})
	if err != nil {
		return StatsResult{}, err
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{
		row("Connected native clients", gauges[mbean]),
	}}}}, nil
}

// fetchCompactionStats matches `nodetool compactionstats`. Two separate
// MBeans, like nodetool itself: in-progress compactions come from
// CompactionManager, but the pending-task count is a metrics Gauge
// (org.apache.cassandra.metrics), not a CompactionManager attribute --
// verified against a live 4.1 cluster after an initial guess ("PendingTasks"
// directly on CompactionManager) came back "No such attribute".
func fetchCompactionStats(j *Client, ip string) (StatsResult, error) {
	summaryValue, err := j.readMBeanAttributes(ip, "org.apache.cassandra.db:type=CompactionManager", "CompactionSummary")
	if err != nil {
		return StatsResult{}, err
	}
	var summary []string
	if err := json.Unmarshal(summaryValue, &summary); err != nil {
		return StatsResult{}, err
	}

	pendingValue, err := j.readMBeanAttributes(ip, "org.apache.cassandra.metrics:type=Compaction,name=PendingTasks", "Value")
	if err != nil {
		return StatsResult{}, err
	}
	var pending int
	if err := json.Unmarshal(pendingValue, &pending); err != nil {
		return StatsResult{}, err
	}

	rows := []StatRow{row("Pending tasks", pending)}
	if len(summary) == 0 {
		rows = append(rows, row("In progress", "none"))
	} else {
		for i, line := range summary {
			rows = append(rows, row(fmt.Sprintf("Compaction %d", i+1), line))
		}
	}
	return StatsResult{Groups: []StatGroup{{Rows: rows}}}, nil
}

// tpStatsMetrics are the per-pool Codahale Gauge MBeans this reads, keyed by
// their ObjectName "name=" value. Cassandra 4.x exposes one MBean per
// (path, scope, metric name) triple under org.apache.cassandra.metrics,
// type=ThreadPools -- NOT one MBean per pool with multiple attributes, which
// was the wrong shape an earlier version of this assumed (verified by
// searching a live 4.1 cluster: "org.apache.cassandra.concurrent:type=*"
// matched nothing at all).
var tpStatsMetrics = []string{"ActiveTasks", "PendingTasks", "CompletedTasks"}

// objectNameAttributes parses the "key=value,key=value" portion of a
// canonical JMX ObjectName (after the "domain:") into a map. Jolokia's
// search results for a target-proxied query are also prefixed "proxy@",
// which this strips first.
func objectNameAttributes(name string) map[string]string {
	name = strings.TrimPrefix(name, "proxy@")
	_, kvPart, found := strings.Cut(name, ":")
	if !found {
		return nil
	}
	attrs := make(map[string]string)
	for _, pair := range strings.Split(kvPart, ",") {
		k, v, found := strings.Cut(pair, "=")
		if found {
			attrs[k] = v
		}
	}
	return attrs
}

// fetchTPStats matches `nodetool tpstats`: per-thread-pool active/pending/
// completed task counts. Unlike the other stats here, the MBean set isn't
// fixed -- thread pool names vary -- so this searches for every thread-pool
// metric MBean first, groups the ones we care about by (path, scope), then
// bulk-reads all of them in one follow-up request.
func fetchTPStats(j *Client, ip string) (StatsResult, error) {
	found, err := j.searchMBeans(ip, "org.apache.cassandra.metrics:type=ThreadPools,*")
	if err != nil {
		return StatsResult{}, err
	}
	if len(found) == 0 {
		return StatsResult{}, fmt.Errorf("no thread pool metric MBeans found under org.apache.cassandra.metrics:type=ThreadPools")
	}

	wanted := make(map[string]bool, len(tpStatsMetrics))
	for _, m := range tpStatsMetrics {
		wanted[m] = true
	}

	type poolKey struct{ path, scope string }
	// poolMBeans[pool][metricName] = the MBean name to read for that metric.
	// found is already target-only and prefix-stripped by searchMBeans.
	poolMBeans := map[poolKey]map[string]string{}
	for _, name := range found {
		attrs := objectNameAttributes(name)
		if !wanted[attrs["name"]] {
			continue
		}
		key := poolKey{path: attrs["path"], scope: attrs["scope"]}
		if poolMBeans[key] == nil {
			poolMBeans[key] = make(map[string]string, len(tpStatsMetrics))
		}
		poolMBeans[key][attrs["name"]] = name
	}
	if len(poolMBeans) == 0 {
		return StatsResult{}, fmt.Errorf("found thread pool MBeans but none matched %v", tpStatsMetrics)
	}

	var allMBeans []string
	for _, metrics := range poolMBeans {
		for _, mbean := range metrics {
			allMBeans = append(allMBeans, mbean)
		}
	}
	values, err := j.bulkReadGauges(ip, allMBeans)
	if err != nil {
		return StatsResult{}, err
	}

	pools := make([]poolKey, 0, len(poolMBeans))
	for k := range poolMBeans {
		pools = append(pools, k)
	}
	sort.Slice(pools, func(i, j int) bool {
		if pools[i].path != pools[j].path {
			return pools[i].path < pools[j].path
		}
		return pools[i].scope < pools[j].scope
	})

	rows := make([]StatRow, 0, len(pools))
	for _, pool := range pools {
		metrics := poolMBeans[pool]
		get := func(metric string) string {
			mbean, ok := metrics[metric]
			if !ok {
				return "n/a"
			}
			return values[mbean]
		}
		label := fmt.Sprintf("%s (%s)", pool.scope, pool.path)
		rows = append(rows, row(label,
			fmt.Sprintf("active=%s pending=%s completed=%s", get("ActiveTasks"), get("PendingTasks"), get("CompletedTasks"))))
	}

	return StatsResult{Groups: []StatGroup{{Name: "Thread pools", Rows: rows}}}, nil
}

// fetchNetStats matches `nodetool netstats`: cross-node messaging health --
// timeouts, per-peer pending message queues, and dropped-message counts by
// verb. All five attributes verified readable in one combined call against
// a live cluster (reading the MBean's *entire* attribute set in one go
// fails outright with "This feature has been removed", from some other
// attribute this never asks for -- so the read must name attributes
// explicitly, never omit the list to mean "all").
func fetchNetStats(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.net:type=MessagingService",
		"TotalTimeouts,TimeoutsPerHost,SmallMessagePendingTasks,LargeMessagePendingTasks,DroppedMessages")
	if err != nil {
		return StatsResult{}, err
	}
	var stats struct {
		TotalTimeouts            int
		TimeoutsPerHost          map[string]int64
		SmallMessagePendingTasks map[string]int
		LargeMessagePendingTasks map[string]int
		DroppedMessages          map[string]int
	}
	if err := json.Unmarshal(value, &stats); err != nil {
		return StatsResult{}, err
	}

	summaryRows := []StatRow{row("Total timeouts", stats.TotalTimeouts)}

	peers := make(map[string]bool)
	for peer := range stats.SmallMessagePendingTasks {
		peers[peer] = true
	}
	for peer := range stats.LargeMessagePendingTasks {
		peers[peer] = true
	}
	peerNames := make([]string, 0, len(peers))
	for peer := range peers {
		peerNames = append(peerNames, peer)
	}
	sort.Strings(peerNames)

	var peerRows []StatRow
	for _, peer := range peerNames {
		timeouts := stats.TimeoutsPerHost[peer]
		small := stats.SmallMessagePendingTasks[peer]
		large := stats.LargeMessagePendingTasks[peer]
		peerRows = append(peerRows, row(peer, fmt.Sprintf("timeouts=%d pending_small=%d pending_large=%d", timeouts, small, large)))
	}
	if len(peerRows) == 0 {
		peerRows = []StatRow{row("(none)", "no peers known")}
	}

	// DroppedMessages lists every verb Cassandra knows, which is a lot --
	// only non-zero counts are worth a user's attention.
	var droppedRows []StatRow
	verbs := make([]string, 0, len(stats.DroppedMessages))
	for verb, count := range stats.DroppedMessages {
		if count > 0 {
			verbs = append(verbs, verb)
		}
	}
	sort.Strings(verbs)
	for _, verb := range verbs {
		droppedRows = append(droppedRows, row(verb, stats.DroppedMessages[verb]))
	}
	if len(droppedRows) == 0 {
		droppedRows = []StatRow{row("(none)", "no dropped messages")}
	}

	return StatsResult{Groups: []StatGroup{
		{Name: "Summary", Rows: summaryRows},
		{Name: "Per-peer pending/timeouts", Rows: peerRows},
		{Name: "Dropped messages (non-zero only)", Rows: droppedRows},
	}}, nil
}

// fetchGCStats matches `nodetool gcstats`, sourced from the standard JVM
// GarbageCollector MBeans rather than a Cassandra-specific one -- guaranteed
// present on any JVM, and Cassandra doesn't expose its own equivalent
// (nodetool's own gcstats reads these same standard MBeans, just resets a
// baseline between calls for a delta view, which this doesn't attempt).
func fetchGCStats(j *Client, ip string) (StatsResult, error) {
	found, err := j.searchMBeans(ip, "java.lang:type=GarbageCollector,*")
	if err != nil {
		return StatsResult{}, err
	}
	if len(found) == 0 {
		return StatsResult{}, fmt.Errorf("no GarbageCollector MBeans found")
	}
	sort.Strings(found)

	rows := make([]StatRow, 0, len(found))
	for _, mbean := range found {
		value, err := j.readMBeanAttributes(ip, mbean, "CollectionCount,CollectionTime")
		if err != nil {
			rows = append(rows, row(objectNameAttributes(mbean)["name"], "unavailable: "+err.Error()))
			continue
		}
		var counts struct {
			CollectionCount int64
			CollectionTime  int64
		}
		if err := json.Unmarshal(value, &counts); err != nil {
			rows = append(rows, row(objectNameAttributes(mbean)["name"], "unavailable: "+err.Error()))
			continue
		}
		rows = append(rows, row(objectNameAttributes(mbean)["name"],
			fmt.Sprintf("collections=%d total_time_ms=%d", counts.CollectionCount, counts.CollectionTime)))
	}

	return StatsResult{Groups: []StatGroup{{Name: "Garbage collectors", Rows: rows}}}, nil
}

// proxyHistogramScopes are the base client-request types `nodetool
// proxyhistograms` reports (verified against a live cluster: the full
// ClientRequest metric set also includes a per-consistency-level breakdown
// for each of these, e.g. "Read-QUORUM", "Write-LOCAL_ONE" -- a different,
// more granular metric set nodetool's classic proxyhistograms doesn't
// surface, so this doesn't either).
var proxyHistogramScopes = []string{"Read", "Write", "RangeSlice", "CASRead", "CASWrite"}

// proxyHistogramPercentiles selects which fields of the Latency Timer to
// show, in display order. RecentValues (a raw sample buffer) is deliberately
// excluded -- verified present on a live cluster, but it's noise, not a stat.
var proxyHistogramPercentiles = []string{"Min", "50thPercentile", "75thPercentile", "95thPercentile", "98thPercentile", "99thPercentile", "999thPercentile", "Max"}

// fetchProxyHistograms matches `nodetool proxyhistograms`: latency
// percentiles for each client request type. This is the clearest candidate
// for a future chart -- each scope's row set is already a ready-made
// percentile distribution (see StatsResult's doc comment on why that's a
// generic, chartable shape rather than yet another bespoke one).
func fetchProxyHistograms(j *Client, ip string) (StatsResult, error) {
	groups := make([]StatGroup, 0, len(proxyHistogramScopes))
	for _, scope := range proxyHistogramScopes {
		mbean := fmt.Sprintf("org.apache.cassandra.metrics:name=Latency,scope=%s,type=ClientRequest", scope)
		value, err := j.readMBeanAttributes(ip, mbean, strings.Join(append([]string{"DurationUnit"}, proxyHistogramPercentiles...), ","))
		if err != nil {
			groups = append(groups, StatGroup{Name: scope, Rows: []StatRow{row("error", err.Error())}})
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal(value, &parsed); err != nil {
			groups = append(groups, StatGroup{Name: scope, Rows: []StatRow{row("error", err.Error())}})
			continue
		}
		unit, _ := parsed["DurationUnit"].(string)
		rows := make([]StatRow, 0, len(proxyHistogramPercentiles))
		for _, p := range proxyHistogramPercentiles {
			rows = append(rows, row(p, fmt.Sprintf("%v %s", parsed[p], unit)))
		}
		groups = append(groups, StatGroup{Name: scope, Rows: rows})
	}
	return StatsResult{Groups: groups}, nil
}

// cacheStatsGaugeMetrics and cacheStatsMeterMetrics are the per-cache
// Codahale metric MBeans this reads, keyed by their ObjectName "name=" value
// -- same one-MBean-per-(cache,metric) shape as tpStatsMetrics/tpstats. Split
// in two because they're not the same Codahale metric type: Capacity/Entries/
// Size/HitRate are Gauges (read via "Value"), but Requests/Hits are Meters
// (read via "Count") -- verified against a live cluster after Requests/Hits
// first came back "n/a" reading "Value" like the rest.
var cacheStatsGaugeMetrics = []string{"Capacity", "Entries", "Size", "HitRate"}
var cacheStatsMeterMetrics = []string{"Requests", "Hits"}

// fetchCacheStats matches the cache section of `nodetool info`: capacity,
// current size/entry count, and hit rate/request/hit counts for each of
// Cassandra's caches (key, row, counter). MBean set isn't fixed the same way
// tpstats' pool set isn't fixed, so this searches first, then bulk-reads.
func fetchCacheStats(j *Client, ip string) (StatsResult, error) {
	found, err := j.searchMBeans(ip, "org.apache.cassandra.metrics:type=Cache,*")
	if err != nil {
		return StatsResult{}, err
	}
	if len(found) == 0 {
		return StatsResult{}, fmt.Errorf("no cache metric MBeans found under org.apache.cassandra.metrics:type=Cache")
	}

	wanted := make(map[string]bool, len(cacheStatsGaugeMetrics)+len(cacheStatsMeterMetrics))
	for _, m := range cacheStatsGaugeMetrics {
		wanted[m] = true
	}
	for _, m := range cacheStatsMeterMetrics {
		wanted[m] = true
	}

	// cacheMBeans[cache scope][metric name] = the MBean name to read for that metric.
	cacheMBeans := map[string]map[string]string{}
	for _, name := range found {
		attrs := objectNameAttributes(name)
		if !wanted[attrs["name"]] {
			continue
		}
		scope := attrs["scope"]
		if cacheMBeans[scope] == nil {
			cacheMBeans[scope] = make(map[string]string, len(cacheStatsGaugeMetrics)+len(cacheStatsMeterMetrics))
		}
		cacheMBeans[scope][attrs["name"]] = name
	}
	if len(cacheMBeans) == 0 {
		return StatsResult{}, fmt.Errorf("found cache MBeans but none matched gauges=%v meters=%v", cacheStatsGaugeMetrics, cacheStatsMeterMetrics)
	}

	var gaugeMBeans, meterMBeans []string
	for _, metrics := range cacheMBeans {
		for metric, mbean := range metrics {
			if wantedIsMeter := metric == "Requests" || metric == "Hits"; wantedIsMeter {
				meterMBeans = append(meterMBeans, mbean)
			} else {
				gaugeMBeans = append(gaugeMBeans, mbean)
			}
		}
	}
	gaugeValues, err := j.bulkReadGauges(ip, gaugeMBeans)
	if err != nil {
		return StatsResult{}, err
	}
	meterValues, err := j.bulkReadMeterCounts(ip, meterMBeans)
	if err != nil {
		return StatsResult{}, err
	}
	values := make(map[string]string, len(gaugeValues)+len(meterValues))
	for k, v := range gaugeValues {
		values[k] = v
	}
	for k, v := range meterValues {
		values[k] = v
	}

	scopes := make([]string, 0, len(cacheMBeans))
	for scope := range cacheMBeans {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)

	groups := make([]StatGroup, 0, len(scopes))
	for _, scope := range scopes {
		metrics := cacheMBeans[scope]
		get := func(metric string) string {
			mbean, ok := metrics[metric]
			if !ok {
				return "n/a"
			}
			return values[mbean]
		}
		groups = append(groups, StatGroup{Name: scope, Rows: []StatRow{
			row("Capacity", get("Capacity")),
			row("Entries", get("Entries")),
			row("Size", get("Size")),
			row("Requests", get("Requests")),
			row("Hits", get("Hits")),
			row("Hit rate", get("HitRate")),
		}})
	}

	return StatsResult{Groups: groups}, nil
}

// settingKind is a settingSource's JMX attribute type -- needed on both ends
// of the get/set round trip: formatting the read value as plain text (an
// int-typed attribute like MaxHintWindow decodes from JSON as a Go float64,
// which Go's default float formatting renders as "1.08e+07" for a value like
// 10800000 -- not something you'd want to see, let alone edit, in the
// settings pane) and parsing an edited string back into the correctly-typed
// JSON value a JMX write expects (sending a string "10800000" where the
// attribute is declared numeric can be rejected outright).
type settingKind int

const (
	settingInt settingKind = iota
	settingFloat
	settingBool
)

// settingSource is one row of the "settings" pane -- a nodetool get*/set*
// pair, sourced as a single scalar JMX attribute.
type settingSource struct {
	Label string
	Kind  settingKind
	attrRead
}

// settingSources deliberately covers only simple scalar StorageService/
// StorageProxy attributes -- the same "get<Thing>()" shape nodetool's own
// get*/set* command family reads, which is why this pane exists: it's the
// place unusual per-node configuration drift (a throughput cap or timeout
// changed on one node via JMX/nodetool and never reconciled) would show up.
// Each row is read independently (bulkReadAttributes, not readMBeanAttributes)
// so a renamed/missing attribute on one row degrades to "unavailable" for
// that row alone rather than failing the whole pane -- deliberately
// defensive here since, unlike the other fetchers above, several of these
// exact attribute names weren't independently re-verified against a live
// cluster before landing (JMX connectivity issues blocked ad hoc probing
// outside the normal Prober->Jolokia call path during development -- see
// nodetool-ui-recap.md). If a row here is consistently "unavailable" against
// a real cluster, that's this list's attribute name being wrong, not a
// runtime bug -- fix the name in settingSources, nothing else.
var settingSources = []settingSource{
	{"Compaction throughput (MB/s)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageService", "CompactionThroughputMbPerSec"}},
	{"Batchlog replay throttle (KB)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageService", "BatchlogReplayThrottleInKB"}},
	{"Trace probability", settingFloat, attrRead{"org.apache.cassandra.db:type=StorageService", "TraceProbability"}},
	{"Hinted handoff enabled", settingBool, attrRead{"org.apache.cassandra.db:type=StorageProxy", "HintedHandoffEnabled"}},
	{"Max hint window (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "MaxHintWindow"}},
	{"RPC timeout (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "RpcTimeout"}},
	{"Read RPC timeout (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "ReadRpcTimeout"}},
	{"Write RPC timeout (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "WriteRpcTimeout"}},
	{"Range RPC timeout (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "RangeRpcTimeout"}},
	{"Counter write RPC timeout (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "CounterWriteRpcTimeout"}},
	{"Truncate RPC timeout (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "TruncateRpcTimeout"}},
	{"CAS contention timeout (ms)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageProxy", "CasContentionTimeout"}},
	{"Stream throughput (MB/s)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageService", "StreamThroughputMbPerSec"}},
	{"Inter-DC stream throughput (MB/s)", settingInt, attrRead{"org.apache.cassandra.db:type=StorageService", "InterDCStreamThroughputMbPerSec"}},
	{"Concurrent compactors", settingInt, attrRead{"org.apache.cassandra.db:type=StorageService", "ConcurrentCompactors"}},
}

// settingSourceByLabel finds a settingSources entry by its display label --
// the identifier the frontend already has from the read side (fetchSettings'
// row labels) and sends back for a write, rather than inventing a separate
// machine-readable key the two ends would need to keep in sync.
func settingSourceByLabel(label string) (settingSource, bool) {
	for _, s := range settingSources {
		if s.Label == label {
			return s, true
		}
	}
	return settingSource{}, false
}

// formatSettingValue renders a raw JMX read as plain text per its Kind --
// see settingKind's doc comment for why this can't just be fmt.Sprint(any).
func formatSettingValue(kind settingKind, raw json.RawMessage) (string, error) {
	switch kind {
	case settingBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return "", err
		}
		return strconv.FormatBool(b), nil
	case settingFloat:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return "", err
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	default: // settingInt
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return "", err
		}
		return strconv.FormatInt(int64(f), 10), nil
	}
}

// parseSettingValue is formatSettingValue's inverse: turns an edited string
// from the settings pane back into the correctly-typed JSON value a JMX
// write for that attribute expects.
//
// settingFloat is deliberately NOT "return strconv.ParseFloat(raw, 64)" --
// verified against a live cluster after writing TraceProbability (a double
// attribute) failed outright for any whole-number value ("0", "0.0", "1"):
// Go's default float64->JSON marshaling drops the decimal point for a whole
// number (0.0 marshals as the bare literal "0"), and Jolokia infers a bare
// integer literal as a Java Long, then refuses the Long->Double conversion
// server-side ("Cannot convert a java.lang.Long value to java.lang.Double").
// Formatting the parsed value back to a string and forcing a "." keeps the
// JSON literal unambiguously a Double no matter what the user typed.
func parseSettingValue(kind settingKind, raw string) (any, error) {
	switch kind {
	case settingBool:
		return strconv.ParseBool(raw)
	case settingFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, err
		}
		literal := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(literal, ".") {
			literal += ".0"
		}
		return json.RawMessage(literal), nil
	default: // settingInt
		return strconv.ParseInt(raw, 10, 64)
	}
}

// fetchSettings is the "pane for all the get/set * commands": one row per
// nodetool get<Thing> equivalent, read fresh from this node. The frontend is
// what actually makes this useful -- running it against every node at once
// and highlighting any row whose value isn't identical everywhere (see
// StatDef.Diffable and formatStatsResult's diffMap in the UI).
func fetchSettings(j *Client, ip string) (StatsResult, error) {
	reads := make([]attrRead, len(settingSources))
	for i, s := range settingSources {
		reads[i] = s.attrRead
	}
	raws, err := j.bulkReadAttributes(ip, reads)
	if err != nil {
		return StatsResult{}, err
	}

	rows := make([]StatRow, 0, len(settingSources))
	for i, s := range settingSources {
		if i >= len(raws) {
			rows = append(rows, row(s.Label, "unavailable"))
			continue
		}
		raw := raws[i]
		if raw.Status != http.StatusOK {
			rows = append(rows, row(s.Label, "unavailable"))
			continue
		}
		formatted, err := formatSettingValue(s.Kind, raw.Value)
		if err != nil {
			rows = append(rows, row(s.Label, "unavailable"))
			continue
		}
		rows = append(rows, row(s.Label, formatted))
	}

	return StatsResult{Groups: []StatGroup{{Rows: rows}}}, nil
}

// SetSettings is fetchSettings' write counterpart: applies new values to a
// subset of settingSources' rows, keyed by Label (changes only ever contains
// rows the UI's caller actually edited -- see ui/static/index.html's Set*
// panel). Each field validates and writes independently -- an unknown label,
// a value that doesn't parse for that field's Kind, or a JMX write rejected
// by the node is reported for that field alone in the returned map (label ->
// error message; a label absent from the result succeeded) and never blocks
// any other field in the same call.
func SetSettings(j *Client, ip string, changes map[string]string) (map[string]string, error) {
	errs := make(map[string]string)
	labels := make([]string, 0, len(changes))
	writes := make([]attrWrite, 0, len(changes))
	for label, raw := range changes {
		source, ok := settingSourceByLabel(label)
		if !ok {
			errs[label] = "unknown setting"
			continue
		}
		value, err := parseSettingValue(source.Kind, raw)
		if err != nil {
			errs[label] = err.Error()
			continue
		}
		labels = append(labels, label)
		writes = append(writes, attrWrite{attrRead: source.attrRead, Value: value})
	}
	if len(writes) == 0 {
		return errs, nil
	}

	raws, err := j.bulkWriteAttributes(ip, writes)
	if err != nil {
		return errs, err
	}
	for i, label := range labels {
		if i >= len(raws) {
			break
		}
		if raws[i].Status != http.StatusOK {
			errs[label] = raws[i].Error
		}
	}
	return errs, nil
}

// statusFlagSources are the boolean cluster-membership/policy flags behind
// `nodetool statusgossip`/`statusbinary`/`statusbackup`, plus whether the
// node has joined the ring -- all simple boolean StorageService attributes,
// same "bounded scalar reads" shape as settingSources. Read independently
// (bulkReadAttributes) for the same reason settingSources' rows are: these
// exact attribute names haven't been re-verified against a live cluster as
// of writing (see the "guess, deploy, curl, fix" note on settingSources
// above), so a wrong guess should degrade one row, not the whole pane.
var statusFlagSources = []settingSource{
	{"Gossip running", settingBool, attrRead{"org.apache.cassandra.db:type=StorageService", "GossipRunning"}},
	{"Native transport running", settingBool, attrRead{"org.apache.cassandra.db:type=StorageService", "NativeTransportRunning"}},
	{"Incremental backups enabled", settingBool, attrRead{"org.apache.cassandra.db:type=StorageService", "IncrementalBackupsEnabled"}},
	{"Joined ring", settingBool, attrRead{"org.apache.cassandra.db:type=StorageService", "Joined"}},
}

// fetchStatusFlags matches `nodetool statusgossip`/`statusbinary`/
// `statusbackup` plus ring-membership, as one combined read instead of three
// separate nodetool invocations' worth of catalog entries -- these are all
// the same "is this policy/membership flag on" shape, so one row group
// covers all of them.
func fetchStatusFlags(j *Client, ip string) (StatsResult, error) {
	reads := make([]attrRead, len(statusFlagSources))
	for i, s := range statusFlagSources {
		reads[i] = s.attrRead
	}
	raws, err := j.bulkReadAttributes(ip, reads)
	if err != nil {
		return StatsResult{}, err
	}

	rows := make([]StatRow, 0, len(statusFlagSources))
	for i, s := range statusFlagSources {
		if i >= len(raws) || raws[i].Status != http.StatusOK {
			rows = append(rows, row(s.Label, "unavailable"))
			continue
		}
		formatted, err := formatSettingValue(s.Kind, raws[i].Value)
		if err != nil {
			rows = append(rows, row(s.Label, "unavailable"))
			continue
		}
		rows = append(rows, row(s.Label, formatted))
	}

	return StatsResult{Groups: []StatGroup{{Rows: rows}}}, nil
}

// compactionHistoryLimit bounds how many rows fetchCompactionHistory shows --
// same "fetch everything" concern as cfstats/tablehistograms (see the
// deliberately-not-implemented note above), except here there's an obvious,
// non-arbitrary bound: most recent compactions are what anyone asking this
// question actually wants, so capping by recency (rather than needing a
// keyspace/table scoping param first) is enough on its own.
const compactionHistoryLimit = 15

// fetchCompactionHistory matches `nodetool compactionhistory`: the most
// recent completed compactions, newest first. Two real things verified
// against a live cluster, neither guessable from the other stats fetchers'
// precedent:
//   - The attribute lives on CompactionManager, not StorageService (an
//     initial guess by analogy with fetchCompactionStats' CompactionSummary/
//     PendingTasks split -- came back "No such attribute").
//   - CompactionHistory's TabularData has a *composite* index (id,
//     keyspace_name, columnfamily_name, compacted_at, bytes_in, bytes_out,
//     rows_merged), which Jolokia serializes as one nested map per index
//     column rather than a flat array of row objects (every other TabularData/
//     composite read elsewhere in this file happens to have a single-column
//     index, which is why this shape hadn't shown up yet). Handled generically
//     by flattenCompactionHistoryRows recursing until it finds a map that
//     looks like a full row, rather than hard-coding the nesting depth/order.
func fetchCompactionHistory(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.db:type=CompactionManager", "CompactionHistory")
	if err != nil {
		return StatsResult{}, err
	}
	var root any
	if err := json.Unmarshal(value, &root); err != nil {
		return StatsResult{}, err
	}
	var history []map[string]any
	flattenCompactionHistoryRows(root, &history)
	if len(history) == 0 {
		return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("(none)", "no compaction history")}}}}, nil
	}

	sort.Slice(history, func(i, k int) bool {
		return compactionHistoryTimestamp(history[i]) > compactionHistoryTimestamp(history[k])
	})
	if len(history) > compactionHistoryLimit {
		history = history[:compactionHistoryLimit]
	}

	rows := make([]StatRow, 0, len(history))
	for _, entry := range history {
		label := fmt.Sprintf("%v.%v", entry["keyspace_name"], entry["columnfamily_name"])
		when := "unknown time"
		if ms := compactionHistoryTimestamp(entry); ms > 0 {
			when = time.UnixMilli(ms).Format(time.RFC3339)
		}
		rows = append(rows, row(label, fmt.Sprintf("%s, %s -> %s bytes, rows merged %v",
			when, compactionHistoryCount(entry["bytes_in"]), compactionHistoryCount(entry["bytes_out"]), entry["rows_merged"])))
	}

	return StatsResult{Groups: []StatGroup{{Name: fmt.Sprintf("Most recent %d", len(rows)), Rows: rows}}}, nil
}

// flattenCompactionHistoryRows recurses through Jolokia's nested-map
// serialization of CompactionHistory's composite-indexed TabularData,
// collecting every leaf map that looks like a full row (has both
// "keyspace_name" and "compacted_at") into out -- see fetchCompactionHistory's
// doc comment for why this can't just be a single json.Unmarshal into
// []map[string]any like a simpler, single-column-index TabularData could.
func flattenCompactionHistoryRows(v any, out *[]map[string]any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if _, hasKeyspace := m["keyspace_name"]; hasKeyspace {
		if _, hasCompactedAt := m["compacted_at"]; hasCompactedAt {
			*out = append(*out, m)
			return
		}
	}
	for _, child := range m {
		flattenCompactionHistoryRows(child, out)
	}
}

func compactionHistoryTimestamp(entry map[string]any) int64 {
	switch t := entry["compacted_at"].(type) {
	case float64:
		return int64(t)
	case string:
		ms, _ := strconv.ParseInt(t, 10, 64)
		return ms
	default:
		return 0
	}
}

// compactionHistoryCount formats a byte-count field as a plain integer --
// same "avoid Go's default float64 formatting" concern formatSettingValue's
// settingInt case exists for (a large byte count as JSON decodes to float64,
// and fmt.Sprint/%v on that can render as "7.87e+07" instead of "78700000").
func compactionHistoryCount(v any) string {
	f, ok := v.(float64)
	if !ok {
		return fmt.Sprint(v)
	}
	return strconv.FormatInt(int64(f), 10)
}

// fetchLoggingLevels matches `nodetool getlogginglevels`: every logger's
// current effective level. StorageServiceMBean.getLoggingLevels() looks like
// an operation by name, but standard-MBean reflection treats any no-arg
// "getX"/"isX" interface method as attribute "X" rather than an operation --
// verified live: calling it via exec fails ("getLoggingLevels", Jolokia's
// bare not-found-as-operation error), while reading it as the "LoggingLevels"
// attribute (like every other get*-named StorageService entry in
// settingSources) succeeds. beginLocalSampling/finishLocalSampling
// (fetchTopPartitions) are verb-named, not get/is-prefixed, so that
// convention doesn't apply to them -- they're genuine operations and do need
// exec.
func fetchLoggingLevels(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.db:type=StorageService", "LoggingLevels")
	if err != nil {
		return StatsResult{}, err
	}
	var levels map[string]string
	if err := json.Unmarshal(value, &levels); err != nil {
		return StatsResult{}, err
	}

	names := make([]string, 0, len(levels))
	for name := range levels {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]StatRow, len(names))
	for i, name := range names {
		rows[i] = row(name, levels[name])
	}
	return StatsResult{Groups: []StatGroup{{Rows: rows}}}, nil
}

// splitKeyspaceTable parses the "keyspace.table" target both cfstats and
// cfhistograms take -- the one thing every other Catalog entry doesn't need,
// since they run against a node with no further scoping. A bare split on the
// first "." is enough: neither keyspace nor table names can themselves
// contain a "." in Cassandra.
// ListTables returns every "keyspace.table" pair the target node knows
// about, sorted -- sourced from the same per-table metric MBeans fetchCfStats
// reads. Searching for exactly one metric name (LiveSSTableCount) picks up
// one MBean per table rather than one per (table, metric) the way an
// unfiltered "type=Table,*" search would, so this needs no new JMX call
// shape beyond searchMBeans. Backs the frontend's table picker (see
// ui/static/index.html's Tables panel) so cfstats/cfhistograms' target
// doesn't have to be typed blind.
func ListTables(j *Client, ip string) ([]string, error) {
	found, err := j.searchMBeans(ip, "org.apache.cassandra.metrics:type=Table,keyspace=*,scope=*,name=LiveSSTableCount")
	if err != nil {
		return nil, err
	}
	tables := make([]string, 0, len(found))
	for _, mbean := range found {
		attrs := objectNameAttributes(mbean)
		if attrs["keyspace"] == "" || attrs["scope"] == "" {
			continue
		}
		tables = append(tables, attrs["keyspace"]+"."+attrs["scope"])
	}
	sort.Strings(tables)
	return tables, nil
}

func splitKeyspaceTable(table string) (keyspace, tableName string, err error) {
	keyspace, tableName, found := strings.Cut(table, ".")
	if !found || keyspace == "" || tableName == "" {
		return "", "", fmt.Errorf("expected a keyspace.table target, got %q", table)
	}
	return keyspace, tableName, nil
}

// tableStatsGaugeMetrics/tableStatsCounterMetrics are the per-table Codahale
// metrics fetchCfStats reads, split by metric type the same way cachestats'
// are (cacheStatsGaugeMetrics/cacheStatsMeterMetrics): Gauges read via
// "Value", but the disk-space ones are Counters (monotonic byte totals),
// read via "Count" -- bulkReadMeterCounts despite the name, since a Counter
// and a Meter expose their number under the same JMX attribute.
var tableStatsGaugeMetrics = []string{"LiveSSTableCount", "PendingCompactions", "CompressionRatio", "PercentRepaired"}
var tableStatsCounterMetrics = []string{"LiveDiskSpaceUsed", "TotalDiskSpaceUsed"}

// fetchCfStats matches (a bounded subset of) `nodetool tablestats
// <keyspace.table>`: SSTable count, disk space used, pending compactions,
// compression ratio, percent repaired, plus read/write latency percentiles
// (the same Timer-percentile shape fetchProxyHistograms already established
// for ClientRequest metrics, just against this table's own ReadLatency/
// WriteLatency MBeans instead). Deliberately a subset, not full nodetool
// parity -- memtable stats, bloom filter stats, and a few others from the
// real command aren't included; add them the same way if they turn out to
// be wanted (search+bulk-read as below, extend the metric lists).
func fetchCfStats(j *Client, ip, table string) (StatsResult, error) {
	keyspace, tableName, err := splitKeyspaceTable(table)
	if err != nil {
		return StatsResult{}, err
	}

	pattern := fmt.Sprintf("org.apache.cassandra.metrics:type=Table,keyspace=%s,scope=%s,*", keyspace, tableName)
	found, err := j.searchMBeans(ip, pattern)
	if err != nil {
		return StatsResult{}, err
	}
	if len(found) == 0 {
		return StatsResult{}, fmt.Errorf("no metrics found for table %s.%s -- check the keyspace/table name", keyspace, tableName)
	}

	byMetric := make(map[string]string, len(found))
	for _, mbean := range found {
		byMetric[objectNameAttributes(mbean)["name"]] = mbean
	}

	var gaugeMBeans, counterMBeans []string
	for _, m := range tableStatsGaugeMetrics {
		if mbean, ok := byMetric[m]; ok {
			gaugeMBeans = append(gaugeMBeans, mbean)
		}
	}
	for _, m := range tableStatsCounterMetrics {
		if mbean, ok := byMetric[m]; ok {
			counterMBeans = append(counterMBeans, mbean)
		}
	}
	gaugeValues, err := j.bulkReadGauges(ip, gaugeMBeans)
	if err != nil {
		return StatsResult{}, err
	}
	counterValues, err := j.bulkReadMeterCounts(ip, counterMBeans)
	if err != nil {
		return StatsResult{}, err
	}

	get := func(metric string, values map[string]string) string {
		mbean, ok := byMetric[metric]
		if !ok {
			return "n/a"
		}
		v, ok := values[mbean]
		if !ok {
			return "n/a"
		}
		return v
	}

	groups := []StatGroup{{Name: "Summary", Rows: []StatRow{
		row("SSTable count", get("LiveSSTableCount", gaugeValues)),
		row("Space used (live, bytes)", get("LiveDiskSpaceUsed", counterValues)),
		row("Space used (total, bytes)", get("TotalDiskSpaceUsed", counterValues)),
		row("Pending compactions", get("PendingCompactions", gaugeValues)),
		row("Compression ratio", get("CompressionRatio", gaugeValues)),
		row("Percent repaired", get("PercentRepaired", gaugeValues)),
	}}}

	for _, latency := range []struct{ label, metric string }{
		{"Read latency", "ReadLatency"},
		{"Write latency", "WriteLatency"},
	} {
		mbean, ok := byMetric[latency.metric]
		if !ok {
			continue
		}
		value, err := j.readMBeanAttributes(ip, mbean, strings.Join(append([]string{"DurationUnit"}, proxyHistogramPercentiles...), ","))
		if err != nil {
			groups = append(groups, StatGroup{Name: latency.label, Rows: []StatRow{row("error", err.Error())}})
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal(value, &parsed); err != nil {
			groups = append(groups, StatGroup{Name: latency.label, Rows: []StatRow{row("error", err.Error())}})
			continue
		}
		unit, _ := parsed["DurationUnit"].(string)
		rows := make([]StatRow, 0, len(proxyHistogramPercentiles))
		for _, p := range proxyHistogramPercentiles {
			rows = append(rows, row(p, fmt.Sprintf("%v %s", parsed[p], unit)))
		}
		groups = append(groups, StatGroup{Name: latency.label, Rows: rows})
	}

	return StatsResult{Groups: groups}, nil
}

// tableHistogramMetrics are the per-table percentile-distribution metrics
// fetchTableHistograms reads -- Read/WriteLatency are Timers (have a
// DurationUnit, same as fetchProxyHistograms' ClientRequest metrics),
// SSTablesPerReadHistogram is a plain Codahale Histogram (no DurationUnit,
// but the same percentile attribute set otherwise). Deliberately excludes
// EstimatedPartitionSizeHistogram/EstimatedColumnCountHistogram -- verified
// against a live cluster that these are NOT simple percentile-attribute
// MBeans like the three below (Jolokia returns a raw bucket-offset/count
// array instead), which needs its own rendering (a real histogram chart, not
// a percentile table) rather than reusing this fetcher's shape.
var tableHistogramMetrics = []struct {
	Label, Metric string
	IsDuration    bool
}{
	{"Read latency", "ReadLatency", true},
	{"Write latency", "WriteLatency", true},
	{"SSTables per read", "SSTablesPerReadHistogram", false},
}

// fetchTableHistograms matches (a bounded subset of) `nodetool
// tablehistograms <keyspace.table>` -- see tableHistogramMetrics' doc
// comment for the scoping decision on which metrics this covers.
func fetchTableHistograms(j *Client, ip, table string) (StatsResult, error) {
	keyspace, tableName, err := splitKeyspaceTable(table)
	if err != nil {
		return StatsResult{}, err
	}

	groups := make([]StatGroup, 0, len(tableHistogramMetrics))
	for _, metric := range tableHistogramMetrics {
		mbean := fmt.Sprintf("org.apache.cassandra.metrics:type=Table,keyspace=%s,scope=%s,name=%s", keyspace, tableName, metric.Metric)
		attrs := proxyHistogramPercentiles
		if metric.IsDuration {
			attrs = append([]string{"DurationUnit"}, proxyHistogramPercentiles...)
		}
		value, err := j.readMBeanAttributes(ip, mbean, strings.Join(attrs, ","))
		if err != nil {
			groups = append(groups, StatGroup{Name: metric.Label, Rows: []StatRow{row("error", err.Error())}})
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal(value, &parsed); err != nil {
			groups = append(groups, StatGroup{Name: metric.Label, Rows: []StatRow{row("error", err.Error())}})
			continue
		}
		unit, _ := parsed["DurationUnit"].(string)
		rows := make([]StatRow, 0, len(proxyHistogramPercentiles))
		for _, p := range proxyHistogramPercentiles {
			if unit != "" {
				rows = append(rows, row(p, fmt.Sprintf("%v %s", parsed[p], unit)))
			} else {
				rows = append(rows, row(p, fmt.Sprintf("%v", parsed[p])))
			}
		}
		groups = append(groups, StatGroup{Name: metric.Label, Rows: rows})
	}

	return StatsResult{Groups: groups}, nil
}

// topPartitionsSamplers are the sampler kinds `nodetool toppartitions`
// reports. Verified against Cassandra 4.1.12's actual MBean interfaces (not
// guessed): StorageServiceMBean.samplePartitions is cluster/node-wide with no
// keyspace/table scoping, so it's not what nodetool's keyspace/cfname-scoped
// CLI form actually calls -- that form drives ColumnFamilyStoreMBean's
// beginLocalSampling(sampler, capacity, durationMillis)/
// finishLocalSampling(sampler, count) pair on the target table's own MBean
// instead, one round per sampler kind here.
var topPartitionsSamplers = []struct{ Sampler, Label string }{
	{"READS", "Frequency of reads by partition"},
	{"WRITES", "Frequency of writes by partition"},
	{"CAS_CONTENTIONS", "Frequency of CAS contentions by partition"},
	{"WRITE_SIZE", "Max mutation size by partition"},
	{"LOCAL_READ_TIME", "Longest local read query times"},
}

// capacity/count/duration match nodetool toppartitions' own defaults
// (-s/-k/<duration>) closely enough for an on-demand UI click; not exposed
// as params since StatDef.FetchTable takes no extra arguments (see its doc
// comment) -- add a param if a future need justifies it.
const (
	topPartitionsCapacity = 256
	topPartitionsCount    = 10
	topPartitionsDuration = 4 * time.Second
)

// fetchTopPartitions matches `nodetool toppartitions <keyspace> <cfname>
// <duration>`. Every sampler kind's CompositeData row has the same three
// fields (value/count/error -- Cassandra's own Sampler.Sample, verified via
// the jar's class constants) regardless of sampler; shown uniformly here
// rather than special-cased per sampler like nodetool's own column headers
// (Partition/Count, Partition/Bytes, Query/Microseconds), matching this
// file's one-generic-row-shape approach elsewhere.
func fetchTopPartitions(j *Client, ip, table string) (StatsResult, error) {
	keyspace, tableName, err := splitKeyspaceTable(table)
	if err != nil {
		return StatsResult{}, err
	}
	mbean := fmt.Sprintf("org.apache.cassandra.db:type=Tables,keyspace=%s,table=%s", keyspace, tableName)

	beginCalls := make([]opCall, len(topPartitionsSamplers))
	for i, s := range topPartitionsSamplers {
		beginCalls[i] = opCall{Mbean: mbean, Operation: "beginLocalSampling",
			Arguments: []any{s.Sampler, topPartitionsCapacity, int(topPartitionsDuration.Milliseconds())}}
	}
	beginRaws, err := j.bulkExec(ip, beginCalls)
	if err != nil {
		return StatsResult{}, err
	}
	for i, raw := range beginRaws {
		if raw.Status != http.StatusOK {
			return StatsResult{}, fmt.Errorf("could not sample %s.%s (sampler %s): %s -- check the keyspace/table name",
				keyspace, tableName, topPartitionsSamplers[i].Sampler, raw.Error)
		}
	}

	time.Sleep(topPartitionsDuration)

	finishCalls := make([]opCall, len(topPartitionsSamplers))
	for i, s := range topPartitionsSamplers {
		finishCalls[i] = opCall{Mbean: mbean, Operation: "finishLocalSampling", Arguments: []any{s.Sampler, topPartitionsCount}}
	}
	raws, err := j.bulkExec(ip, finishCalls)
	if err != nil {
		return StatsResult{}, err
	}

	groups := make([]StatGroup, 0, len(topPartitionsSamplers))
	for i, s := range topPartitionsSamplers {
		if i >= len(raws) {
			groups = append(groups, StatGroup{Name: s.Label, Rows: []StatRow{row("error", "unavailable")}})
			continue
		}
		if raws[i].Status != http.StatusOK {
			groups = append(groups, StatGroup{Name: s.Label, Rows: []StatRow{row("error", raws[i].Error)}})
			continue
		}
		var samples []struct {
			Value string  `json:"value"`
			Count float64 `json:"count"`
			Error float64 `json:"error"`
		}
		if err := json.Unmarshal(raws[i].Value, &samples); err != nil {
			groups = append(groups, StatGroup{Name: s.Label, Rows: []StatRow{row("error", err.Error())}})
			continue
		}
		if len(samples) == 0 {
			groups = append(groups, StatGroup{Name: s.Label, Rows: []StatRow{row("(none)", "nothing recorded during sampling period")}})
			continue
		}
		rows := make([]StatRow, len(samples))
		for k, sample := range samples {
			rows[k] = row(sample.Value, fmt.Sprintf("%d +/- %d", int64(sample.Count), int64(sample.Error)))
		}
		groups = append(groups, StatGroup{Name: s.Label, Rows: rows})
	}

	return StatsResult{Groups: groups}, nil
}

// fetchDescribeRing and fetchEffectiveOwnership are keyspace-scoped, not
// table-scoped -- nodetool's own `describering <keyspace>` and the
// ownership-% computation both key off the keyspace's replication settings
// alone. They reuse RequiresTable/FetchTable (splitting the "keyspace.table"
// target and discarding tableName) instead of a parallel RequiresKeyspace
// scoping, since that would need its own frontend picker and route param for
// a need this narrow -- the one cost is that selecting several tables from
// the *same* keyspace in the Tables panel reruns the same keyspace-scoped
// query once per selected table (redundant but harmless, each tab still
// correct) rather than deduping to one.
//
// This corrects an earlier, incomplete assumption (see git history) that
// "ownership %" had no real JMX-exposed number and would need to be computed
// here from replication factor/topology by hand. StorageServiceMBean has
// exactly that number already computed server-side --
// effectiveOwnership(keyspace) -- verified against Cassandra 4.1.12's own
// jar and a live cluster; no need to reimplement NetworkTopologyStrategy math
// in this package.

// fetchDescribeRing matches `nodetool describering <keyspace>`: every token
// range's owning endpoints, via StorageServiceMBean.describeRingJMX(keyspace).
// Verified live that this returns a List<String> of already-formatted
// TokenRange.toString() entries (the same text nodetool's own CLI prints
// verbatim), not structured CompositeData like every other TabularData/
// composite read elsewhere in this file -- despite the "JMX" name suggesting
// otherwise. Shown as-is rather than regex-parsed back into fields: Cassandra
// already did the formatting, and re-parsing its toString() format would
// just be a second, more fragile way to get the same information (breaks
// silently if that format ever changes across versions). Left uncapped and
// in the MBean's own return order, same as nodetool's own describering
// output -- unlike compactionhistory, there's no "most recent N" style
// cutoff that makes sense for a ring (every range is equally relevant).
func fetchDescribeRing(j *Client, ip, table string) (StatsResult, error) {
	keyspace, _, err := splitKeyspaceTable(table)
	if err != nil {
		return StatsResult{}, err
	}

	raws, err := j.bulkExec(ip, []opCall{{
		Mbean: "org.apache.cassandra.db:type=StorageService", Operation: "describeRingJMX", Arguments: []any{keyspace},
	}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("describering failed for keyspace %s: %s -- check the keyspace name", keyspace, raws[0].Error)
	}

	var ranges []string
	if err := json.Unmarshal(raws[0].Value, &ranges); err != nil {
		return StatsResult{}, err
	}
	rows := make([]StatRow, len(ranges))
	for i, r := range ranges {
		rows[i] = row(fmt.Sprintf("Range %d", i+1), r)
	}
	return StatsResult{Groups: []StatGroup{{Name: fmt.Sprintf("%d token ranges", len(rows)), Rows: rows}}}, nil
}

// fetchEffectiveOwnership matches the "Owns" column of `nodetool ring
// <keyspace>`/`nodetool status <keyspace>`: each endpoint's effective
// ownership percentage for the target keyspace, via
// StorageServiceMBean.effectiveOwnership(keyspace) -- a real, already-
// replication-factor-and-topology-aware number Cassandra computes itself
// (Map<InetAddress,Float>), not something this package recomputes from raw
// tokens.
func fetchEffectiveOwnership(j *Client, ip, table string) (StatsResult, error) {
	keyspace, _, err := splitKeyspaceTable(table)
	if err != nil {
		return StatsResult{}, err
	}

	raws, err := j.bulkExec(ip, []opCall{{
		Mbean: "org.apache.cassandra.db:type=StorageService", Operation: "effectiveOwnership", Arguments: []any{keyspace},
	}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("effective ownership failed for keyspace %s: %s -- check the keyspace name", keyspace, raws[0].Error)
	}

	var ownership map[string]float64
	if err := json.Unmarshal(raws[0].Value, &ownership); err != nil {
		return StatsResult{}, err
	}

	endpoints := make([]string, 0, len(ownership))
	for endpoint := range ownership {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)
	rows := make([]StatRow, len(endpoints))
	for i, endpoint := range endpoints {
		rows[i] = row(strings.TrimPrefix(endpoint, "/"), fmt.Sprintf("%.2f%%", ownership[endpoint]*100))
	}
	return StatsResult{Groups: []StatGroup{{Rows: rows}}}, nil
}

// fetchReloadSeeds matches `nodetool reloadseeds`: re-reads the seed list
// from the configured seed provider and returns the resulting list. Unlike
// everything else in this catalog, this *mutates* node-local gossip state
// rather than only reading it -- StatDef.Confirm/SingleTarget gate it in the
// frontend accordingly (see their doc comments). The operation itself lives
// on GossiperMBean, not StorageServiceMBean like most of this file --
// verified against the jar's class constants (GossiperMBean.reloadSeeds(),
// object name "org.apache.cassandra.net:type=Gossiper" from Gossiper's own
// registration string, not the "org.apache.cassandra.gms" package name a
// guess-by-analogy would produce).
func fetchReloadSeeds(j *Client, ip string) (StatsResult, error) {
	raws, err := j.bulkExec(ip, []opCall{{Mbean: "org.apache.cassandra.net:type=Gossiper", Operation: "reloadSeeds"}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("reloadSeeds failed: %s", raws[0].Error)
	}

	var seeds []string
	if err := json.Unmarshal(raws[0].Value, &seeds); err != nil {
		return StatsResult{}, err
	}
	return StatsResult{Groups: []StatGroup{{Name: fmt.Sprintf("%d seeds after reload", len(seeds)), Rows: []StatRow{row("Seeds", listValue(seeds))}}}}, nil
}

// fetchSeeds matches `nodetool getseeds`: the read-only complement to
// reloadseeds, on the same GossiperMBean. "Seeds" is a zero-arg get-prefixed
// method, so it's an attribute (same convention that made getlogginglevels
// an attribute read rather than an exec), not an operation.
func fetchSeeds(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.net:type=Gossiper", "Seeds")
	if err != nil {
		return StatsResult{}, err
	}
	var seeds []string
	if err := json.Unmarshal(value, &seeds); err != nil {
		return StatsResult{}, err
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("Seeds", listValue(seeds))}}}}, nil
}

// fetchFailureDetector matches `nodetool failuredetector`: each endpoint's
// phi-accrual conviction value, from FailureDetectorMBean's "PhiValues"
// attribute (a zero-arg get-prefixed TabularData read, same convention as
// every other attribute here). Verified live that -- unlike
// compactionhistory's composite-index TabularData (nested maps) or every
// other single-column TabularData elsewhere in this file (flat array) --
// Jolokia serializes this single-column-indexed one as a JSON *object* keyed
// by the index value (the endpoint address) instead, e.g.
// {"/10.0.0.1": {"Endpoint": "/10.0.0.1", "PHI": 0.18}, ...}.
func fetchFailureDetector(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.net:type=FailureDetector", "PhiValues")
	if err != nil {
		return StatsResult{}, err
	}
	var byEndpoint map[string]struct {
		Endpoint string
		PHI      float64
	}
	if err := json.Unmarshal(value, &byEndpoint); err != nil {
		return StatsResult{}, err
	}
	if len(byEndpoint) == 0 {
		return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("(none)", "no endpoints")}}}}, nil
	}
	endpoints := make([]string, 0, len(byEndpoint))
	for endpoint := range byEndpoint {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)
	rows := make([]StatRow, len(endpoints))
	for i, endpoint := range endpoints {
		rows[i] = row(strings.TrimPrefix(endpoint, "/"), fmt.Sprintf("%.4f", byEndpoint[endpoint].PHI))
	}
	return StatsResult{Groups: []StatGroup{{Rows: rows}}}, nil
}

// fetchPendingHints matches `nodetool listpendinghints`, from
// HintsServiceMBean's "PendingHints" attribute (List<Map<String,String>>,
// one map per target endpoint with keys verified live rather than guessed).
func fetchPendingHints(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.hints:type=HintsService", "PendingHints")
	if err != nil {
		return StatsResult{}, err
	}
	var entries []map[string]string
	if err := json.Unmarshal(value, &entries); err != nil {
		return StatsResult{}, err
	}
	if len(entries) == 0 {
		return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("(none)", "no pending hints")}}}}, nil
	}
	rows := make([]StatRow, len(entries))
	for i, e := range entries {
		keys := make([]string, 0, len(e))
		for k := range e {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for k, key := range keys {
			parts[k] = fmt.Sprintf("%s: %s", key, e[key])
		}
		rows[i] = row(fmt.Sprintf("Hint %d", i+1), strings.Join(parts, ", "))
	}
	return StatsResult{Groups: []StatGroup{{Rows: rows}}}, nil
}

// cachesMbean is CacheServiceMBean's object name -- verified against the
// jar's class constants, backing the three invalidate*cache entries whose
// operations (invalidateKeyCache/invalidateRowCache/invalidateCounterCache)
// live on it rather than each having their own per-cache MBean.
const cachesMbean = "org.apache.cassandra.db:type=Caches"

func fetchInvalidateKeyCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, cachesMbean, "invalidateKeyCache", "Key cache")
}

func fetchInvalidateRowCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, cachesMbean, "invalidateRowCache", "Row cache")
}

func fetchInvalidateCounterCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, cachesMbean, "invalidateCounterCache", "Counter cache")
}

// The five auth caches each get their own MBean (unlike the key/row/counter
// caches above, which share one) and all implement AuthCacheMBean's generic
// no-arg invalidate() -- nodetool's own invalidate*cache commands call this
// generic form, not the more targeted invalidateCredentials(role)/
// invalidatePermissions(role, resource)/invalidateRoles(role) overloads each
// cache also exposes for invalidating a single entry.
//
// Two of the five register under a name that doesn't match their Java class
// name -- verified against the jar rather than assumed by analogy with the
// other three (CredentialsCache/PermissionsCache/RolesCache, which do follow
// the obvious "org.apache.cassandra.auth:type=<ClassName>" pattern):
// NetworkPermissionsCache registers as "NetworkAuthCache" (its deprecated/
// legacy name), and JmxPermissionsCache registers as "JMXPermissionsCache"
// (capitalized differently than its class name).
func fetchInvalidateCredentialsCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, "org.apache.cassandra.auth:type=CredentialsCache", "invalidate", "Credentials cache")
}

func fetchInvalidatePermissionsCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, "org.apache.cassandra.auth:type=PermissionsCache", "invalidate", "Permissions cache")
}

func fetchInvalidateRolesCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, "org.apache.cassandra.auth:type=RolesCache", "invalidate", "Roles cache")
}

func fetchInvalidateJmxPermissionsCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, "org.apache.cassandra.auth:type=JMXPermissionsCache", "invalidate", "JMX permissions cache")
}

func fetchInvalidateNetworkPermissionsCache(j *Client, ip string) (StatsResult, error) {
	return execInvalidate(j, ip, "org.apache.cassandra.auth:type=NetworkAuthCache", "invalidate", "Network permissions cache")
}

// execInvalidate is the shared no-arg-exec-then-confirm shape every
// invalidate*cache entry above uses.
func execInvalidate(j *Client, ip, mbean, operation, label string) (StatsResult, error) {
	raws, err := j.bulkExec(ip, []opCall{{Mbean: mbean, Operation: operation}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("%s invalidation failed: %s", label, raws[0].Error)
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row(label, "invalidated")}}}}, nil
}

// fetchRemovalStatus matches `nodetool status` reporting the removal in
// progress, if any -- StorageServiceMBean's zero-arg get-prefixed
// "RemovalStatus" attribute (a string, not a composite type).
func fetchRemovalStatus(j *Client, ip string) (StatsResult, error) {
	value, err := j.readMBeanAttributes(ip, "org.apache.cassandra.db:type=StorageService", "RemovalStatus")
	if err != nil {
		return StatsResult{}, err
	}
	var status string
	if err := json.Unmarshal(value, &status); err != nil {
		return StatsResult{}, err
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("Removal status", status)}}}}, nil
}

// execDangerOp is the shared no-arg-exec-then-confirm shape decommission/
// drain/stopDaemon/forceRemoveCompletion use -- the same pattern as
// execInvalidate, just named separately since these are Danger entries and
// a future reader shouldn't have to check each call site to tell which kind
// of operation this is backing.
func execDangerOp(j *Client, ip, mbean, operation, successLabel string) (StatsResult, error) {
	raws, err := j.bulkExec(ip, []opCall{{Mbean: mbean, Operation: operation}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("%s failed: %s", operation, raws[0].Error)
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row(successLabel, "done")}}}}, nil
}

// fetchDecommission matches `nodetool decommission` (without --force):
// StorageServiceMBean.decommission(boolean force). Streams this node's data
// to the rest of the ring and then removes it -- the operator's own
// controllers/nodectl has a separate, reconcile-driven Decommission for
// scale-down; this is the same underlying JMX call, exposed here for a
// human operating on a cluster directly.
func fetchDecommission(j *Client, ip string) (StatsResult, error) {
	return execDangerOp(j, ip, "org.apache.cassandra.db:type=StorageService", "decommission", "Decommission")
}

// fetchDrain matches `nodetool drain`: stops accepting writes and flushes
// every table. Recovery needs a restart of this node's process (in
// mr-cassop, the StatefulSet's own pod restart).
func fetchDrain(j *Client, ip string) (StatsResult, error) {
	return execDangerOp(j, ip, "org.apache.cassandra.db:type=StorageService", "drain", "Drain")
}

// fetchStopDaemon matches `nodetool stopdaemon`: stops the Cassandra JVM
// outright. In mr-cassop this pod restarts automatically (StatefulSet), but
// the process is down for however long that takes -- more disruptive than
// drain, not more destructive to data.
func fetchStopDaemon(j *Client, ip string) (StatsResult, error) {
	return execDangerOp(j, ip, "org.apache.cassandra.db:type=StorageService", "stopDaemon", "Stop Daemon")
}

// fetchForceRemoveCompletion matches `nodetool forceremovecompletion`:
// force-completes a removeNode operation this node's view of the ring
// considers still pending -- a recovery action for a stuck removal, not
// something to reach for otherwise.
func fetchForceRemoveCompletion(j *Client, ip string) (StatsResult, error) {
	return execDangerOp(j, ip, "org.apache.cassandra.db:type=StorageService", "forceRemoveCompletion", "Force Remove Completion")
}

// fetchAssassinate matches `nodetool assassinate <endpoint>`:
// GossiperMBean.assassinateEndpoint(String) -- permanently declares a peer
// dead in gossip with no re-replication of the data it held. The node the
// JMX call runs against (ip, this catalog's usual per-row target) merely
// issues the command; endpoint (the RequiresArg value) is the *other* node
// being removed from gossip, which is why this needed RequiresArg rather
// than just running against the selected row like decommission/drain do.
func fetchAssassinate(j *Client, ip, endpoint string) (StatsResult, error) {
	raws, err := j.bulkExec(ip, []opCall{{Mbean: "org.apache.cassandra.net:type=Gossiper", Operation: "assassinateEndpoint", Arguments: []any{endpoint}}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("assassinateEndpoint(%s) failed: %s", endpoint, raws[0].Error)
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("Assassinated", endpoint)}}}}, nil
}

// fetchRemoveNode matches `nodetool removenode <host ID>`:
// StorageServiceMBean.removeNode(String hostId) -- tells the ring (via the
// node the JMX call runs against) to remove a *different*, down node by its
// host ID, streaming its data from replicas first. RequiresArg for the same
// reason as fetchAssassinate: the target of the removal isn't the row this
// runs against.
func fetchRemoveNode(j *Client, ip, hostID string) (StatsResult, error) {
	raws, err := j.bulkExec(ip, []opCall{{Mbean: "org.apache.cassandra.db:type=StorageService", Operation: "removeNode", Arguments: []any{hostID}}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("removeNode(%s) failed: %s", hostID, raws[0].Error)
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("Remove started", hostID)}}}}, nil
}

// fetchMove matches `nodetool move <new token>`:
// StorageServiceMBean.move(String newToken) -- relocates this node to a new
// position on the token ring, streaming data accordingly. RequiresArg for
// the token itself, unlike decommission/drain which need no argument beyond
// which node to run against.
func fetchMove(j *Client, ip, newToken string) (StatsResult, error) {
	raws, err := j.bulkExec(ip, []opCall{{Mbean: "org.apache.cassandra.db:type=StorageService", Operation: "move", Arguments: []any{newToken}}})
	if err != nil {
		return StatsResult{}, err
	}
	if len(raws) == 0 || raws[0].Status != http.StatusOK {
		return StatsResult{}, fmt.Errorf("move(%s) failed: %s", newToken, raws[0].Error)
	}
	return StatsResult{Groups: []StatGroup{{Rows: []StatRow{row("Moved to token", newToken)}}}}, nil
}
