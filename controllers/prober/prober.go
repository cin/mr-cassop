package prober

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pkg/errors"
)

type ProberClient interface {
	Ready(ctx context.Context) (bool, error)
	GetSeeds(ctx context.Context, host string) ([]string, error)
	UpdateSeeds(ctx context.Context, seeds []string) error
	UpdateDCStatus(ctx context.Context, ready bool) error
	DCsReady(ctx context.Context, host string) (bool, error)
}

type proberClient struct {
	baseUrl *url.URL
	client  *http.Client
}

func NewProberClient(url *url.URL, client *http.Client) ProberClient {
	return &proberClient{url, client}
}

func (p proberClient) url(path string) string {
	return p.baseUrl.String() + path
}

func (p *proberClient) Ready(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url("/ping"), nil)
	if err != nil {
		return false, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return false, errors.Wrap(err, "Request to prober failed")
	}

	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

func (p *proberClient) GetSeeds(ctx context.Context, host string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s/localseeds", host), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "Request to prober failed")
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return []string{}, fmt.Errorf("response status %q (code %v) is not %q",
			http.StatusText(resp.StatusCode), resp.StatusCode, http.StatusText(http.StatusOK))
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []string{}, errors.Wrap(err, "Unable to read response body")
	}

	var seeds []string
	if err := json.Unmarshal(body, &seeds); err != nil {
		return []string{}, errors.Wrap(err, "Error unmarshalling response body")
	}

	return seeds, nil
}

func (p *proberClient) UpdateSeeds(ctx context.Context, seeds []string) error {
	body, _ := json.Marshal(seeds)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.url("/localseeds"), bytes.NewReader(body))
	if err != nil {
		return errors.Wrap(err, "Can't create request")
	}

	if _, err := p.client.Do(req); err != nil {
		return errors.Wrap(err, "PUT Request to prober failed")
	}

	return nil
}

func (p *proberClient) UpdateDCStatus(ctx context.Context, ready bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.url("/readylocaldcs"), bytes.NewReader([]byte(strconv.FormatBool(ready))))
	if err != nil {
		return errors.Wrap(err, "Can't create request")
	}

	if _, err := p.client.Do(req); err != nil {
		return errors.Wrap(err, "PUT Request to prober failed")
	}

	return nil
}

func (p *proberClient) DCsReady(ctx context.Context, host string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s/readylocaldcs", host), nil)
	if err != nil {
		return false, errors.Wrap(err, "Can't create request")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return false, errors.Wrap(err, "PUT Request to prober failed")
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("response status %q (code %v) is not %q",
			http.StatusText(resp.StatusCode), resp.StatusCode, http.StatusText(http.StatusOK))
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	ready, err := strconv.ParseBool(string(bytes.TrimSpace(body)))
	if err != nil {
		return false, errors.Wrapf(err, "Unexpected response from prober. Expect true or false")
	}

	return ready, nil
}
