package reaper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/pkg/errors"
)

// authTransport transparently authenticates requests against Reaper's REST API.
// Reaper's docker entrypoint auto-enables JWT access control whenever REAPER_AUTH_USER/
// REAPER_AUTH_PASSWORD are set, which the operator always sets (see reaperEnvironment).
// Every non-anonymous endpoint then requires an "Authorization: Bearer <token>" header,
// obtained via POST /login with those same credentials.
//
// Requests are sent unauthenticated first; a 401 triggers a login (or re-login, if the
// cached token expired) and a single retry with the fresh token attached.
type authTransport struct {
	base     http.RoundTripper
	loginURL string
	username string
	password string

	mu    sync.Mutex
	token string
}

func newAuthTransport(base http.RoundTripper, baseURL *url.URL, username, password string) *authTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &authTransport{
		base:     base,
		loginURL: baseURL.String() + "/login",
		username: username,
		password: password,
	}
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if token := t.cachedToken(); token != "" {
		req = withBearerToken(req, token)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()

	token, err := t.login(req.Context())
	if err != nil {
		return nil, errors.Wrap(err, "reaper authentication failed")
	}

	return t.base.RoundTrip(withBearerToken(req, token))
}

func (t *authTransport) cachedToken() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.token
}

func (t *authTransport) login(ctx context.Context) (string, error) {
	form := url.Values{"username": {t.username}, "password": {t.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", &requestFailedWithStatus{code: resp.StatusCode, message: string(b)}
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &loginResp); err != nil {
		return "", errors.Wrap(err, "failed to parse reaper login response")
	}
	if loginResp.Token == "" {
		return "", errors.New("reaper login response did not contain a token")
	}

	t.mu.Lock()
	t.token = loginResp.Token
	t.mu.Unlock()

	return loginResp.Token, nil
}

func withBearerToken(req *http.Request, token string) *http.Request {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token)
	return clone
}
