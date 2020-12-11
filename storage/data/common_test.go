package data

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockPaginateableImpl struct {
	mock.Mock
}

func (m *MockPaginateableImpl) GetCursor() (*Cursor, error) {
	callInstance := m.Called()
	cursor := callInstance.Get(0).(*Cursor)
	return cursor, callInstance.Error(1)
}

func TestNewPagination(t *testing.T) {
	err := errors.New("Nil Error")
	cursor := Cursor("test")
	cursor2 := Cursor("test2")
	t.Run("AfterNilBeforeNil", func(t *testing.T) {
		t.Parallel()
		pagination := NewPagination(nil, nil)
		assert.Nil(t, pagination.Next)
		assert.Nil(t, pagination.Previous)
	})
	t.Run("AfterValBeforeNil", func(t *testing.T) {
		t.Parallel()
		after := new(MockPaginateableImpl)
		after.On("GetCursor").Return(&cursor, nil)
		pagination := NewPagination(after, nil)
		after.AssertExpectations(t)
		assert.Nil(t, pagination.Previous)
		assert.Equal(t, pagination.Next, &cursor)
	})
	t.Run("AfterNilBeforeVal", func(t *testing.T) {
		t.Parallel()
		before := new(MockPaginateableImpl)
		before.On("GetCursor").Return(&cursor2, nil)
		pagination := NewPagination(nil, before)
		before.AssertExpectations(t)
		assert.Nil(t, pagination.Next)
		assert.Equal(t, pagination.Previous, &cursor2)
	})
	t.Run("AfterCursorErr", func(t *testing.T) {
		t.Parallel()
		after := new(MockPaginateableImpl)
		before := new(MockPaginateableImpl)
		after.On("GetCursor").Return(&cursor, err)
		pagination := NewPagination(after, before)
		assert.Nil(t, pagination.Next)
		assert.Nil(t, pagination.Previous)
		after.AssertExpectations(t)
		before.AssertExpectations(t)
	})
	t.Run("BeforeCursorErr", func(t *testing.T) {
		t.Parallel()
		after := new(MockPaginateableImpl)
		before := new(MockPaginateableImpl)
		after.On("GetCursor").Return(&cursor, nil)
		before.On("GetCursor").Return(&cursor2, err)
		pagination := NewPagination(after, before)
		assert.Nil(t, pagination.Next)
		assert.Nil(t, pagination.Previous)
		after.AssertExpectations(t)
		before.AssertExpectations(t)
	})
	t.Run("AfterValBeforeVal", func(t *testing.T) {
		t.Parallel()
		after := new(MockPaginateableImpl)
		before := new(MockPaginateableImpl)
		after.On("GetCursor").Return(&cursor, nil)
		before.On("GetCursor").Return(&cursor2, nil)
		pagination := NewPagination(after, before)
		assert.Equal(t, pagination.Next, &cursor)
		assert.Equal(t, pagination.Previous, &cursor2)
		after.AssertExpectations(t)
		before.AssertExpectations(t)
	})
}

func TestGetLastUpdatedHTTPTimeString(t *testing.T) {
	currentTime := time.Now()
	base := BasePaginateable{UpdatedAt: currentTime}
	assert.Equal(t, currentTime.Format(http.TimeFormat), base.GetLastUpdatedHTTPTimeString())
}
