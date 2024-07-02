// Code generated by mockery v2.42.1. DO NOT EDIT.

package fleetdb

import (
	context "context"

	model "github.com/metal-toolbox/conditionorc/internal/model"
	mock "github.com/stretchr/testify/mock"

	uuid "github.com/google/uuid"
)

// MockFleetDB is an autogenerated mock type for the FleetDB type
type MockFleetDB struct {
	mock.Mock
}

type MockFleetDB_Expecter struct {
	mock *mock.Mock
}

func (_m *MockFleetDB) EXPECT() *MockFleetDB_Expecter {
	return &MockFleetDB_Expecter{mock: &_m.Mock}
}

// AddServer provides a mock function with given fields: ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass
func (_m *MockFleetDB) AddServer(ctx context.Context, serverID uuid.UUID, facilityCode string, bmcAddr string, bmcUser string, bmcPass string) (func() error, error) {
	ret := _m.Called(ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass)

	if len(ret) == 0 {
		panic("no return value specified for AddServer")
	}

	var r0 func() error
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uuid.UUID, string, string, string, string) (func() error, error)); ok {
		return rf(ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uuid.UUID, string, string, string, string) func() error); ok {
		r0 = rf(ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(func() error)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, uuid.UUID, string, string, string, string) error); ok {
		r1 = rf(ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockFleetDB_AddServer_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'AddServer'
type MockFleetDB_AddServer_Call struct {
	*mock.Call
}

// AddServer is a helper method to define mock.On call
//   - ctx context.Context
//   - serverID uuid.UUID
//   - facilityCode string
//   - bmcAddr string
//   - bmcUser string
//   - bmcPass string
func (_e *MockFleetDB_Expecter) AddServer(ctx interface{}, serverID interface{}, facilityCode interface{}, bmcAddr interface{}, bmcUser interface{}, bmcPass interface{}) *MockFleetDB_AddServer_Call {
	return &MockFleetDB_AddServer_Call{Call: _e.mock.On("AddServer", ctx, serverID, facilityCode, bmcAddr, bmcUser, bmcPass)}
}

func (_c *MockFleetDB_AddServer_Call) Run(run func(ctx context.Context, serverID uuid.UUID, facilityCode string, bmcAddr string, bmcUser string, bmcPass string)) *MockFleetDB_AddServer_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uuid.UUID), args[2].(string), args[3].(string), args[4].(string), args[5].(string))
	})
	return _c
}

func (_c *MockFleetDB_AddServer_Call) Return(_a0 func() error, _a1 error) *MockFleetDB_AddServer_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockFleetDB_AddServer_Call) RunAndReturn(run func(context.Context, uuid.UUID, string, string, string, string) (func() error, error)) *MockFleetDB_AddServer_Call {
	_c.Call.Return(run)
	return _c
}

// DeleteServer provides a mock function with given fields: ctx, serverID
func (_m *MockFleetDB) DeleteServer(ctx context.Context, serverID uuid.UUID) error {
	ret := _m.Called(ctx, serverID)

	if len(ret) == 0 {
		panic("no return value specified for DeleteServer")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, uuid.UUID) error); ok {
		r0 = rf(ctx, serverID)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MockFleetDB_DeleteServer_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'DeleteServer'
type MockFleetDB_DeleteServer_Call struct {
	*mock.Call
}

// DeleteServer is a helper method to define mock.On call
//   - ctx context.Context
//   - serverID uuid.UUID
func (_e *MockFleetDB_Expecter) DeleteServer(ctx interface{}, serverID interface{}) *MockFleetDB_DeleteServer_Call {
	return &MockFleetDB_DeleteServer_Call{Call: _e.mock.On("DeleteServer", ctx, serverID)}
}

func (_c *MockFleetDB_DeleteServer_Call) Run(run func(ctx context.Context, serverID uuid.UUID)) *MockFleetDB_DeleteServer_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uuid.UUID))
	})
	return _c
}

func (_c *MockFleetDB_DeleteServer_Call) Return(_a0 error) *MockFleetDB_DeleteServer_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockFleetDB_DeleteServer_Call) RunAndReturn(run func(context.Context, uuid.UUID) error) *MockFleetDB_DeleteServer_Call {
	_c.Call.Return(run)
	return _c
}

// GetServer provides a mock function with given fields: ctx, serverID
func (_m *MockFleetDB) GetServer(ctx context.Context, serverID uuid.UUID) (*model.Server, error) {
	ret := _m.Called(ctx, serverID)

	if len(ret) == 0 {
		panic("no return value specified for GetServer")
	}

	var r0 *model.Server
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uuid.UUID) (*model.Server, error)); ok {
		return rf(ctx, serverID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uuid.UUID) *model.Server); ok {
		r0 = rf(ctx, serverID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*model.Server)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, uuid.UUID) error); ok {
		r1 = rf(ctx, serverID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockFleetDB_GetServer_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetServer'
type MockFleetDB_GetServer_Call struct {
	*mock.Call
}

// GetServer is a helper method to define mock.On call
//   - ctx context.Context
//   - serverID uuid.UUID
func (_e *MockFleetDB_Expecter) GetServer(ctx interface{}, serverID interface{}) *MockFleetDB_GetServer_Call {
	return &MockFleetDB_GetServer_Call{Call: _e.mock.On("GetServer", ctx, serverID)}
}

func (_c *MockFleetDB_GetServer_Call) Run(run func(ctx context.Context, serverID uuid.UUID)) *MockFleetDB_GetServer_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uuid.UUID))
	})
	return _c
}

func (_c *MockFleetDB_GetServer_Call) Return(_a0 *model.Server, _a1 error) *MockFleetDB_GetServer_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockFleetDB_GetServer_Call) RunAndReturn(run func(context.Context, uuid.UUID) (*model.Server, error)) *MockFleetDB_GetServer_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockFleetDB creates a new instance of MockFleetDB. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockFleetDB(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockFleetDB {
	mock := &MockFleetDB{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}