// Code generated by MockGen. DO NOT EDIT.
// Source: internal/fleetdb/interface.go

// Package fleetdb is a generated GoMock package.
package fleetdb

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	uuid "github.com/google/uuid"
)

// MockFleetDB is a mock of FleetDB interface.
type MockFleetDB struct {
	ctrl     *gomock.Controller
	recorder *MockFleetDBMockRecorder
}

// MockFleetDBMockRecorder is the mock recorder for MockFleetDB.
type MockFleetDBMockRecorder struct {
	mock *MockFleetDB
}

// NewMockFleetDB creates a new mock instance.
func NewMockFleetDB(ctrl *gomock.Controller) *MockFleetDB {
	mock := &MockFleetDB{ctrl: ctrl}
	mock.recorder = &MockFleetDBMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockFleetDB) EXPECT() *MockFleetDBMockRecorder {
	return m.recorder
}

// AddServer mocks base method.
func (m *MockFleetDB) AddServer(ctx context.Context, serverID uuid.UUID, facilityCode, bmcAddr, bmcUser, bmcPass string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AddServer", ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass)
	ret0, _ := ret[0].(error)
	return ret0
}

// AddServer indicates an expected call of AddServer.
func (mr *MockFleetDBMockRecorder) AddServer(ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AddServer", reflect.TypeOf((*MockFleetDB)(nil).AddServer), ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass)
}
