// Code generated by mockery v2.42.1. DO NOT EDIT.

package client

import (
	context "context"

	condition "github.com/metal-toolbox/rivets/condition"

	mock "github.com/stretchr/testify/mock"

	registry "github.com/metal-toolbox/rivets/events/registry"

	types "github.com/metal-toolbox/conditionorc/pkg/api/v1/orchestrator/types"

	uuid "github.com/google/uuid"
)

// MockQueryor is an autogenerated mock type for the Queryor type
type MockQueryor struct {
	mock.Mock
}

type MockQueryor_Expecter struct {
	mock *mock.Mock
}

func (_m *MockQueryor) EXPECT() *MockQueryor_Expecter {
	return &MockQueryor_Expecter{mock: &_m.Mock}
}

// ConditionQueuePop provides a mock function with given fields: ctx, conditionKind, serverID
func (_m *MockQueryor) ConditionQueuePop(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID) (*types.ServerResponse, error) {
	ret := _m.Called(ctx, conditionKind, serverID)

	if len(ret) == 0 {
		panic("no return value specified for ConditionQueuePop")
	}

	var r0 *types.ServerResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID) (*types.ServerResponse, error)); ok {
		return rf(ctx, conditionKind, serverID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID) *types.ServerResponse); ok {
		r0 = rf(ctx, conditionKind, serverID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ServerResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, condition.Kind, uuid.UUID) error); ok {
		r1 = rf(ctx, conditionKind, serverID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockQueryor_ConditionQueuePop_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ConditionQueuePop'
type MockQueryor_ConditionQueuePop_Call struct {
	*mock.Call
}

// ConditionQueuePop is a helper method to define mock.On call
//   - ctx context.Context
//   - conditionKind condition.Kind
//   - serverID uuid.UUID
func (_e *MockQueryor_Expecter) ConditionQueuePop(ctx interface{}, conditionKind interface{}, serverID interface{}) *MockQueryor_ConditionQueuePop_Call {
	return &MockQueryor_ConditionQueuePop_Call{Call: _e.mock.On("ConditionQueuePop", ctx, conditionKind, serverID)}
}

func (_c *MockQueryor_ConditionQueuePop_Call) Run(run func(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID)) *MockQueryor_ConditionQueuePop_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(condition.Kind), args[2].(uuid.UUID))
	})
	return _c
}

func (_c *MockQueryor_ConditionQueuePop_Call) Return(_a0 *types.ServerResponse, _a1 error) *MockQueryor_ConditionQueuePop_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockQueryor_ConditionQueuePop_Call) RunAndReturn(run func(context.Context, condition.Kind, uuid.UUID) (*types.ServerResponse, error)) *MockQueryor_ConditionQueuePop_Call {
	_c.Call.Return(run)
	return _c
}

// ConditionStatusUpdate provides a mock function with given fields: ctx, conditionKind, serverID, conditionID, controllerID, statusValue, onlyUpdateTimestamp
func (_m *MockQueryor) ConditionStatusUpdate(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID, conditionID uuid.UUID, controllerID registry.ControllerID, statusValue *condition.StatusValue, onlyUpdateTimestamp bool) (*types.ServerResponse, error) {
	ret := _m.Called(ctx, conditionKind, serverID, conditionID, controllerID, statusValue, onlyUpdateTimestamp)

	if len(ret) == 0 {
		panic("no return value specified for ConditionStatusUpdate")
	}

	var r0 *types.ServerResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, registry.ControllerID, *condition.StatusValue, bool) (*types.ServerResponse, error)); ok {
		return rf(ctx, conditionKind, serverID, conditionID, controllerID, statusValue, onlyUpdateTimestamp)
	}
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, registry.ControllerID, *condition.StatusValue, bool) *types.ServerResponse); ok {
		r0 = rf(ctx, conditionKind, serverID, conditionID, controllerID, statusValue, onlyUpdateTimestamp)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ServerResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, registry.ControllerID, *condition.StatusValue, bool) error); ok {
		r1 = rf(ctx, conditionKind, serverID, conditionID, controllerID, statusValue, onlyUpdateTimestamp)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockQueryor_ConditionStatusUpdate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ConditionStatusUpdate'
type MockQueryor_ConditionStatusUpdate_Call struct {
	*mock.Call
}

// ConditionStatusUpdate is a helper method to define mock.On call
//   - ctx context.Context
//   - conditionKind condition.Kind
//   - serverID uuid.UUID
//   - conditionID uuid.UUID
//   - controllerID registry.ControllerID
//   - statusValue *condition.StatusValue
//   - onlyUpdateTimestamp bool
func (_e *MockQueryor_Expecter) ConditionStatusUpdate(ctx interface{}, conditionKind interface{}, serverID interface{}, conditionID interface{}, controllerID interface{}, statusValue interface{}, onlyUpdateTimestamp interface{}) *MockQueryor_ConditionStatusUpdate_Call {
	return &MockQueryor_ConditionStatusUpdate_Call{Call: _e.mock.On("ConditionStatusUpdate", ctx, conditionKind, serverID, conditionID, controllerID, statusValue, onlyUpdateTimestamp)}
}

func (_c *MockQueryor_ConditionStatusUpdate_Call) Run(run func(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID, conditionID uuid.UUID, controllerID registry.ControllerID, statusValue *condition.StatusValue, onlyUpdateTimestamp bool)) *MockQueryor_ConditionStatusUpdate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(condition.Kind), args[2].(uuid.UUID), args[3].(uuid.UUID), args[4].(registry.ControllerID), args[5].(*condition.StatusValue), args[6].(bool))
	})
	return _c
}

