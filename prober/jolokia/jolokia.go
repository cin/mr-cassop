package jolokia

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	jsoniter "github.com/json-iterator/go"
	"github.com/json-iterator/go/extra"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	userRegExp = regexp.MustCompile(`("password":)".*?"`)
	passRegExp = regexp.MustCompile(`("user":)".*?"`)
)

type Jolokia interface {
	CassandraNodeState(ip string) (CassandraResponse, error)
	// RunStat executes one named entry from the Catalog (see stats.go) --
	// the single generic entry point for every "get" tool, in place of a
	// bespoke method per command. table is only consulted for a
	// RequiresTable entry (cfstats/cfhistograms) -- pass "" otherwise.
	RunStat(name, ip, table string) (StatsResult, error)
	// ListTables returns every "keyspace.table" pair known to ip -- backs the
	// frontend's table picker for RequiresTable stats (cfstats/cfhistograms),
	// sparing the user from typing a keyspace.table string blind.
	ListTables(ip string) ([]string, error)
	// SetSettings is RunStat("settings", ip)'s write counterpart: applies new
	// values to a subset of settingSources' rows (see stats.go), keyed by
	// Label. Returns a map of only the labels that failed to apply (an empty
	// map means every field in changes succeeded).
	SetSettings(ip string, changes map[string]string) (map[string]string, error)
	SetAuth(username, password string)
}

// toolCallTimeout bounds on-demand "tool" JMX calls (search, attribute
// reads/bulk-reads backing the stats Catalog) -- deliberately separate from
// the poll-loop's own HTTP client, whose timeout is tied to JmxPollingInterval
// (10s by default) and is far too tight for e.g. tpstats' search-then-bulk-
// read-~60-MBeans round trip, which legitimately takes longer than a single
// cached-loop attribute read. Kept a bit under the UI backend's own proxy
// timeout for /stats (see ui/main.go's proxyablePaths) so a real timeout here
// surfaces its actual JMX error instead of the UI's generic "request failed"
// once its own budget runs out first.
const toolCallTimeout = 25 * time.Second

type Client struct {
	url          string
	*http.Client // used by the poll loop (CassandraNodeState) -- see toolClient for on-demand calls
	toolClient   *http.Client
	auth         auth
	jmxPort      int
	log          *zap.SugaredLogger
}

type Response struct {
	Request, Value interface{}
	Status         int
	Error          string
}

type auth struct {
	username string
	password string
}

type jmxRequest struct{ Type, Mbean, Attribute string }

type CassandraResponse struct {
	Response
	Value CassandraNodeState
}

func NewClient(jolokiaPort, jmxPort int, timeout time.Duration, logr *zap.SugaredLogger, username, password string) Jolokia {
	return &Client{
		url:        fmt.Sprintf("http://localhost:%d/jolokia", jolokiaPort),
		Client:     &http.Client{Timeout: timeout},
		toolClient: &http.Client{Timeout: toolCallTimeout},
		auth: auth{
			username: username,
			password: password,
		},
		jmxPort: jmxPort,
		log:     logr,
	}
}

func jmxUrl(ip string, port int) string {
	return fmt.Sprintf("service:jmx:rmi:///jndi/rmi:/%s:%d/jmxrmi", ip, port)
}

// rawResponse mirrors Response but keeps Value undecoded, since a bulk request
// returns one response per MBean read and each needs its own unmarshal target.
type rawResponse struct {
	Value  json.RawMessage
	Status int
	Error  string
}

// postBulk POSTs a Jolokia bulk request body via the given HTTP client and
// returns its per-request responses with Value left undecoded -- shared by
// every call site below, each of which unmarshals Value into its own shape.
// Callers pass j.Client (poll-loop timeout) or j.toolClient (on-demand tool
// timeout) depending on which kind of call this is.
func (j *Client) postBulk(client *http.Client, body []byte) ([]rawResponse, error) {
	resp, err := client.Post(j.url, runtime.ContentTypeJSON, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	responseBodyModified := userRegExp.ReplaceAllString(string(responseBody), `$1"***"`)
	responseBodyModified = passRegExp.ReplaceAllString(responseBodyModified, `$1"***"`)

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status + ": " + responseBodyModified)
	}

	var raws []rawResponse
	if err := json.Unmarshal(responseBody, &raws); err != nil {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("empty response. Raw body: %s", responseBodyModified)
	}
	return raws, nil
}

