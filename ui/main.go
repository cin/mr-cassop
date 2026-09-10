package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed static
var staticFiles embed.FS

// Environment is one named Prober target this backend can proxy to. The
// browser never sees the Prober URL or credentials -- only this backend
// does; it authenticates to Prober using env.User/env.Password and forwards
// only the proxied API surface (see proxyablePaths) to the browser.
type Environment struct {
	Name      string
	ProberURL string
	User      string
	Password  string
}

var environments []Environment

// loadEnvironments reads the single Prober target this backend talks to
// from its own process env vars -- a POC/dev-scale setup (one environment),
// not a multi-tenant config file, hence no config parsing here.
func loadEnvironments() {
	environments = []Environment{
		{
			Name:      os.Getenv("PROBER_ENV_NAME"),
			ProberURL: os.Getenv("PROBER_URL"),
			User:      os.Getenv("PROBER_USER"),
			Password:  os.Getenv("PROBER_PASSWORD"),
		},
	}
}

func findEnvironment(name string) (Environment, bool) {
	for _, e := range environments {
		if e.Name == name {
			return e, true
		}
	}
	return Environment{}, false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// handleEnvironments lists every configured environment's name -- backs the
// frontend's environment-picker dropdown.
func handleEnvironments(w http.ResponseWriter, r *http.Request) {
	names := make([]string, len(environments))
	for i, e := range environments {
		names[i] = e.Name
	}
	writeJSON(w, http.StatusOK, names)
}

// proxyablePaths allowlists which Prober routes this backend will forward to,
// keyed by the path segment right after /api/environments/{env}/. Each has
// its own timeout: stats makes a live JMX round trip per call (can take real
// time under load, against a slow node, or against tpstats' MBean search +
// bulk read), nodes/tools are cache/static reads.
//
// stats' timeout is kept a bit above Prober's own toolCallTimeout (25s, see
// prober/jolokia/jolokia.go) so a real timeout surfaces Prober's specific
// JMX error instead of this proxy's generic "request failed" once its own,
// shorter budget runs out first.
//
// method restricts each route to the one HTTP method it actually backs --
// settings is PUT-only (a write), everything else GET-only -- rather than
// forwarding whatever method the browser happened to send, which would turn
// this allowlist into "any method on these five paths."
var proxyablePaths = map[string]struct {
	proberPath string
	method     string
	timeout    time.Duration
}{
	"nodes":    {"/nodes", http.MethodGet, 5 * time.Second},
	"tools":    {"/tools", http.MethodGet, 5 * time.Second},
	"stats":    {"/stats", http.MethodGet, 30 * time.Second},
	"settings": {"/settings", http.MethodPut, 30 * time.Second},
	"tables":   {"/tables", http.MethodGet, 10 * time.Second},
}

// handleProxy forwards /api/environments/{env}/{nodes|tools|stats|settings|tables}
// to the matching Prober endpoint (method-checked against proxyablePaths),
// passing query parameters and, for a write, the request body through
// unchanged. The browser never sees the Prober URL or credentials -- only
// this backend does.
func handleProxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/environments/")
	envName, rest, found := strings.Cut(path, "/")
	route, allowed := proxyablePaths[rest]
	if !found || !allowed {
		http.NotFound(w, r)
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": rest + " only accepts " + route.method})
		return
	}

	env, ok := findEnvironment(envName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown environment " + envName})
		return
	}

	target := strings.TrimRight(env.ProberURL, "/") + route.proberPath
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}

	req, err := http.NewRequestWithContext(r.Context(), route.method, target, r.Body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if env.User != "" {
		req.SetBasicAuth(env.User, env.Password)
	}

	client := &http.Client{Timeout: route.timeout}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("prober request failed: %v", err)})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func main() {
	loadEnvironments()

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/environments", handleEnvironments)
	mux.HandleFunc("/api/environments/", handleProxy)

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("nodetool-ui listening on %s (%d environment(s) configured)", addr, len(environments))
	log.Fatal(http.ListenAndServe(addr, mux))
}
