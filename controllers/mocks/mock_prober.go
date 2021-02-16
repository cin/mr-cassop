// Code generated by MockGen. DO NOT EDIT.
// Source: ./controllers/prober/prober.go

// Package mocks is a generated GoMock package.
package mocks

import (
	context "context"
	gomock "github.com/golang/mock/gomock"
	reflect "reflect"
)

// MockProberClient is a mock of ProberClient interface
type MockProberClient struct {
	ctrl     *gomock.Controller
	recorder *MockProberClientMockRecorder
}

// MockProberClientMockRecorder is the mock recorder for MockProberClient
type MockProberClientMockRecorder struct {
	mock *MockProberClient
}

// NewMockProberClient creates a new mock instance
func NewMockProberClient(ctrl *gomock.Controller) *MockProberClient {
	mock := &MockProberClient{ctrl: ctrl}
	mock.recorder = &MockProberClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockProberClient) EXPECT() *MockProberClientMockRecorder {
	return m.recorder
}

// Ready mocks base method
func (m *MockProberClient) Ready(ctx context.Context) (bool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Ready", ctx)
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Ready indicates an expected call of Ready
func (mr *MockProberClientMockRecorder) Ready(ctx interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Ready", reflect.TypeOf((*MockProberClient)(nil).Ready), ctx)
}
