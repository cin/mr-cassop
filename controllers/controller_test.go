package controllers

import (
	"net/url"
	"testing"

	"github.com/cin/mr-cassop/api/v1alpha1"
	"github.com/cin/mr-cassop/controllers/config"
	"github.com/cin/mr-cassop/controllers/cql"
	"github.com/cin/mr-cassop/controllers/events"
	"github.com/cin/mr-cassop/controllers/mocks"
	"github.com/cin/mr-cassop/controllers/prober"
	"github.com/cin/mr-cassop/controllers/reaper"
	"github.com/gocql/gocql"
	"github.com/golang/mock/gomock"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
)

var baseScheme = setupScheme()

func createBasicMockedReconciler() *CassandraClusterReconciler {
	return &CassandraClusterReconciler{
		Client: nil,
		Log:    zap.NewNop().Sugar(),
		Scheme: scheme.Scheme,
		Cfg:    config.Config{},
		Events: events.NewEventRecorder(&record.FakeRecorder{}),
	}
}

func createMockedReconciler(t *testing.T) (*CassandraClusterReconciler, *gomock.Controller, mockedClients) {
	mCtrl := gomock.NewController(t)
	proberClientMock := mocks.NewMockProberClient(mCtrl)
	cqlClientMock := mocks.NewMockCqlClient(mCtrl)
	reaperClientMock := mocks.NewMockReaperClient(mCtrl)
	reconciler := &CassandraClusterReconciler{
		Client: nil,
		Log:    zap.NewNop().Sugar(),
		Scheme: scheme.Scheme,
		Cfg:    config.Config{},
		Events: events.NewEventRecorder(&record.FakeRecorder{}),
		ProberClient: func(url *url.URL, user, password string) prober.ProberClient {
			return proberClientMock
		},
		CqlClient: func(clusterConfig *gocql.ClusterConfig) (cql.CqlClient, error) {
			return cqlClientMock, nil
		},
		ReaperClient: func(url *url.URL, clusterName string, defaultRepairThreadCount int32) reaper.ReaperClient {
			return reaperClientMock
		},
	}

	m := mockedClients{prober: proberClientMock, cql: cqlClientMock, reaper: reaperClientMock}
	return reconciler, mCtrl, m
}

type mockedClients struct {
	prober *mocks.MockProberClient
	cql    *mocks.MockCqlClient
	reaper *mocks.MockReaperClient
}

func setupScheme() *runtime.Scheme {
	s := scheme.Scheme
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}