func (_c *MockQueryor_ConditionStatusUpdate_Call) Return(_a0 *types.ServerResponse, _a1 error) *MockQueryor_ConditionStatusUpdate_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockQueryor_ConditionStatusUpdate_Call) RunAndReturn(run func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, registry.ControllerID, *condition.StatusValue, bool) (*types.ServerResponse, error)) *MockQueryor_ConditionStatusUpdate_Call {
	_c.Call.Return(run)
	return _c
}

// ConditionTaskPublish provides a mock function with given fields: ctx, conditionKind, serverID, conditionID, task, onlyUpdateTimestamp
func (_m *MockQueryor) ConditionTaskPublish(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID, conditionID uuid.UUID, task *condition.Task[interface{}, interface{}], onlyUpdateTimestamp bool) (*types.ServerResponse, error) {
	ret := _m.Called(ctx, conditionKind, serverID, conditionID, task, onlyUpdateTimestamp)

	if len(ret) == 0 {
		panic("no return value specified for ConditionTaskPublish")
	}

	var r0 *types.ServerResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, *condition.Task[interface{}, interface{}], bool) (*types.ServerResponse, error)); ok {
		return rf(ctx, conditionKind, serverID, conditionID, task, onlyUpdateTimestamp)
	}
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, *condition.Task[interface{}, interface{}], bool) *types.ServerResponse); ok {
		r0 = rf(ctx, conditionKind, serverID, conditionID, task, onlyUpdateTimestamp)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ServerResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, *condition.Task[interface{}, interface{}], bool) error); ok {
		r1 = rf(ctx, conditionKind, serverID, conditionID, task, onlyUpdateTimestamp)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockQueryor_ConditionTaskPublish_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ConditionTaskPublish'
type MockQueryor_ConditionTaskPublish_Call struct {
	*mock.Call
}

// ConditionTaskPublish is a helper method to define mock.On call
//   - ctx context.Context
//   - conditionKind condition.Kind
//   - serverID uuid.UUID
//   - conditionID uuid.UUID
//   - task *condition.Task[interface{},interface{}]
//   - onlyUpdateTimestamp bool
func (_e *MockQueryor_Expecter) ConditionTaskPublish(ctx interface{}, conditionKind interface{}, serverID interface{}, conditionID interface{}, task interface{}, onlyUpdateTimestamp interface{}) *MockQueryor_ConditionTaskPublish_Call {
	return &MockQueryor_ConditionTaskPublish_Call{Call: _e.mock.On("ConditionTaskPublish", ctx, conditionKind, serverID, conditionID, task, onlyUpdateTimestamp)}
}

func (_c *MockQueryor_ConditionTaskPublish_Call) Run(run func(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID, conditionID uuid.UUID, task *condition.Task[interface{}, interface{}], onlyUpdateTimestamp bool)) *MockQueryor_ConditionTaskPublish_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(condition.Kind), args[2].(uuid.UUID), args[3].(uuid.UUID), args[4].(*condition.Task[interface{}, interface{}]), args[5].(bool))
	})
	return _c
}

func (_c *MockQueryor_ConditionTaskPublish_Call) Return(_a0 *types.ServerResponse, _a1 error) *MockQueryor_ConditionTaskPublish_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockQueryor_ConditionTaskPublish_Call) RunAndReturn(run func(context.Context, condition.Kind, uuid.UUID, uuid.UUID, *condition.Task[interface{}, interface{}], bool) (*types.ServerResponse, error)) *MockQueryor_ConditionTaskPublish_Call {
	_c.Call.Return(run)
	return _c
}