// postBulkFirst is postBulk for the common case of a single-request body,
// additionally checking that one response's own status.
func (j *Client) postBulkFirst(client *http.Client, body []byte) (rawResponse, error) {
	raws, err := j.postBulk(client, body)
	if err != nil {
		return rawResponse{}, err
	}
	if raws[0].Status != http.StatusOK {
		return rawResponse{}, errors.New(raws[0].Error)
	}
	return raws[0], nil
}

func (j *Client) CassandraNodeState(ip string) (CassandraResponse, error) {
	raws, err := j.postBulk(j.Client, j.cassandraNodeStateRequest(ip))
	if err != nil {
		return CassandraResponse{}, err
	}

	gossip := raws[0]
	result := CassandraResponse{Response: Response{Status: gossip.Status, Error: gossip.Error}}
	if len(gossip.Error) != 0 {
		j.log.Debugf("gossip read failed: %s", gossip.Error)
	}
	if gossip.Status == http.StatusOK {
		if err := json.Unmarshal(gossip.Value, &result.Value); err != nil {
			return CassandraResponse{}, err
		}
	}

	// Token data is best-effort: a failed or missing read here shouldn't fail
	// gossip/status discovery, which is the core behavior callers depend on.
	if len(raws) > 1 {
		tokens := raws[1]
		if tokens.Status == http.StatusOK {
			var tokenMap map[string]string
			if err := json.Unmarshal(tokens.Value, &tokenMap); err != nil {
				j.log.Debugf("failed to parse TokenToEndpointMap: %s", err.Error())
			} else {
				result.Value.TokenToEndpointMap = tokenMap
			}
		} else if len(tokens.Error) != 0 {
			j.log.Debugf("TokenToEndpointMap read failed: %s", tokens.Error)
		}
	}

	return result, nil
}

func (j *Client) cassandraNodeStateRequest(ip string) []byte {
	type Target struct{ Url, User, Password string }
	type Request struct {
		jmxRequest
		Target Target
	}
	target := Target{jmxUrl(ip, j.jmxPort), j.auth.username, j.auth.password}
	req := []Request{
		{
			jmxRequest: jmxRequest{
				Type:      "read",
				Mbean:     "org.apache.cassandra.net:type=FailureDetector",
				Attribute: "SimpleStates,AllEndpointStates",
			},
			Target: target,
		},
		{
			jmxRequest: jmxRequest{
				Type:      "read",
				Mbean:     "org.apache.cassandra.db:type=StorageService",
				Attribute: "TokenToEndpointMap",
			},
			Target: target,
		},
	}

	extra.SetNamingStrategy(extra.LowerCaseWithUnderscores)
	body, _ := jsoniter.Marshal(req)
	return body
}

// readMBeanAttributes issues a single-MBean, single-request JMX read for the
// given comma-separated attribute list and returns the raw, undecoded value
// payload. Shared by every "tool" call (NodeInfo, DescribeCluster,
// CompactionStats, ...) that reads one MBean's attributes and unmarshals
// them into its own struct -- these are always fresh, on-demand calls, never
// served from the poll cache, since they back user-triggered actions where a
// real round trip is the expected behavior.
func (j *Client) readMBeanAttributes(ip, mbean, attribute string) (json.RawMessage, error) {
	type Target struct{ Url, User, Password string }
	type Request struct {
		jmxRequest
		Target Target
	}
	req := []Request{
		{
			jmxRequest: jmxRequest{Type: "read", Mbean: mbean, Attribute: attribute},
			Target:     Target{jmxUrl(ip, j.jmxPort), j.auth.username, j.auth.password},
		},
	}
	extra.SetNamingStrategy(extra.LowerCaseWithUnderscores)
	body, _ := jsoniter.Marshal(req)

	raw, err := j.postBulkFirst(j.toolClient, body)
	if err != nil {
		return nil, err
	}
	return raw.Value, nil
}

