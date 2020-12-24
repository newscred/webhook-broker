package data

import (
	"encoding/base64"
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
	cursor := Cursor{ID: "test"}
	cursor2 := Cursor{ID: "test2"}
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

func TestCursorString(t *testing.T) {
	testTime := time.Now()
	testID := "testing"
	cursor := &Cursor{ID: testID, Timestamp: testTime}
	assert.Equal(t, string(base64.StdEncoding.EncodeToString([]byte(testID+cursorSeparator+testTime.Format(time.RFC3339Nano)))), cursor.String())
}

func TestParseCursor(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		testTime := time.Now()
		testID := "testing"
		cursor := &Cursor{ID: testID, Timestamp: testTime}
		parsedCursor, err := ParseCursor(cursor.String())
		assert.Nil(t, err)
		assert.Equal(t, testID, parsedCursor.ID)
		assert.True(t, cursor.Timestamp.Equal(parsedCursor.Timestamp))
	})
	t.Run("NoEnoughSplits", func(t *testing.T) {
		t.Parallel()
		testTime := time.Now()
		testID := "testing"
		cursorString := string(base64.StdEncoding.EncodeToString([]byte(testID + testTime.Format(time.RFC3339Nano))))
		parsedCursor, err := ParseCursor(cursorString)
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
		assert.NotNil(t, parsedCursor)
		cursorString = string(base64.StdEncoding.EncodeToString([]byte(testID + cursorString + testTime.Format(time.RFC3339Nano) + cursorString)))
		parsedCursor, err = ParseCursor(cursorString)
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
		assert.NotNil(t, parsedCursor)
	})
	t.Run("TimeFormatIncorrect", func(t *testing.T) {
		t.Parallel()
		testTime := time.Now()
		testID := "testing"
		cursorString := string(base64.StdEncoding.EncodeToString([]byte(testID + testTime.Format(time.RFC1123))))
		parsedCursor, err := ParseCursor(cursorString)
		assert.NotNil(t, err)
		assert.NotNil(t, parsedCursor)
	})
}
