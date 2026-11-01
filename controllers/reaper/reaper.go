package reaper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pkg/errors"

	dbv1alpha1 "github.com/cin/mr-cassop/api/v1alpha1"
)

const (
	RepairStateRunning = "RUNNING"
	RepairStatePaused  = "PAUSED"

	RepairScheduleStateActive = "ACTIVE"
	RepairScheduleStatePaused = "PAUSED"

	OwnerCassandraOperator = "mr-cassop"
)

type reaperClient struct {
	baseUrl           *url.URL
	client            *http.Client
	clusterName       string
	repairThreadCount int32
}

type ReaperClient interface {
	IsRunning(ctx context.Context) (bool, error)
	ClusterExists(ctx context.Context) (bool, error)
	Clusters(ctx context.Context) ([]string, error)
	DeleteCluster(ctx context.Context) error
	AddCluster(ctx context.Context, seed string) error
	CreateRepairSchedule(ctx context.Context, repair dbv1alpha1.RepairSchedule) error
	RepairSchedules(ctx context.Context) ([]RepairSchedule, error)
	DeleteRepairSchedule(ctx context.Context, repairScheduleID string) error
	SetRepairScheduleState(ctx context.Context, repairScheduleID string, active bool) error
	RunRepair(ctx context.Context, keyspace, cause string) error
	RepairRuns(ctx context.Context) ([]RepairRun, error)
	PauseRepairRun(ctx context.Context, repairRunID string) error
	ResumeRepairRun(ctx context.Context, repairRunID string) error
}

var (
	ClusterNotFound = errors.New("cassandra cluster not found")
)

type requestFailedWithStatus struct {
	code    int
	message string
}

func (e *requestFailedWithStatus) Error() string {
	return fmt.Sprintf("Request failed with status code %d. Response body: %s", e.code, e.message)
}

func NewReaperClient(url *url.URL, clusterName, username, password string, client *http.Client, defaultRepairThreadCount int32) ReaperClient {
	authedClient := *client
	authedClient.Transport = newAuthTransport(client.Transport, url, username, password)
	return &reaperClient{
		baseUrl:           url,
		client:            &authedClient,
		clusterName:       clusterName,
		repairThreadCount: defaultRepairThreadCount,
	}
}

func (r *reaperClient) url(path string) string {
	return r.baseUrl.String() + path
}

func (r *reaperClient) IsRunning(ctx context.Context) (bool, error) {
	route := r.url("/ping")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, route, nil)
	if err != nil {
		return false, err
	}
	req = req.WithContext(ctx)
	resp, err := r.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent, nil
}

func (r *reaperClient) ClusterExists(ctx context.Context) (bool, error) {
	route := r.url("/cluster/" + r.clusterName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, route, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(ctx)
	resp, err := r.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, &requestFailedWithStatus{code: resp.StatusCode, message: string(b)}
	}
	return true, nil
}

func (r *reaperClient) AddCluster(ctx context.Context, seed string) error {
	route := r.url("/cluster/" + r.clusterName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, route, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("seedHost", seed)
	req.URL.RawQuery = q.Encode()
	req = req.WithContext(ctx)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	b, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound {
			return ClusterNotFound
		}
		return &requestFailedWithStatus{code: resp.StatusCode, message: string(b)}
	}
	return nil
}

func (r *reaperClient) Clusters(ctx context.Context) ([]string, error) {
	route := r.url("/cluster")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, route, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(ctx)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	b, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, &requestFailedWithStatus{code: resp.StatusCode, message: string(b)}
	}

	var clusters []string
	err = json.Unmarshal(b, &clusters)
	if err != nil {
		return nil, err
	}

	return clusters, nil
}

func (r *reaperClient) DeleteCluster(ctx context.Context) error {
	if err := r.deleteRepairRuns(ctx); err != nil {
		return err
	}

	if err := r.deleteOperatorRepairSchedules(ctx); err != nil {
		return err
	}

	route := r.url("/cluster/" + r.clusterName)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, route, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	b, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &requestFailedWithStatus{code: resp.StatusCode, message: string(b)}
	}

	return nil
}

// deleteRepairRuns ensures no repair runs exist, each deleted with its own owner.
func (r *reaperClient) deleteRepairRuns(ctx context.Context) error {
	repairRuns, err := r.getRepairRuns(ctx, "")
	if err != nil {
		return err
	}

	for _, repairRun := range repairRuns {
		err = r.deleteRepairRun(ctx, repairRun)
		if err != nil {
			return errors.Wrapf(err, "can't delete repair run %s for keyspace %s", repairRun.ID, repairRun.KeyspaceName)
		}
	}

	return nil
}

// deleteOperatorRepairSchedules pauses and deletes the schedules the operator owns. Schedules owned by
// anyone else are left untouched, but Reaper refuses to remove a cluster that still has schedules, so
// they are reported in the returned error instead of failing later with a bare 409.
func (r *reaperClient) deleteOperatorRepairSchedules(ctx context.Context) error {
	repairSchedules, err := r.RepairSchedules(ctx)
	if err != nil {
		return err
	}

	foreign := make([]string, 0)
	for _, repairSchedule := range repairSchedules {
		if repairSchedule.Owner != OwnerCassandraOperator {
			foreign = append(foreign, fmt.Sprintf("%s (owner %q)", repairSchedule.ID, repairSchedule.Owner))
			continue
		}

		// Reaper refuses to delete an ACTIVE schedule (409), so pause it first.
		if err = r.SetRepairScheduleState(ctx, repairSchedule.ID, false); err != nil {
			return errors.Wrapf(err, "can't pause repair schedule %s for keyspace %s", repairSchedule.ID, repairSchedule.KeyspaceName)
		}

		if err = r.DeleteRepairSchedule(ctx, repairSchedule.ID); err != nil {
			return errors.Wrapf(err, "can't delete repair schedule %s for keyspace %s", repairSchedule.ID, repairSchedule.KeyspaceName)
		}
	}

	if len(foreign) > 0 {
		return errors.Errorf("reaper can't remove cluster %s while it has repair schedules not owned by the operator, delete them in reaper first: %s",
			r.clusterName, strings.Join(foreign, ", "))
	}

	return nil
}

func (r *reaperClient) deleteRepairRun(ctx context.Context, repairRun RepairRun) error {
	if repairRun.State == RepairStateRunning {
		if err := r.setRepairState(ctx, repairRun.ID, RepairStatePaused); err != nil {
			return errors.Wrapf(err, "can't pause repair run %s for keyspace %s", repairRun.ID, repairRun.KeyspaceName)
		}
	}

	route := r.url("/repair_run/" + repairRun.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, route, nil)
	if err != nil {
		return err
	}
	q := req.URL.Query()
	q.Add("owner", repairRun.Owner)
	req.URL.RawQuery = q.Encode()
	req = req.WithContext(ctx)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	b, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &requestFailedWithStatus{code: resp.StatusCode, message: string(b)}
	}

	return nil
}
