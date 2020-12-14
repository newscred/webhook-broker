package dispatcher

import (
	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
)

const (
	panicString = "parameters null"
)

// MessageDispatcher is the contract for dispatching message
type MessageDispatcher interface {
	Dispatch(message *data.Message)
}

// MessageDispatcherImpl is responsible for dispatching delivery jobs from acknowledged message
type MessageDispatcherImpl struct {
	msgRepo      storage.MessageRepository
	consumerRepo storage.ConsumerRepository
}

// Dispatch is responsible for dispatching delivery jobs for the message
func (msgDispatcher *MessageDispatcherImpl) Dispatch(message *data.Message) {

}

// NewMessageDispatcher retrieves new instance of MessageDispatcher
func NewMessageDispatcher(msgRepo storage.MessageRepository, consumerRepo storage.ConsumerRepository) MessageDispatcher {
	if msgRepo == nil || consumerRepo == nil {
		panic(panicString)
	}
	return &MessageDispatcherImpl{msgRepo: msgRepo, consumerRepo: consumerRepo}
}
