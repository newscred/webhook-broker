package dispatcher

import (
	"testing"

	"github.com/imyousuf/webhook-broker/storage/mocks"
	"github.com/stretchr/testify/assert"
)

func TestNewMessageDispatcher(t *testing.T) {
	deferFunc := func() {
		if r := recover(); r != panicString {
			t.Fail()
		}
	}
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		mRepo := new(mocks.MessageRepository)
		cRepo := new(mocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, cRepo))
	})
	t.Run("MsgRepoNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		cRepo := new(mocks.ConsumerRepository)
		assert.NotNil(t, NewMessageDispatcher(nil, cRepo))
	})
	t.Run("ConsumerRepoNil", func(t *testing.T) {
		t.Parallel()
		defer deferFunc()
		mRepo := new(mocks.MessageRepository)
		assert.NotNil(t, NewMessageDispatcher(mRepo, nil))
	})
}

func TestMessageDispatcherImpl_Dispatch(t *testing.T) {
	t.Parallel()
	mRepo := new(mocks.MessageRepository)
	cRepo := new(mocks.ConsumerRepository)
	dispatcher := NewMessageDispatcher(mRepo, cRepo)
	assert.NotNil(t, dispatcher)
	dispatcher.Dispatch(nil)
}
