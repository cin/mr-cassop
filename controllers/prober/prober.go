package prober

import (
	"context"
	"github.com/pkg/errors"
	"io/ioutil"
	"net/http"
)

type ProberClient struct {
	host string
}

func NewProberClient(host string) *ProberClient {
	return &ProberClient{host: host}
}

func (p ProberClient) ReadyAllDCs(ctx context.Context) (bool, error) {
	proberReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+p.host+"/readyalldcs", nil)
	if err != nil {
		return false, errors.Wrap(err, "Can't create request")
	}

	resp, err := http.DefaultClient.Do(proberReq)
	if err != nil {
		return false, errors.Wrap(err, "Request to prober failed")
	}

	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return false, errors.Wrap(err, "Can't read body")
	}

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	return true, nil
}
