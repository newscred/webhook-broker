package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	someID    = "some-id"
	someToken = "some-token"
	someName  = "some-name"
)

func TestNewProducer(t *testing.T) {
	t.Run("EmptyID", func(t *testing.T) {
		t.Parallel()
		_, err := NewProducer("", "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("EmptyToken", func(t *testing.T) {
		t.Parallel()
		_, err := NewProducer(someID, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		producer, err := NewProducer(someID, someToken)
		assert.Nil(t, err)
		assert.NotNil(t, producer.ID)
		assert.Equal(t, someID, producer.ProducerID)
		assert.Equal(t, someID, producer.Name)
		assert.Equal(t, someToken, producer.Token)
	})
}

func TestGetCursor(t *testing.T) {
	producer, err := NewProducer(someID, someToken)
	assert.Nil(t, err)
	expectedCursor := Cursor{ID: producer.ID.String(), Timestamp: producer.CreatedAt}
	actualCursor, err := producer.GetCursor()
	assert.Nil(t, err)
	assert.Equal(t, expectedCursor, *actualCursor)
}

func TestQuickFix(t *testing.T) {
	t.Parallel()
	producer := Producer{ProducerID: someID}
	producer.Token = someToken
	assert.False(t, producer.IsInValidState())
	assert.True(t, producer.ID.IsNil())
	assert.True(t, producer.CreatedAt.IsZero())
	assert.True(t, producer.UpdatedAt.IsZero())
	producer.QuickFix()
	assert.True(t, producer.IsInValidState())
	assert.False(t, producer.ID.IsNil())
	assert.False(t, producer.CreatedAt.IsZero())
	assert.False(t, producer.UpdatedAt.IsZero())
}

func TestIsInValidState(t *testing.T) {
	t.Run("EmptyNameFalse", func(t *testing.T) {
		t.Parallel()
		producer, _ := NewProducer(someID, someToken)
		producer.Name = ""
		assert.False(t, producer.IsInValidState())
	})
	t.Run("EmptyTokenFalse", func(t *testing.T) {
		t.Parallel()
		producer, _ := NewProducer(someID, someToken)
		producer.Token = ""
		assert.False(t, producer.IsInValidState())
	})
}

func TestProducerQuickFix(t *testing.T) {
	t.Run("ChildQuickFixChange", func(t *testing.T) {
		t.Parallel()
		producer := Producer{ProducerID: someID}
		producer.Token = someToken
		assert.False(t, producer.IsInValidState())
		assert.True(t, len(producer.Name) <= 0)
		assert.True(t, producer.QuickFix())
		assert.True(t, producer.IsInValidState())
		assert.Equal(t, someID, producer.Name)
	})
	t.Run("ParentOnlyQuickFixChange", func(t *testing.T) {
		t.Parallel()
		producer := Producer{ProducerID: someID, MessageStakeholder: MessageStakeholder{Name: someName}}
		producer.Token = someToken
		assert.True(t, producer.QuickFix())
		assert.Equal(t, someName, producer.Name)
	})
}

func TestProducerIsInValidState(t *testing.T) {
	t.Run("True", func(t *testing.T) {
		t.Parallel()
		producer, _ := NewProducer(someID, someToken)
		assert.True(t, producer.IsInValidState())
	})
	t.Run("EmptyIDFalse", func(t *testing.T) {
		t.Parallel()
		producer, _ := NewProducer(someID, someToken)
		producer.ProducerID = ""
		assert.False(t, producer.IsInValidState())
	})
}
