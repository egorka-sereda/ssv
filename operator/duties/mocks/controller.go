// Code generated by MockGen. DO NOT EDIT.
// Source: ./controller.go

// Package mocks is a generated GoMock package.
package mocks

import (
	reflect "reflect"

	spectypes "github.com/bloxapp/ssv-spec/types"
	gomock "github.com/golang/mock/gomock"
)

// MockDutyExecutor is a mock of DutyExecutor interface
type MockDutyExecutor struct {
	ctrl     *gomock.Controller
	recorder *MockDutyExecutorMockRecorder
}

// MockDutyExecutorMockRecorder is the mock recorder for MockDutyExecutor
type MockDutyExecutorMockRecorder struct {
	mock *MockDutyExecutor
}

// NewMockDutyExecutor creates a new mock instance
func NewMockDutyExecutor(ctrl *gomock.Controller) *MockDutyExecutor {
	mock := &MockDutyExecutor{ctrl: ctrl}
	mock.recorder = &MockDutyExecutorMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockDutyExecutor) EXPECT() *MockDutyExecutorMockRecorder {
	return m.recorder
}

// ExecuteDuty mocks base method
func (m *MockDutyExecutor) ExecuteDuty(duty *spectypes.Duty) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ExecuteDuty", duty)
	ret0, _ := ret[0].(error)
	return ret0
}

// ExecuteDuty indicates an expected call of ExecuteDuty
func (mr *MockDutyExecutorMockRecorder) ExecuteDuty(duty interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ExecuteDuty", reflect.TypeOf((*MockDutyExecutor)(nil).ExecuteDuty), duty)
}

// MockDutyController is a mock of DutyController interface
type MockDutyController struct {
	ctrl     *gomock.Controller
	recorder *MockDutyControllerMockRecorder
}

// MockDutyControllerMockRecorder is the mock recorder for MockDutyController
type MockDutyControllerMockRecorder struct {
	mock *MockDutyController
}

// NewMockDutyController creates a new mock instance
func NewMockDutyController(ctrl *gomock.Controller) *MockDutyController {
	mock := &MockDutyController{ctrl: ctrl}
	mock.recorder = &MockDutyControllerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockDutyController) EXPECT() *MockDutyControllerMockRecorder {
	return m.recorder
}

// Start mocks base method
func (m *MockDutyController) Start() {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Start")
}

// Start indicates an expected call of Start
func (mr *MockDutyControllerMockRecorder) Start() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Start", reflect.TypeOf((*MockDutyController)(nil).Start))
}

// CurrentSlotChan mocks base method
func (m *MockDutyController) CurrentSlotChan() <-chan uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CurrentSlotChan")
	ret0, _ := ret[0].(<-chan uint64)
	return ret0
}

// CurrentSlotChan indicates an expected call of CurrentSlotChan
func (mr *MockDutyControllerMockRecorder) CurrentSlotChan() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CurrentSlotChan", reflect.TypeOf((*MockDutyController)(nil).CurrentSlotChan))
}
