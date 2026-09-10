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
}

var Catalog = []StatDef{
	{Name: "info", Label: "Info", Category: "Status", Fetch: fetchInfo},
	// Diffable: a node's SchemaVersion disagreeing with the rest of the
	// cluster is a real, actionable problem (a pending/failed schema push),
	// not expected variance -- worth the same red-highlight treatment as
	// fetchSettings gets, and free to add since it's just this one flag.
	{Name: "describecluster", Label: "Describe", Category: "Status", Fetch: fetchDescribeCluster, Diffable: true},
	{Name: "version", Label: "Version", Category: "Status", Fetch: fetchVersion, Diffable: true},
	{Name: "gossipinfo", Label: "Gossip Info", Category: "Status", Fetch: fetchGossipInfo},
	{Name: "clientstats", Label: "Client Stats", Category: "Performance", Fetch: fetchClientStats},
	{Name: "compactionstats", Label: "Compactions", Category: "Compaction", Fetch: fetchCompactionStats},
	{Name: "tpstats", Label: "TP Stats", Category: "Performance", Fetch: fetchTPStats},
	{Name: "netstats", Label: "Net Stats", Category: "Performance", Fetch: fetchNetStats},
	{Name: "gcstats", Label: "GC Stats", Category: "Performance", Fetch: fetchGCStats},
	{Name: "proxyhistograms", Label: "Proxy Histograms", Category: "Performance", Fetch: fetchProxyHistograms},
	{Name: "cachestats", Label: "Cache Stats", Category: "Performance", Fetch: fetchCacheStats},
	{Name: "settings", Label: "Settings (get*)", Category: "Settings", Fetch: fetchSettings, Diffable: true},
	// Diffable for the same reason as describecluster above: gossip/native
	// transport/incremental backups should normally be uniformly enabled (or
	// uniformly disabled during planned maintenance) across a healthy
	// cluster -- one node quietly disagreeing is exactly the kind of thing
	// this pane exists to surface.
	{Name: "statusflags", Label: "Status Flags", Category: "Status", Fetch: fetchStatusFlags, Diffable: true},
	{Name: "compactionhistory", Label: "Compaction History", Category: "Compaction", Fetch: fetchCompactionHistory},
	// The scoping decision the old comment here asked for: a required
	// keyspace.table target (RequiresTable), not pagination or a top-N-by-
	// size default -- see StatDef.RequiresTable's doc comment for why.
	{Name: "cfstats", Label: "Table Stats", Category: "Tables", RequiresTable: true, FetchTable: fetchCfStats},
	{Name: "cfhistograms", Label: "Table Histograms", Category: "Tables", RequiresTable: true, FetchTable: fetchTableHistograms},
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
func RunStat(j *Client, name, ip, table string) (StatsResult, error) {
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
