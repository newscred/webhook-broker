// Code generated by mockery v2.46.3. DO NOT EDIT.

package mocks

import (
	data "github.com/newscred/webhook-broker/storage/data"
	mock "github.com/stretchr/testify/mock"

	time "time"
)

// DeliveryJobRepository is an autogenerated mock type for the DeliveryJobRepository type
type DeliveryJobRepository struct {
	mock.Mock
}

// DispatchMessage provides a mock function with given fields: message, deliveryJobs
func (_m *DeliveryJobRepository) DispatchMessage(message *data.Message, deliveryJobs ...*data.DeliveryJob) error {
	_va := make([]interface{}, len(deliveryJobs))
	for _i := range deliveryJobs {
		_va[_i] = deliveryJobs[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, message)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for DispatchMessage")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.Message, ...*data.DeliveryJob) error); ok {
		r0 = rf(message, deliveryJobs...)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// GetByID provides a mock function with given fields: id
func (_m *DeliveryJobRepository) GetByID(id string) (*data.DeliveryJob, error) {
	ret := _m.Called(id)

	if len(ret) == 0 {
		panic("no return value specified for GetByID")
	}

	var r0 *data.DeliveryJob
	var r1 error
	if rf, ok := ret.Get(0).(func(string) (*data.DeliveryJob, error)); ok {
		return rf(id)
	}
	if rf, ok := ret.Get(0).(func(string) *data.DeliveryJob); ok {
		r0 = rf(id)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*data.DeliveryJob)
		}
	}

	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(id)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetJobsForConsumer provides a mock function with given fields: consumer, jobStatus, page
func (_m *DeliveryJobRepository) GetJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error) {
	ret := _m.Called(consumer, jobStatus, page)

	if len(ret) == 0 {
		panic("no return value specified for GetJobsForConsumer")
	}

	var r0 []*data.DeliveryJob
	var r1 *data.Pagination
	var r2 error
	if rf, ok := ret.Get(0).(func(*data.Consumer, data.JobStatus, *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error)); ok {
		return rf(consumer, jobStatus, page)
	}
	if rf, ok := ret.Get(0).(func(*data.Consumer, data.JobStatus, *data.Pagination) []*data.DeliveryJob); ok {
		r0 = rf(consumer, jobStatus, page)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*data.DeliveryJob)
		}
	}

	if rf, ok := ret.Get(1).(func(*data.Consumer, data.JobStatus, *data.Pagination) *data.Pagination); ok {
		r1 = rf(consumer, jobStatus, page)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*data.Pagination)
		}
	}

	if rf, ok := ret.Get(2).(func(*data.Consumer, data.JobStatus, *data.Pagination) error); ok {
		r2 = rf(consumer, jobStatus, page)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// GetJobsForMessage provides a mock function with given fields: message, page
func (_m *DeliveryJobRepository) GetJobsForMessage(message *data.Message, page *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error) {
	ret := _m.Called(message, page)

	if len(ret) == 0 {
		panic("no return value specified for GetJobsForMessage")
	}

	var r0 []*data.DeliveryJob
	var r1 *data.Pagination
	var r2 error
	if rf, ok := ret.Get(0).(func(*data.Message, *data.Pagination) ([]*data.DeliveryJob, *data.Pagination, error)); ok {
		return rf(message, page)
	}
	if rf, ok := ret.Get(0).(func(*data.Message, *data.Pagination) []*data.DeliveryJob); ok {
		r0 = rf(message, page)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*data.DeliveryJob)
		}
	}

	if rf, ok := ret.Get(1).(func(*data.Message, *data.Pagination) *data.Pagination); ok {
		r1 = rf(message, page)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*data.Pagination)
		}
	}

	if rf, ok := ret.Get(2).(func(*data.Message, *data.Pagination) error); ok {
		r2 = rf(message, page)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// GetJobsInflightSince provides a mock function with given fields: delta
func (_m *DeliveryJobRepository) GetJobsInflightSince(delta time.Duration) []*data.DeliveryJob {
	ret := _m.Called(delta)

	if len(ret) == 0 {
		panic("no return value specified for GetJobsInflightSince")
	}

	var r0 []*data.DeliveryJob
	if rf, ok := ret.Get(0).(func(time.Duration) []*data.DeliveryJob); ok {
		r0 = rf(delta)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*data.DeliveryJob)
		}
	}

	return r0
}

