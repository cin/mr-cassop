package prober

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cin/mr-cassop/prober/jolokia"
	"github.com/julienschmidt/httprouter"
)

func (p *Prober) healthCheck(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	broadcastIP := ps.ByName("broadcastip")
	podIP := strings.Split(r.RemoteAddr, ":")[0]

	isReady, states := p.processReadinessProbe(podIP, broadcastIP)
	if isReady {
		w.WriteHeader(http.StatusOK)
	} else {
		p.log.Infow("health check failed", "podIP", podIP, "broadcastIP", broadcastIP, "isReady", isReady)
		w.WriteHeader(http.StatusNotFound)
	}
	response, _ := json.Marshal(states)
	p.write(w, response)
}

func (p *Prober) ping(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	p.write(w, []byte("pong"))
}

func (p *Prober) getRegionReady(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	p.write(w, []byte(strconv.FormatBool(p.state.regionReady)))
}

func (p *Prober) putRegionReady(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var ready bool
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.log.Error(err, "can't ready body")
		w.WriteHeader(http.StatusInternalServerError)
	} else if ready, err = strconv.ParseBool(string(body)); err != nil {
		p.log.Error(err, "can't parse region readiness state")
		w.WriteHeader(http.StatusBadRequest)
	} else {
		p.state.regionReady = ready
	}
}

func (p *Prober) getReaperReady(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	p.write(w, []byte(strconv.FormatBool(p.state.reaperReady)))
}

func (p *Prober) putReaperReady(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var ready bool
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.log.Error(err, "can't ready body")
		w.WriteHeader(http.StatusInternalServerError)
	} else if ready, err = strconv.ParseBool(string(body)); err != nil {
		p.log.Error(err, "can't parse reaper readiness state")
		w.WriteHeader(http.StatusBadRequest)
	} else {
		p.state.reaperReady = ready
	}
}

func (p *Prober) getSeeds(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	response, _ := json.Marshal(p.state.seeds)
	p.write(w, response)
}

func (p *Prober) putSeeds(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var s []string
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	} else if json.Unmarshal(body, &s) != nil {
		w.WriteHeader(http.StatusBadRequest)
	} else {
		p.state.seeds = s
	}
}

func (p *Prober) getDCs(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	response, _ := json.Marshal(p.state.dcs)
	p.write(w, response)
}

func (p *Prober) putDCs(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var dcs []dc
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	} else if json.Unmarshal(body, &dcs) != nil {
		w.WriteHeader(http.StatusBadRequest)
	} else {
		p.state.dcs = dcs
	}
}

// getNodes returns the last polled gossip state of every node discovered by this Prober,
// keyed by node IP. This is a read of the in-memory state built by pollNodeStates and does
// not trigger a new JMX round trip.
func (p *Prober) getNodes(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	response, _ := json.Marshal(p.state.nodes)
	p.write(w, response)
}

// getTools lists every registered stat command (name + display label) from
// the jolokia package's catalog, so the frontend builds its tool buttons
// from what the server actually implements rather than a hand-maintained
// list that can drift out of sync.
func (p *Prober) getTools(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	response, _ := json.Marshal(jolokia.ListStats())
	p.write(w, response)
}

// getStat makes a fresh, on-demand JMX call for the named catalog entry
// (?name=...) against a single node (?ip=...) -- unlike getNodes, this is
// never served from the poll cache. It backs user-triggered "tool"
// invocations, where a real round trip, with real latency, is expected.
// ?table=keyspace.table is only consulted for a RequiresTable entry
// (cfstats/cfhistograms) -- RunStat validates its presence for those, so
// there's nothing extra to check here.
func (p *Prober) getStat(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	name := r.URL.Query().Get("name")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		p.write(w, []byte(`{"error":"missing name query parameter"}`))
		return
	}
	table := r.URL.Query().Get("table")

	podIP, ok := p.resolvePodIP(w, r)
	if !ok {
		return
	}

	result, err := p.jolokia.RunStat(name, podIP, table)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		response, _ := json.Marshal(map[string]string{"error": err.Error()})
		p.write(w, response)
		return
	}

	response, _ := json.Marshal(result)
	p.write(w, response)
}

