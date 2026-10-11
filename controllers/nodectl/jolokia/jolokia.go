package jolokia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cin/mr-cassop/api/v1alpha1"

	"github.com/pkg/errors"

	"go.uber.org/zap"
)

// DefaultTimeout bounds a request that doesn't set JMXRequest.Timeout. Reads and quick operations
// finish well within it; a node that silently drops packets would otherwise hang the caller until
// the kernel gives up the TCP connect (minutes).
const DefaultTimeout = 10 * time.Second

type Client struct {
	url     string
	client  *http.Client
	timeout time.Duration
	auth    auth
	log     *zap.SugaredLogger
}

type JMXRequest struct {
	Type       string   `json:"type"`
	Mbean      string   `json:"mbean"`
	Attributes []string `json:"attribute,omitempty"`
	Operation  string   `json:"operation,omitempty"`
	Arguments  []string `json:"arguments,omitempty"` //args are identified based on the order they are passed
	// Timeout caps the whole request. Zero means DefaultTimeout. It isn't sent to Jolokia.
	Timeout time.Duration `json:"-"`
}

type JMXResponse struct {
	Request json.RawMessage `json:"request"`
	Value   json.RawMessage `json:"value"`
	Status  int             `json:"status"`
	Error   string          `json:"error"`
}

type auth struct {
	username string
	password string
}

type request struct {
	JMXRequest
	Target Target `json:"target"`
}

type Target struct {
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func NewClient(jolokiaURL string, username, password string, logr *zap.SugaredLogger) *Client {
	return &Client{
		url: jolokiaURL,
		// requests are bounded per call with a context deadline (see JMXRequest.Timeout), not here
		client:  &http.Client{},
		timeout: DefaultTimeout,
		auth: auth{
			username: username,
			password: password,
		},
		log: logr,
	}
}

func (j *Client) Post(ctx context.Context, jmxReq JMXRequest, ip string) (JMXResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.requestTimeout(jmxReq))
	defer cancel()

	body, err := j.do(ctx, jmxReq, ip)
	if err != nil {
		return JMXResponse{}, err
	}

	return j.parseResponse(body)
}

func (j *Client) requestTimeout(jmxReq JMXRequest) time.Duration {
	if jmxReq.Timeout > 0 {
		return jmxReq.Timeout
	}
	return j.timeout
}

// do sends the request through the Jolokia proxy and returns the raw response body.
func (j *Client) do(ctx context.Context, jmxReq JMXRequest, ip string) ([]byte, error) {
	httpReq, err := j.newHTTPRequest(ctx, jmxReq, ip)
	if err != nil {
		return nil, err
	}
	resp, err := j.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return readBody(resp)
}

func (j *Client) newHTTPRequest(ctx context.Context, jmxReq JMXRequest, ip string) (*http.Request, error) {
	b, err := json.Marshal(j.newRequest(jmxReq, ip))
	if err != nil {
		return nil, err
	}

	return http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(b))
}

func readBody(resp *http.Response) ([]byte, error) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status + ": " + string(responseBody))
	}

	return responseBody, nil
}

func (j *Client) newRequest(jmxReq JMXRequest, ip string) request {
	return request{
		Target: Target{
			URL:      jmxUrl(ip, v1alpha1.JmxPort),
			User:     j.auth.username,
			Password: j.auth.password,
		},
		JMXRequest: jmxReq,
	}
}

func (j *Client) parseResponse(responseBody []byte) (JMXResponse, error) {
	var response JMXResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return JMXResponse{}, fmt.Errorf("cannot unmarshall response. Error %s. Raw body: %s", err.Error(), string(responseBody))
	}

	if len(response.Error) != 0 {
		errMsg := fmt.Sprintf("received error JMX response; error: %s", response.Error)
		if j.log.Desugar().Core().Enabled(zap.DebugLevel) {
			errMsg += "; raw response: " + string(responseBody)
		}
		return JMXResponse{}, errors.New(errMsg)
	}

	return response, nil
}

func jmxUrl(ip string, port int) string {
	return fmt.Sprintf("service:jmx:rmi:///jndi/rmi://%s:%d/jmxrmi", ip, port)
}
