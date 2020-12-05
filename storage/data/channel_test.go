package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewChannel(t *testing.T) {
	t.Run("EmptyID", func(t *testing.T) {
		t.Parallel()
		_, err := NewChannel("", "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("EmptyToken", func(t *testing.T) {
		t.Parallel()
		_, err := NewChannel(someID, "")
		assert.Equal(t, ErrInsufficientInformationForCreating, err)
	})
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		producer, err := NewChannel(someID, someToken)
		assert.Nil(t, err)
		assert.NotNil(t, producer.ID)
		assert.Equal(t, someID, producer.ChannelID)
		assert.Equal(t, someID, producer.Name)
		assert.Equal(t, someToken, producer.Token)
	})
}

func TestChannelIsInValidState(t *testing.T) {
	t.Run("True", func(t *testing.T) {
		t.Parallel()
		producer, _ := NewChannel(someID, someToken)
		assert.True(t, producer.IsInValidState())
	})
	t.Run("EmptyIDFalse", func(t *testing.T) {
		t.Parallel()
		producer, _ := NewChannel(someID, someToken)
		producer.ChannelID = ""
		assert.False(t, producer.IsInValidState())
	})
}

func TestChannelQuickFix(t *testing.T) {
	t.Parallel()
	producer := Channel{ChannelID: someID}
	producer.Token = someToken
	assert.False(t, producer.IsInValidState())
	assert.True(t, len(producer.Name) <= 0)
	producer.QuickFix()
	assert.True(t, producer.IsInValidState())
	assert.Equal(t, someID, producer.Name)
}