// getTables lists every "keyspace.table" pair known to a single node
// (?ip=...) -- backs the frontend's table picker for RequiresTable stats
// (cfstats/cfhistograms), sparing the user from typing a keyspace.table
// string blind. Always a fresh on-demand JMX search, like getStat -- schema
// rarely changes, but a stale cached list showing dropped tables (or missing
// ones just created) would be actively misleading for something whose whole
// purpose is picking a real target.
func (p *Prober) getTables(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	podIP, ok := p.resolvePodIP(w, r)
	if !ok {
		return
	}

	tables, err := p.jolokia.ListTables(podIP)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		response, _ := json.Marshal(map[string]string{"error": err.Error()})
		p.write(w, response)
		return
	}

	response, _ := json.Marshal(tables)
	p.write(w, response)
}

// putSettings is getStat's write counterpart, restricted to the "settings"
// pane (see jolokia.settingSources) -- there's no general "write any JMX
// attribute" route, deliberately, since that's a much bigger blast radius
// than this one bounded, reviewed set of scalar knobs. Body is a flat JSON
// object of label -> new value string; only labels present in the body are
// touched. Response is a JSON object of label -> error message for any field
// that failed to apply (an unknown label, a value that doesn't parse, or a
// JMX write the node rejected) -- a label absent from the response
// succeeded, and `{}` means every field in the request succeeded.
func (p *Prober) putSettings(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	podIP, ok := p.resolvePodIP(w, r)
	if !ok {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		p.write(w, []byte(`{"error":"failed to read body"}`))
		return
	}
	var changes map[string]string
	if err := json.Unmarshal(body, &changes); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		p.write(w, []byte(`{"error":"invalid JSON body, expected a flat object of label to new value"}`))
		return
	}
	if len(changes) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		p.write(w, []byte(`{"error":"empty changes"}`))
		return
	}

	fieldErrs, err := p.jolokia.SetSettings(podIP, changes)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		response, _ := json.Marshal(map[string]string{"error": err.Error()})
		p.write(w, response)
		return
	}

	response, _ := json.Marshal(fieldErrs)
	p.write(w, response)
}

// resolvePodIP validates the ip query parameter and resolves it to the pod IP
// Jolokia calls should target, writing an error response and returning false
// if either step fails.
func (p *Prober) resolvePodIP(w http.ResponseWriter, r *http.Request) (string, bool) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		w.WriteHeader(http.StatusBadRequest)
		p.write(w, []byte(`{"error":"missing ip query parameter"}`))
		return "", false
	}

	broadcastIP := "/" + strings.TrimPrefix(ip, "/")
	podIP, known := p.state.podIPs[broadcastIP]
	if !known {
		w.WriteHeader(http.StatusNotFound)
		p.write(w, []byte(`{"error":"unknown node"}`))
		return "", false
	}

	return podIP, true
}

func (p *Prober) write(writer io.Writer, data []byte) {
	written, err := writer.Write(data)
	if err != nil {
		p.log.Error("Error writing data: %s, written %d bytes", err.Error(), written)
	}
}

func (p *Prober) getRegionIPs(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	response, _ := json.Marshal(p.state.regionIPs)
	p.write(w, response)
}

func (p *Prober) putRegionIPs(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var ips []string
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.log.Error(err)
		w.WriteHeader(http.StatusInternalServerError)
	} else if json.Unmarshal(body, &ips) != nil {
		p.log.Error(err)
		w.WriteHeader(http.StatusBadRequest)
	} else {
		p.state.regionIPs = ips
	}
}

func (p *Prober) getReaperIPs(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	response, _ := json.Marshal(p.state.reaperIPs)
	p.write(w, response)
}

func (p *Prober) putReaperIPs(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var ips []string
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.log.Error(err)
		w.WriteHeader(http.StatusInternalServerError)
	} else if json.Unmarshal(body, &ips) != nil {
		p.log.Error(err)
		w.WriteHeader(http.StatusBadRequest)
	} else {
		p.state.reaperIPs = ips
	}
}
