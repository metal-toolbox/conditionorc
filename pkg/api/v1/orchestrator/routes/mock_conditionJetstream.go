// Code generated by mockery v2.42.1. DO NOT EDIT.

package routes

import (
	context "context"

	condition "github.com/metal-toolbox/rivets/condition"

	mock "github.com/stretchr/testify/mock"

	uuid "github.com/google/uuid"
)

// MockconditionJetstream is an autogenerated mock type for the conditionJetstream type
type MockconditionJetstream struct {
	mock.Mock
}

type MockconditionJetstream_Expecter struct {
	mock *mock.Mock
}

func (_m *MockconditionJetstream) EXPECT() *MockconditionJetstream_Expecter {
	return &MockconditionJetstream_Expecter{mock: &_m.Mock}
}

// pop provides a mock function with given fields: ctx, conditionKind, serverID
func (_m *MockconditionJetstream) pop(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID) (*condition.Condition, error) {
	ret := _m.Called(ctx, conditionKind, serverID)

	if len(ret) == 0 {
		panic("no return value specified for pop")
	}

	var r0 *condition.Condition
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID) (*condition.Condition, error)); ok {
		return rf(ctx, conditionKind, serverID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, condition.Kind, uuid.UUID) *condition.Condition); ok {
		r0 = rf(ctx, conditionKind, serverID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*condition.Condition)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, condition.Kind, uuid.UUID) error); ok {
		r1 = rf(ctx, conditionKind, serverID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockconditionJetstream_pop_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'pop'
type MockconditionJetstream_pop_Call struct {
	*mock.Call
}

// pop is a helper method to define mock.On call
//   - ctx context.Context
//   - conditionKind condition.Kind
//   - serverID uuid.UUID
func (_e *MockconditionJetstream_Expecter) pop(ctx interface{}, conditionKind interface{}, serverID interface{}) *MockconditionJetstream_pop_Call {
	return &MockconditionJetstream_pop_Call{Call: _e.mock.On("pop", ctx, conditionKind, serverID)}
}

func (_c *MockconditionJetstream_pop_Call) Run(run func(ctx context.Context, conditionKind condition.Kind, serverID uuid.UUID)) *MockconditionJetstream_pop_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(condition.Kind), args[2].(uuid.UUID))
	})
	return _c
}

func (_c *MockconditionJetstream_pop_Call) Return(_a0 *condition.Condition, _a1 error) *MockconditionJetstream_pop_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockconditionJetstream_pop_Call) RunAndReturn(run func(context.Context, condition.Kind, uuid.UUID) (*condition.Condition, error)) *MockconditionJetstream_pop_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockconditionJetstream creates a new instance of MockconditionJetstream. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockconditionJetstream(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockconditionJetstream {
	mock := &MockconditionJetstream{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}