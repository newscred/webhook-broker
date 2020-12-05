package data

import "net/url"

// Consumer is the object that producer broadcasts to and consumer consumes from
type Consumer struct {
	MessageStakeholder
	ConsumerID    string
	CallbackURL   string
	ConsumingFrom *Channel
}

// QuickFix fixes the model to set default ID, name same as producer id, created and updated at to current time.
func (consumer *Consumer) QuickFix() bool {
	madeChanges := consumer.BasePaginateable.QuickFix()
	if len(consumer.Name) <= 0 && len(consumer.ConsumerID) > 0 {
		consumer.Name = consumer.ConsumerID
		madeChanges = true
	}
	return madeChanges
}

// IsInValidState returns false if any of consumer id or name or token is empty, channel is not nil and callback URL is absolute URL
func (consumer *Consumer) IsInValidState() bool {
	if len(consumer.ConsumerID) <= 0 || len(consumer.Name) <= 0 || len(consumer.Token) <= 0 || consumer.ConsumingFrom == nil {
		return false
	}
	if callbackURL, err := url.Parse(consumer.CallbackURL); err != nil || !callbackURL.IsAbs() {
		return false
	}
	return true
}

// NewConsumer creates new Consumer
func NewConsumer(channel *Channel, consumerID, token string, callbackURL *url.URL) (*Consumer, error) {
	if len(consumerID) <= 0 || len(token) <= 0 || channel == nil || !callbackURL.IsAbs() {
		return nil, ErrInsufficientInformationForCreating
	}
	consumer := Consumer{ConsumerID: consumerID, ConsumingFrom: channel, CallbackURL: callbackURL.String(), MessageStakeholder: createMessageStakeholder(consumerID, token)}
	return &consumer, nil
}
