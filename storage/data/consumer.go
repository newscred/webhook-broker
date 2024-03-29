package data

import (
	"fmt"
	"net/url"
)

type ConsumerType int

const (
	PullConsumer ConsumerType = iota
	PushConsumer
	PullConsumerStr = "pull"
	PushConsumerStr = "push"
)

func (consumerType ConsumerType) String() string {
	switch consumerType {
	case PullConsumer:
		return PullConsumerStr
	default:
		return PushConsumerStr
	}
}

func getConsumerTypeFromString(value string) (ConsumerType, error) {
	switch value {
	case PullConsumerStr:
		return PullConsumer, nil
	case PushConsumerStr:
		return PushConsumer, nil
	case "":
		return PushConsumer, nil
	default:
		return PushConsumer, fmt.Errorf("invalid consumerType: %v", value)
	}
}

// Consumer is the object that producer broadcasts to and consumer consumes from
type Consumer struct {
	MessageStakeholder
	ConsumerID    string
	CallbackURL   string
	ConsumingFrom *Channel
	Type          ConsumerType
}

// QuickFix fixes the model to set default ID, name same as producer id, created and updated at to current time.
func (consumer *Consumer) QuickFix() bool {
	madeChanges := consumer.BasePaginateable.QuickFix()
	madeChanges = setValIfBothNotEmpty(&consumer.Name, &consumer.ConsumerID) || madeChanges
	return madeChanges
}

// IsInValidState returns false if any of consumer id or name or token is empty, channel is not nil and callback URL is absolute URL
func (consumer *Consumer) IsInValidState() bool {
	if len(consumer.ConsumerID) <= 0 || len(consumer.Name) <= 0 || len(consumer.Token) <= 0 || consumer.ConsumingFrom == nil || !consumer.ConsumingFrom.IsInValidState() {
		return false
	}
	if callbackURL, err := url.Parse(consumer.CallbackURL); err != nil || !callbackURL.IsAbs() {
		return false
	}
	return true
}

// GetChannelIDSafely retrieves channel id account for the fact that ConsumingFrom may be null
func (consumer *Consumer) GetChannelIDSafely() (channelID string) {
	if consumer.ConsumingFrom != nil {
		channelID = consumer.ConsumingFrom.ChannelID
	}
	return channelID
}

// NewConsumer creates new Consumer
func NewConsumer(channel *Channel, consumerID, token string, callbackURL *url.URL, consumerTypeStr string) (*Consumer, error) {
	if len(consumerID) <= 0 || len(token) <= 0 || channel == nil || !callbackURL.IsAbs() {
		return nil, ErrInsufficientInformationForCreating
	}
	consumerType, err := getConsumerTypeFromString(consumerTypeStr)
	if err != nil {
		return nil, err
	}
	consumer := Consumer{ConsumerID: consumerID, ConsumingFrom: channel, CallbackURL: callbackURL.String(), MessageStakeholder: createMessageStakeholder(consumerID, token), Type: consumerType}
	return &consumer, nil
}
