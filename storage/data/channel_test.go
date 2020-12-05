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
		channel, err := NewChannel(someID, someToken)
		assert.Nil(t, err)
		assert.NotNil(t, channel.ID)
		assert.Equal(t, someID, channel.ChannelID)
		assert.Equal(t, someID, channel.Name)
		assert.Equal(t, someToken, channel.Token)
	})
}

func TestChannelIsInValidState(t *testing.T) {
	t.Run("True", func(t *testing.T) {
		t.Parallel()
		channel, _ := NewChannel(someID, someToken)
		assert.True(t, channel.IsInValidState())
	})
	t.Run("EmptyIDFalse", func(t *testing.T) {
		t.Parallel()
		channel, _ := NewChannel(someID, someToken)
		channel.ChannelID = ""
		assert.False(t, channel.IsInValidState())
	})
}

func TestChannelQuickFix(t *testing.T) {
	t.Parallel()
	channel := Channel{ChannelID: someID}
	channel.Token = someToken
	assert.False(t, channel.IsInValidState())
	assert.True(t, len(channel.Name) <= 0)
	assert.True(t, channel.QuickFix())
	assert.True(t, channel.IsInValidState())
	assert.Equal(t, someID, channel.Name)
}
