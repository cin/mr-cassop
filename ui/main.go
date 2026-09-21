package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed static
var staticFiles embed.FS

// Environment is one named Prober target this backend can proxy to. The
// browser never sees the Prober URL or credentials -- only this backend
// does; it authenticates to Prober using env.User/env.Password and forwards
// only the proxied API surface (see proxyablePaths) to the browser.
//
// When CredentialsDir is set (the operator-managed Deployment mounts prober's
// auth secret there), credentials are re-read from it on every request, so an
// admin password rotation reaches the UI without a restart. User/Password are
// the fallback for running the UI by hand with env vars.
type Environment struct {
	Name           string
	ProberURL      string
	User           string
	Password       string
	CredentialsDir string
}

// Secret keys mounted into CredentialsDir, matching the operator's admin secret.
const (
	credentialsUserFile     = "admin-role"
	credentialsPasswordFile = "admin-password"
)

// credentials returns the user/password to authenticate to Prober with.
func (e Environment) credentials() (user, password string, err error) {
	if e.CredentialsDir == "" {
		return e.User, e.Password, nil
	}
	if user, err = readCredentialFile(e.CredentialsDir, credentialsUserFile); err != nil {
		return "", "", err
	}
	if password, err = readCredentialFile(e.CredentialsDir, credentialsPasswordFile); err != nil {
		return "", "", err
	}
	return user, password, nil
}

func readCredentialFile(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("reading prober credentials: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

var environments []Environment

// Version is set via -ldflags at build time (see ui/Dockerfile), matching the operator and prober.
var Version = "undefined"

// loadEnvironments reads the single Prober target this backend talks to
// from its own process env vars -- a POC/dev-scale setup (one environment),
// not a multi-tenant config file, hence no config parsing here.
func loadEnvironments() {
	environments = []Environment{
		{
			Name:           os.Getenv("PROBER_ENV_NAME"),
			ProberURL:      os.Getenv("PROBER_URL"),
			User:           os.Getenv("PROBER_USER"),
			Password:       os.Getenv("PROBER_PASSWORD"),
			CredentialsDir: os.Getenv("PROBER_CREDENTIALS_DIR"),
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
	user, password, err := env.credentials()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if user != "" {
		req.SetBasicAuth(user, password)
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

// Server timeouts. writeTimeout has to outlast the slowest proxied route
// (stats/settings, 30s in proxyablePaths) or those responses get cut off.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 45 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func newMux() (*http.ServeMux, error) {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/environments", handleEnvironments)
	mux.HandleFunc("/api/environments/", handleProxy)
	return mux, nil
}

// serve runs srv until ctx is cancelled (SIGTERM/SIGINT), then drains in-flight
// requests for up to shutdownTimeout.
func serve(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func main() {
	loadEnvironments()

	mux, err := newMux()
	if err != nil {
		log.Fatal(err)
	}

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	log.Printf("nodetool-ui %s listening on %s (%d environment(s) configured)", Version, addr, len(environments))
	if err := serve(ctx, srv); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