// ConditionTaskQuery provides a mock function with given fields: ctx, conditionKind, serverID
func (_m *MockQueryor) ConditionTaskQuery(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID) (*types.ServerResponse, error) {
	ret := _m.Called(ctx, conditionKind, serverID)

	if len(ret) == 0 {
		panic("no return value specified for ConditionTaskQuery")
	}

	var r0 *types.ServerResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID) (*types.ServerResponse, error)); ok {
		return rf(ctx, conditionKind, serverID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID) *types.ServerResponse); ok {
		r0 = rf(ctx, conditionKind, serverID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ServerResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, condition.Kind, uuid.UUID) error); ok {
		r1 = rf(ctx, conditionKind, serverID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockQueryor_ConditionTaskQuery_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ConditionTaskQuery'
type MockQueryor_ConditionTaskQuery_Call struct {
	*mock.Call
}

// ConditionTaskQuery is a helper method to define mock.On call
//   - ctx context.Context
//   - conditionKind condition.Kind
//   - serverID uuid.UUID
func (_e *MockQueryor_Expecter) ConditionTaskQuery(ctx interface{}, conditionKind interface{}, serverID interface{}) *MockQueryor_ConditionTaskQuery_Call {
	return &MockQueryor_ConditionTaskQuery_Call{Call: _e.mock.On("ConditionTaskQuery", ctx, conditionKind, serverID)}
}

func (_c *MockQueryor_ConditionTaskQuery_Call) Run(run func(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID)) *MockQueryor_ConditionTaskQuery_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(condition.Kind), args[2].(uuid.UUID))
	})
	return _c
}

func (_c *MockQueryor_ConditionTaskQuery_Call) Return(_a0 *types.ServerResponse, _a1 error) *MockQueryor_ConditionTaskQuery_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockQueryor_ConditionTaskQuery_Call) RunAndReturn(run func(context.Context, condition.Kind, uuid.UUID) (*types.ServerResponse, error)) *MockQueryor_ConditionTaskQuery_Call {
	_c.Call.Return(run)
	return _c
}

// ControllerCheckin provides a mock function with given fields: ctx, serverID, conditionID, controllerID
func (_m *MockQueryor) ControllerCheckin(ctx context.Context, serverID uuid.UUID, conditionID uuid.UUID, controllerID registry.ControllerID) (*types.ServerResponse, error) {
	ret := _m.Called(ctx, serverID, conditionID, controllerID)

	if len(ret) == 0 {
		panic("no return value specified for ControllerCheckin")
	}

	var r0 *types.ServerResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uuid.UUID, uuid.UUID, registry.ControllerID) (*types.ServerResponse, error)); ok {
		return rf(ctx, serverID, conditionID, controllerID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uuid.UUID, uuid.UUID, registry.ControllerID) *types.ServerResponse); ok {
		r0 = rf(ctx, serverID, conditionID, controllerID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ServerResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, uuid.UUID, uuid.UUID, registry.ControllerID) error); ok {
		r1 = rf(ctx, serverID, conditionID, controllerID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockQueryor_ControllerCheckin_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ControllerCheckin'
type MockQueryor_ControllerCheckin_Call struct {
	*mock.Call
}

// ControllerCheckin is a helper method to define mock.On call
//   - ctx context.Context
//   - serverID uuid.UUID
//   - conditionID uuid.UUID
//   - controllerID registry.ControllerID
func (_e *MockQueryor_Expecter) ControllerCheckin(ctx interface{}, serverID interface{}, conditionID interface{}, controllerID interface{}) *MockQueryor_ControllerCheckin_Call {
	return &MockQueryor_ControllerCheckin_Call{Call: _e.mock.On("ControllerCheckin", ctx, serverID, conditionID, controllerID)}
}

func (_c *MockQueryor_ControllerCheckin_Call) Run(run func(ctx context.Context, serverID uuid.UUID, conditionID uuid.UUID, controllerID registry.ControllerID)) *MockQueryor_ControllerCheckin_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uuid.UUID), args[2].(uuid.UUID), args[3].(registry.ControllerID))
	})
	return _c
}

func (_c *MockQueryor_ControllerCheckin_Call) Return(_a0 *types.ServerResponse, _a1 error) *MockQueryor_ControllerCheckin_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockQueryor_ControllerCheckin_Call) RunAndReturn(run func(context.Context, uuid.UUID, uuid.UUID, registry.ControllerID) (*types.ServerResponse, error)) *MockQueryor_ControllerCheckin_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockQueryor creates a new instance of MockQueryor. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockQueryor(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockQueryor {
	mock := &MockQueryor{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