// GetJobsReadyForInflightSince provides a mock function with given fields: delta
func (_m *DeliveryJobRepository) GetJobsReadyForInflightSince(delta time.Duration) []*data.DeliveryJob {
	ret := _m.Called(delta)

	if len(ret) == 0 {
		panic("no return value specified for GetJobsReadyForInflightSince")
	}

	var r0 []*data.DeliveryJob
	if rf, ok := ret.Get(0).(func(time.Duration) []*data.DeliveryJob); ok {
		r0 = rf(delta)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*data.DeliveryJob)
		}
	}

	return r0
}

// GetPrioritizedJobsForConsumer provides a mock function with given fields: consumer, jobStatus, pageSize
func (_m *DeliveryJobRepository) GetPrioritizedJobsForConsumer(consumer *data.Consumer, jobStatus data.JobStatus, pageSize int) ([]*data.DeliveryJob, error) {
	ret := _m.Called(consumer, jobStatus, pageSize)

	if len(ret) == 0 {
		panic("no return value specified for GetPrioritizedJobsForConsumer")
	}

	var r0 []*data.DeliveryJob
	var r1 error
	if rf, ok := ret.Get(0).(func(*data.Consumer, data.JobStatus, int) ([]*data.DeliveryJob, error)); ok {
		return rf(consumer, jobStatus, pageSize)
	}
	if rf, ok := ret.Get(0).(func(*data.Consumer, data.JobStatus, int) []*data.DeliveryJob); ok {
		r0 = rf(consumer, jobStatus, pageSize)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*data.DeliveryJob)
		}
	}

	if rf, ok := ret.Get(1).(func(*data.Consumer, data.JobStatus, int) error); ok {
		r1 = rf(consumer, jobStatus, pageSize)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MarkDeadJobAsInflight provides a mock function with given fields: deliveryJob
func (_m *DeliveryJobRepository) MarkDeadJobAsInflight(deliveryJob *data.DeliveryJob) error {
	ret := _m.Called(deliveryJob)

	if len(ret) == 0 {
		panic("no return value specified for MarkDeadJobAsInflight")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.DeliveryJob) error); ok {
		r0 = rf(deliveryJob)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MarkJobDead provides a mock function with given fields: deliveryJob
func (_m *DeliveryJobRepository) MarkJobDead(deliveryJob *data.DeliveryJob) error {
	ret := _m.Called(deliveryJob)

	if len(ret) == 0 {
		panic("no return value specified for MarkJobDead")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.DeliveryJob) error); ok {
		r0 = rf(deliveryJob)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MarkJobDelivered provides a mock function with given fields: deliveryJob
func (_m *DeliveryJobRepository) MarkJobDelivered(deliveryJob *data.DeliveryJob) error {
	ret := _m.Called(deliveryJob)

	if len(ret) == 0 {
		panic("no return value specified for MarkJobDelivered")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.DeliveryJob) error); ok {
		r0 = rf(deliveryJob)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MarkJobInflight provides a mock function with given fields: deliveryJob
func (_m *DeliveryJobRepository) MarkJobInflight(deliveryJob *data.DeliveryJob) error {
	ret := _m.Called(deliveryJob)

	if len(ret) == 0 {
		panic("no return value specified for MarkJobInflight")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.DeliveryJob) error); ok {
		r0 = rf(deliveryJob)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MarkJobRetry provides a mock function with given fields: deliveryJob, earliestDelta
func (_m *DeliveryJobRepository) MarkJobRetry(deliveryJob *data.DeliveryJob, earliestDelta time.Duration) error {
	ret := _m.Called(deliveryJob, earliestDelta)

	if len(ret) == 0 {
		panic("no return value specified for MarkJobRetry")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.DeliveryJob, time.Duration) error); ok {
		r0 = rf(deliveryJob, earliestDelta)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MarkQueuedJobAsDead provides a mock function with given fields: deliveryJob
func (_m *DeliveryJobRepository) MarkQueuedJobAsDead(deliveryJob *data.DeliveryJob) error {
	ret := _m.Called(deliveryJob)

	if len(ret) == 0 {
		panic("no return value specified for MarkQueuedJobAsDead")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.DeliveryJob) error); ok {
		r0 = rf(deliveryJob)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// RequeueDeadJobsForConsumer provides a mock function with given fields: consumer
func (_m *DeliveryJobRepository) RequeueDeadJobsForConsumer(consumer *data.Consumer) error {
	ret := _m.Called(consumer)

	if len(ret) == 0 {
		panic("no return value specified for RequeueDeadJobsForConsumer")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*data.Consumer) error); ok {
		r0 = rf(consumer)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewDeliveryJobRepository creates a new instance of DeliveryJobRepository. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewDeliveryJobRepository(t interface {
	mock.TestingT
	Cleanup(func())
}) *DeliveryJobRepository {
	mock := &DeliveryJobRepository{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