// RunStat looks up name in the Catalog (stats.go) and runs its fetch against
// ip. This is the one entry point every "get" tool goes through.
func (j *Client) RunStat(name, ip, table string) (StatsResult, error) {
	return RunStat(j, name, ip, table)
}

// ListTables returns every "keyspace.table" pair known to ip. See
// jolokia.ListTables (stats.go) for how.
func (j *Client) ListTables(ip string) ([]string, error) {
	return ListTables(j, ip)
}

// SetSettings looks up each changed label in settingSources (stats.go) and
// writes it. This is RunStat's write counterpart -- the one entry point the
// settings pane's "Set" action goes through.
func (j *Client) SetSettings(ip string, changes map[string]string) (map[string]string, error) {
	return SetSettings(j, ip, changes)
}

// attrRead is one JMX read target: a single MBean plus a single attribute
// name on it.
type attrRead struct{ Mbean, Attribute string }

// attrWrite pairs an attrRead with the new value to write there. Value is
// `any` rather than string because a JMX write must carry the value as the
// attribute's actual declared type (number/boolean/string) -- callers build
// this from parseSettingValue, never straight from user input text.
type attrWrite struct {
	attrRead
	Value any
}

// bulkReadAttributes issues one JMX read per attrRead, batched into a single
// HTTP round trip, and returns each one's raw response independently --
// unlike readMBeanAttributes' single combined multi-attribute read, a bad or
// renamed attribute on one entry surfaces only in that entry's own response
// (raws[i].Status/Error) rather than failing the whole request (see
// fetchNetStats' comment on why a combined read of unrelated attributes can
// fail outright). Used by fetchSettings, whose rows come from several
// different MBeans/attributes that should degrade independently.
func (j *Client) bulkReadAttributes(ip string, reads []attrRead) ([]rawResponse, error) {
	type Target struct{ Url, User, Password string }
	type Request struct {
		jmxRequest
		Target Target
	}
	target := Target{jmxUrl(ip, j.jmxPort), j.auth.username, j.auth.password}
	reqs := make([]Request, len(reads))
	for i, r := range reads {
		reqs[i] = Request{jmxRequest: jmxRequest{Type: "read", Mbean: r.Mbean, Attribute: r.Attribute}, Target: target}
	}
	extra.SetNamingStrategy(extra.LowerCaseWithUnderscores)
	body, _ := jsoniter.Marshal(reqs)
	return j.postBulk(j.toolClient, body)
}

// bulkWriteAttributes is bulkReadAttributes' write counterpart: one JMX
// write per attrWrite, batched into a single HTTP round trip, each degrading
// independently (a rejected value or a read-only attribute on one entry
// surfaces only in that entry's own response, same as bulkReadAttributes).
// Used only by SetSettings -- nothing else in this package writes to JMX.
func (j *Client) bulkWriteAttributes(ip string, writes []attrWrite) ([]rawResponse, error) {
	type Target struct{ Url, User, Password string }
	type Request struct {
		Type      string
		Mbean     string
		Attribute string
		Value     any
		Target    Target
	}
	target := Target{jmxUrl(ip, j.jmxPort), j.auth.username, j.auth.password}
	reqs := make([]Request, len(writes))
	for i, w := range writes {
		reqs[i] = Request{Type: "write", Mbean: w.Mbean, Attribute: w.Attribute, Value: w.Value, Target: target}
	}
	extra.SetNamingStrategy(extra.LowerCaseWithUnderscores)
	body, _ := jsoniter.Marshal(reqs)
	return j.postBulk(j.toolClient, body)
}

