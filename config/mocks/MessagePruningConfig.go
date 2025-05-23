// Code generated by mockery v2.53.3. DO NOT EDIT.

package mocks

import (
	config "github.com/newscred/webhook-broker/config"
	mock "github.com/stretchr/testify/mock"

	url "net/url"
)

// MessagePruningConfig is an autogenerated mock type for the MessagePruningConfig type
type MessagePruningConfig struct {
	mock.Mock
}

// GetExportNodeName provides a mock function with no fields
func (_m *MessagePruningConfig) GetExportNodeName() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetExportNodeName")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetExportPath provides a mock function with no fields
func (_m *MessagePruningConfig) GetExportPath() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetExportPath")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetMaxArchiveFileSizeInMB provides a mock function with no fields
func (_m *MessagePruningConfig) GetMaxArchiveFileSizeInMB() uint {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetMaxArchiveFileSizeInMB")
	}

	var r0 uint
	if rf, ok := ret.Get(0).(func() uint); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint)
	}

	return r0
}

// GetMessageRetentionDays provides a mock function with no fields
func (_m *MessagePruningConfig) GetMessageRetentionDays() uint {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetMessageRetentionDays")
	}

	var r0 uint
	if rf, ok := ret.Get(0).(func() uint); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint)
	}

	return r0
}

// GetRemoteExportDestination provides a mock function with no fields
func (_m *MessagePruningConfig) GetRemoteExportDestination() config.RemoteMessageDestination {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetRemoteExportDestination")
	}

	var r0 config.RemoteMessageDestination
	if rf, ok := ret.Get(0).(func() config.RemoteMessageDestination); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(config.RemoteMessageDestination)
	}

	return r0
}

// GetRemoteExportURL provides a mock function with no fields
func (_m *MessagePruningConfig) GetRemoteExportURL() *url.URL {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetRemoteExportURL")
	}

	var r0 *url.URL
	if rf, ok := ret.Get(0).(func() *url.URL); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*url.URL)
		}
	}

	return r0
}

// GetRemoteFilePrefix provides a mock function with no fields
func (_m *MessagePruningConfig) GetRemoteFilePrefix() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetRemoteFilePrefix")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// IsPruningEnabled provides a mock function with no fields
func (_m *MessagePruningConfig) IsPruningEnabled() bool {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for IsPruningEnabled")
	}

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// NewMessagePruningConfig creates a new instance of MessagePruningConfig. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMessagePruningConfig(t interface {
	mock.TestingT
	Cleanup(func())
}) *MessagePruningConfig {
	mock := &MessagePruningConfig{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