// searchMBeans resolves a JMX ObjectName pattern (e.g. "domain:type=*") to
// the matching canonical MBean names on the given node, via Jolokia's search
// operation. Used by fetchers whose MBean set isn't fixed (e.g. tpstats'
// thread pools).
func (j *Client) searchMBeans(ip, pattern string) ([]string, error) {
	type Target struct{ Url, User, Password string }
	type Request struct {
		Type   string
		Mbean  string
		Target Target
	}
	req := []Request{
		{
			Type:   "search",
			Mbean:  pattern,
			Target: Target{jmxUrl(ip, j.jmxPort), j.auth.username, j.auth.password},
		},
	}
	extra.SetNamingStrategy(extra.LowerCaseWithUnderscores)
	body, _ := jsoniter.Marshal(req)

	raw, err := j.postBulkFirst(j.toolClient, body)
	if err != nil {
		return nil, err
	}

	var rawNames []string
	if err := json.Unmarshal(raw.Value, &rawNames); err != nil {
		return nil, err
	}

	// A target-proxied search can also return matches from Jolokia's own
	// local JVM (the sidecar Jolokia runs in, not the Cassandra node being
	// queried) whenever the pattern's domain happens to exist on both --
	// verified empirically with java.lang:type=GarbageCollector, where an
	// unfiltered search returned the sidecar's own G1 collectors alongside
	// the real target's ParNew/CMS ones. Real target-side matches are always
	// prefixed "proxy@"; anything without that prefix is local and must be
	// discarded rather than mistaken for the node being queried.
	names := make([]string, 0, len(rawNames))
	for _, n := range rawNames {
		if stripped, ok := strings.CutPrefix(n, "proxy@"); ok {
			names = append(names, stripped)
		}
	}
	return names, nil
}

// bulkReadGauges reads the "Value" attribute of every given MBean in one
// request -- the shape a Codahale/Dropwizard Gauge exposes over JMX, which is
// what Cassandra 4.x's per-metric MBeans mostly are (one MBean per (path,
// scope, metric name) triple, not one MBean per pool with multiple
// attributes). A per-MBean failure yields "n/a" for that entry rather than
// failing the whole batch, since which metrics exist can vary. Not every
// per-metric MBean is a Gauge, though -- a Meter (e.g. cachestats' Requests/
// Hits) exposes its count as "Count" instead and needs bulkReadMeterCounts.
func (j *Client) bulkReadGauges(ip string, mbeans []string) (map[string]string, error) {
	return j.bulkReadMetricAttribute(ip, mbeans, "Value")
}

// bulkReadMeterCounts is bulkReadGauges for Meter MBeans: same batching and
// per-MBean fault tolerance, but reads "Count" -- verified against a live
// cluster after cachestats' Requests/Hits rows first came back "n/a" reading
// "Value" like every other cache metric here does; those two are Meters
// (com.codahale.metrics.Meter), not Gauges like Capacity/Entries/Size/HitRate.
func (j *Client) bulkReadMeterCounts(ip string, mbeans []string) (map[string]string, error) {
	return j.bulkReadMetricAttribute(ip, mbeans, "Count")
}

func (j *Client) bulkReadMetricAttribute(ip string, mbeans []string, attribute string) (map[string]string, error) {
	type Target struct{ Url, User, Password string }
	type Request struct {
		Type      string
		Mbean     string
		Attribute string
		Target    Target
	}
	target := Target{jmxUrl(ip, j.jmxPort), j.auth.username, j.auth.password}
	reqs := make([]Request, len(mbeans))
	for i, mbean := range mbeans {
		reqs[i] = Request{Type: "read", Mbean: mbean, Attribute: attribute, Target: target}
	}
	extra.SetNamingStrategy(extra.LowerCaseWithUnderscores)
	body, _ := jsoniter.Marshal(reqs)

	raws, err := j.postBulk(j.toolClient, body)
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(mbeans))
	for i, raw := range raws {
		if i >= len(mbeans) {
			break
		}
		if raw.Status != http.StatusOK {
			values[mbeans[i]] = "n/a"
			continue
		}
		var v any
		if err := json.Unmarshal(raw.Value, &v); err != nil {
			values[mbeans[i]] = "n/a"
			continue
		}
		values[mbeans[i]] = fmt.Sprint(v)
	}
	return values, nil
}

func (j *Client) SetAuth(username, password string) {
	j.auth.username = username
	j.auth.password = password
}
